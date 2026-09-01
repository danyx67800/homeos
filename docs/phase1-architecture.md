# HomeOS — Phase 1: System Architecture and Bootstrap

Phase 1 turns a minimal Debian 12 / Ubuntu 22.04+ install into an appliance
substrate: Docker, service discovery, file sharing, storage plumbing, a dynamic
reverse proxy, and the systemd wiring that starts it all. The dashboard and the
REST API that sit on top of it arrive in phases 2 and 3.

Nothing here assumes those later phases exist. `homeos-core.service` is
installed and enabled but carries `ConditionPathExists`, so a phase-1-only box
boots clean instead of crash-looping on a missing binary.

---

## 1. Layout, and why it is split this way

Four directories, four different lifetimes. The split is what makes the phase-4
OTA update safe: an update replaces two of them and never touches the other two.

| Path | Lifetime | Owner | Contents |
|---|---|---|---|
| `/usr/lib/homeos` | replaced on update | `root:root` | backend binary, helper scripts, shell libraries |
| `/opt/homeos/web` | replaced on update | `root:root` | built dashboard SPA |
| `/etc/homeos` | merged on update | `root:root` | `config.yaml`, proxy routes, Samba shares, secrets |
| `/var/lib/homeos` | never touched | `homeos:homeos` | app stacks, databases, user data, backups |
| `/mnt/storage` | never touched | `root:root` | mounted data disks and SMB shares |

```
/etc/homeos/
├── config.yaml              operator-editable; the single source of truth
├── homeos.env               generated; consumed by the systemd unit
├── apps/                    per-app config overrides
├── proxy/
│   ├── Caddyfile            base config, written by install.sh
│   └── routes.d/            generated per-app routes (homeos-proxy-sync owns this)
├── samba/shares.conf        generated share definitions, included by smb.conf
├── storage/                 mount and pool definitions
└── secrets/                 0700, homeos-owned: API token, app credentials

/var/lib/homeos/
├── apps/<app>/              compose file, .env and persistent volumes
├── data/                    shared user data (group homeos-share)
├── store/                   app-store git checkout
├── db/homeos.db             backend state
├── backups/  updates/  tmp/  storage-events/
```

`/etc/homeos/config.yaml` is deliberately flat enough that a `sed` one-liner can
read a scalar out of it. `homeos-proxy-sync` does exactly that, so the phase-1
shell tooling has no YAML dependency; the phase-2 backend parses it properly.

---

## 2. Networking: three concentric rings

```
                    LAN
                     │
                     │  :80 / :445 / mDNS
              ┌──────▼───────┐
              │    Caddy     │  host process, systemd-supervised
              │  (host net)  │
              └──────┬───────┘
                     │  10.20.0.0/24
         ┌───────────▼────────────┐
         │   homeos-edge bridge   │   every published app joins this
         └──┬──────────┬──────────┘
            │          │
    ┌───────▼──┐  ┌────▼─────┐
    │ jellyfin │  │ nextcloud│     each app also gets a private bridge
    └───┬──────┘  └────┬─────┘     from 10.21.0.0/16, carved into /24s
        │              │
   ┌────▼─────┐   ┌────▼─────┐
   │ (no peer)│   │ postgres │     private: reachable by its app, nobody else
   └──────────┘   └──────────┘
```

An app's sidecars sit on a bridge that only that app joins, so a compromised
container cannot reach another app's database. The shared edge network exists
only so the proxy has one route to every published service without publishing
host ports.

Docker's `default-address-pools` is pinned to `10.21.0.0/16` and `10.22.0.0/16`
in `daemon.json`. The default pool starts at `172.17.0.0/16`, which collides
with a surprising number of home routers and corporate VPNs.

### Why Caddy runs on the host, and the consequence

Caddy is a host process rather than a container so systemd supervises it like
everything else and it can bind :80/:443 without a privileged container.

The consequence is real and worth stating plainly: **Docker's embedded DNS is
not available to a host process**, so `reverse_proxy http://jellyfin:8096` does
not resolve. `homeos-proxy-sync` therefore resolves every backend to a concrete
address and rewrites the route file whenever Docker emits an event.

Resolution order, per container:

1. its IP on `homeos-edge` — the normal path
2. a published host port for the app port — for containers outside the fabric
3. its IP on any other bridge — last resort, for hand-rolled stacks

Because addresses change when a container is recreated, the watcher is not an
optimisation; it is what keeps routes correct.

---

## 3. Publishing an app

A container opts in with labels. Nothing else is required — no file to edit,
no restart of the proxy.

| Label | Default | Meaning |
|---|---|---|
| `homeos.enable` | — | `true` opts the container in |
| `homeos.app` | container name | slug used in URLs |
| `homeos.title` | app slug | display name for the dashboard |
| `homeos.port` | sole `EXPOSE`d port | container port to proxy to |
| `homeos.scheme` | `http` | upstream scheme |
| `homeos.route` | from `config.yaml` | `host` / `path` / `port` |
| `homeos.public_port` | app port | listen port, `port` mode only |
| `homeos.strip_prefix` | `true` | `path` mode: strip `/app/<slug>` |

### The three route modes, and when each is right

| Mode | URL | Cost | Use when |
|---|---|---|---|
| `host` | `http://jellyfin.local/` | one mDNS alias per app | default; the app assumes it owns `/` |
| `path` | `http://homenas.local/app/jellyfin/` | none | one hostname is enough and the app supports a base path |
| `port` | `http://homenas.local:8096/` | one host port | the app hard-codes absolute paths and ignores prefixes |

`host` is the default because most self-hosted apps generate absolute URLs from
`/` and break subtly under a path prefix — the page loads, then its assets
404. The cost is one `avahi-publish` process per app, which is cheap.

Aliases are published as *transient systemd units* (`systemd-run
--unit=homeos-mdns-<app>`), so teardown is exact: stop the unit and the name
stops resolving. There is no state file to drift.

### Failure handling

`homeos-proxy-sync` stages the complete desired route set in a temporary
directory, swaps it in, and runs `caddy validate`. If validation or reload
fails it restores the previous set and reloads again. One malformed label
cannot take the proxy down for every other app.

Events are debounced (2s by default): a single `docker compose up` emits several
events, and reloading per event would thrash the proxy.

---

## 4. Storage and Samba

`smb.conf` is split in two. `install.sh` owns `[global]`; every share lives in
`/etc/homeos/samba/shares.conf`, which `[global]` includes. The storage API
therefore never parses or rewrites `smb.conf` — it writes one generated file and
reloads.

Baseline choices worth knowing about:

- **SMB1 is off** (`server min protocol = SMB2_10`). It is unauthenticated by
  design and is where most historic SMB CVEs live.
- **`nmbd` is disabled.** Avahi already advertises the host; NetBIOS name
  service is legacy noise on a modern LAN.
- **`bind interfaces only`** with an explicit interface list, so Samba never
  listens on a Docker bridge.
- **Recycle bin on every share**, because a deleted holiday album that is
  actually gone is the worst NAS failure mode there is.

Disk hotplug goes through udev, but the rule does almost nothing: heavy work in
a udev rule blocks the event queue and can wedge device enumeration at boot. The
rule fires `systemd-run --no-block` and returns; the helper drops a JSON event
in a spool directory and pokes the API. If the backend is down the spool file
survives, and the backend reconciles from `lsblk` on start regardless.

---

## 5. Privilege model

`homeos-core` runs as the unprivileged `homeos` user, inside a systemd sandbox,
and reaches for root only through an explicit sudoers allowlist.

Two grants are root-equivalent and should be understood rather than glossed:

1. **`docker` group membership.** Anyone who can talk to the Docker socket can
   start a privileged container and own the host. Managing containers without it
   would need a brokered API, which is out of scope here.
2. **`mount` and `mkfs` in the sudoers file.** They take operator-supplied
   arguments. A NAS manager cannot avoid this.

So the sandbox is the compensating control, not the allowlist:

- `ProtectSystem=strict` with an explicit `ReadWritePaths`
- `ProtectHome`, `PrivateTmp`, `ProtectKernelTunables`, `ProtectKernelModules`
- `PrivateDevices=false` with `DeviceAllow=block-*` — `smartctl` needs raw block
  devices, so device isolation cannot be used here
- `MountFlags=shared` — without it, mounts made inside the unit's namespace
  would be invisible to the rest of the system

`NoNewPrivileges` is deliberately **off** on `homeos-core`: it would block the
sudo calls the storage and Samba managers depend on. It is **on** for
`homeos-proxy-sync`, which needs no such escalation.

The real security boundary is authentication on the API (phase 2) plus the
`homeos_lan_only` guard in Caddy, which refuses any request whose source is not
in RFC1918 space.

> **Caddy's directive ordering caught a live bug here.** Caddy sorts directives
> by its own precedence list, in which `respond` runs *after* `handle`. A bare
> `respond` guard would never fire, because the catch-all `handle` would already
> have served the request. The guard is wrapped in
> `handle @external { respond ... }` so it joins the mutually-exclusive group
> and is ordered as written.

---

## 6. Idempotency

Every stage converges rather than rebuilds, and reports what it skipped.

- `write_file` compares before writing and returns a distinct status when the
  content was already identical.
- `ensure_managed_block` replaces its own delimited block in a foreign file, in
  place, so the original inode, mode and ownership survive.
- `backup_file` keeps one `.homeos-orig` copy the first time HomeOS touches a
  file it did not author. `--uninstall` restores from those.
- Preflight runs to completion before anything is written, so a rejected system
  is left exactly as it was found.

`--dry-run` prints every action and changes nothing. `--force` overrides the
kernel, distribution and port-conflict refusals.

---

## 7. Deliberate limits of phase 1

- **No TLS by default.** `.local` names cannot be validated by a public CA. Add
  `tls internal` to a site block and trust Caddy's local root CA if you want LAN
  HTTPS; the cost is a manual trust step on every client.
- **No authentication yet.** The LAN guard is a network control, not an identity
  one. Authentication is a phase-2 deliverable, and until it lands anyone on the
  LAN can reach the API.
- **Kernel 5.15 is tolerated with a warning**, not refused. The brief asks for
  `>= 6.x`, but Ubuntu 22.04 LTS ships 5.15 and runs Docker, overlay2 and
  cgroup v2 correctly; refusing it would exclude a large installed base for no
  functional gain. Anything older is refused unless `--force`.
- **32-bit ARM is refused**, because the app catalogue publishes amd64/arm64
  images only.
- **Traefik is not implemented.** The brief allowed either; Caddy was chosen for
  its file-based config with glob imports, which makes generated per-app route
  files a natural fit and keeps `caddy validate` available as a pre-flight gate
  before every reload. Swapping engines means reimplementing
  `emit_site`/`emit_path` against Traefik's dynamic file provider — the rest of
  the sync loop is engine-agnostic.

---

## 8. What phase 2 plugs into

Phase 1 defines these contracts, and phase 2 fills them in:

| Contract | Provided by phase 1 | Consumed by phase 2 |
|---|---|---|
| `/usr/lib/homeos/bin/homeos-core` | unit file, sandbox, `ConditionPathExists` | the binary itself |
| `127.0.0.1:8790` | Caddy routes `/api/*`, `/ws/*`, `/events*` to it | the HTTP server |
| `/etc/homeos/config.yaml` | written with every key the backend needs | parsed at start-up |
| `/etc/homeos/secrets/api.token` | generated, `0600` | bearer auth for local helpers |
| Container labels | consumed by `homeos-proxy-sync` | emitted by the app-store installer |
| `/etc/homeos/samba/shares.conf` | included by `smb.conf`, created empty | rewritten by the storage API |
| `/var/lib/homeos/storage-events/` | udev drops JSON here | drained on start and on notify |
| `POST /api/v1/storage/events` | `homeos-disk-event` calls it | hotplug endpoint |
