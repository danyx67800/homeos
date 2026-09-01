# HomeOS

A self-hosting and NAS appliance built on Debian 12 / Ubuntu 22.04+: Docker app
hosting with one-click installs, SMB file sharing, disk management, and a web
dashboard reachable at `http://homenas.local`.

**Status: complete.** The appliance installs, runs, and updates itself over the
air with automatic rollback. Everything below has been built and tested.

---

## Install

**The easy way** — flash a prebuilt image, no Linux setup at all. Download from
[the latest release](https://github.com/danyx67800/homeos/releases/latest):
`amd64` for a PC or mini PC, `arm64` for a Raspberry Pi 4/5.

```bash
# Check what you downloaded before writing it to a disk
sha256sum -c homeos-<version>-amd64.img.xz.sha256

# On another machine: write the image to the appliance's disk
xzcat homeos-<version>-amd64.img.xz | sudo dd of=/dev/sdX bs=4M conv=fsync status=progress
```

Or boot the `.iso` from a USB stick and let the installer write to the internal
disk. Both are built by CI — see
[docs/building-images.md](docs/building-images.md).

**From source**, on a fresh minimal Debian 12 or Ubuntu 22.04+ system:

```bash
git clone https://github.com/danyx67800/homeos.git
cd homeos
sudo ./install.sh
```

Useful variations:

```bash
sudo ./install.sh --hostname mynas --timezone Europe/Rome --yes
sudo ./install.sh --dry-run --debug      # show every action, change nothing
sudo ./install.sh --route-mode path      # one hostname, apps under /app/<name>/
sudo ./install.sh --skip-docker          # keep an existing Docker install
sudo ./install.sh --uninstall            # remove HomeOS, keep all user data
```

The installer is idempotent: re-run it after a config change and it converges
the system, reporting what it skipped.

### Requirements

| | |
|---|---|
| Architecture | x86_64 or aarch64 (32-bit ARM is refused) |
| OS | Debian 12+, Ubuntu 22.04+ |
| Kernel | 6.x recommended; 5.15 works with a warning |
| Disk | 6 GB free on `/var` |
| RAM | 1 GB minimum, 2 GB+ realistic |
| Ports | 80, 443 and 445 must be free |

---

## What you get

- **Docker Engine + Compose V2** from the upstream repository, with log rotation
  and a non-colliding address pool configured out of the box.
- **`homenas.local`** over mDNS, advertised so phones and Finder discover it.
- **A dynamic reverse proxy.** Label a container and it gets a URL — no config
  file to edit, no proxy restart.
- **SMB file sharing**, SMB2+ only, with a recycle bin on every share.
- **Disk hotplug detection** through udev, spooled so nothing is lost while the
  backend is down.
- **systemd units** for everything, sandboxed, with an unprivileged service
  account and an explicit sudoers allowlist.
- **A REST + WebSocket backend** in one static Go binary: telemetry, container
  orchestration, a git-backed app store, and disk and share management.
- **A dashboard** at 46 KB gzipped — live gauges, an app launcher, the store,
  and storage and share panels.
- **Over-the-air updates** with signature verification and automatic rollback.
- **Flashable images and an installer ISO**, built for amd64 and arm64 in CI.

---

## Publishing an app

Add labels to any container. That is the whole contract.

```yaml
services:
  jellyfin:
    image: jellyfin/jellyfin:10.9.11
    networks: [homeos-edge, internal]
    labels:
      homeos.enable: "true"
      homeos.app: "jellyfin"
      homeos.port: "8096"

networks:
  homeos-edge: { external: true }
  internal:    { driver: bridge, internal: true }
```

```bash
docker compose up -d
homeos-proxy-sync list
# APP        MODE   URL                       UPSTREAM               CONTAINER
# jellyfin   host   http://jellyfin.local/    http://10.20.0.5:8096  jellyfin
```

The watcher picks up the container within a couple of seconds; `homeos-proxy-sync
sync` forces it immediately. A complete worked example is in
[`examples/jellyfin.compose.yml`](examples/jellyfin.compose.yml).

Full label reference and the three routing modes:
[docs/phase1-architecture.md](docs/phase1-architecture.md#3-publishing-an-app).

---


---

## Building the backend

`install.sh` lays out the tree and enables `homeos-core.service`, which stays
dormant until the binary exists. Build and drop it in:

```bash
sudo scripts/install-build-deps.sh   # Go 1.25+ and Node 18+, one time
cd backend
make                                 # vet, test, build
sudo make install
sudo systemctl restart homeos-core
```

Go comes from go.dev rather than apt: HomeOS needs 1.25 and the newest any
supported distribution packages is 1.24. `install.sh` does not install it on
purpose — an appliance should not be carrying a compiler, and the normal way to
get HomeOS onto a box is a prebuilt release.

Cross-compiling for the appliance from any machine:

```bash
make build-all   # dist/homeos-core-linux-{amd64,arm64}
```

The output is a single static binary (`CGO_ENABLED=0`), which is what makes the
phase-4 OTA update a file replacement rather than a package transaction.

Nothing else is needed on the target beyond what `install.sh` already put there.

### Driving the API

```bash
# First run: create the admin account
curl -sX POST localhost:8790/api/v1/setup \
     -H 'Content-Type: application/json' \
     -d '{"username":"marco","password":"a-long-enough-passphrase"}'

T=$(curl -sX POST localhost:8790/api/v1/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"marco","password":"a-long-enough-passphrase"}' | jq -r .token)

curl -s -H "Authorization: Bearer $T" localhost:8790/api/v1/system/metrics | jq .cpu
curl -s -H "Authorization: Bearer $T" localhost:8790/api/v1/storage/disks | jq '.disks[].path'
curl -sN "localhost:8790/events?token=$T"          # live telemetry stream
```

Full surface: [docs/api-reference.md](docs/api-reference.md).

---

## Building the dashboard

```bash
cd web
make                 # npm install + vite build
sudo make install    # rsync dist/ to /opt/homeos/web
```

Caddy serves it from there. `homeos-core` also serves the same directory as a
fallback, so the dashboard still comes up on `http://<host>:8790` when Caddy is
stopped — which is exactly when you need it.

Development runs Vite with a proxy to the daemon:

```bash
cd web && npm run dev        # http://localhost:5173
```

End-to-end checks drive a real browser against a running daemon:

```bash
npm run e2e                  # 21 assertions; screenshots land in web/shots/
```


---

## Updating

HomeOS updates itself over the air, and rolls back on its own if the new
version does not come up healthy.

```bash
sudo systemctl start homeos-update-check    # check and stage now
homeos-apply-update                          # apply the staged release
```

Or from the dashboard: **Settings → Software updates**.

Releases are versioned directories and `current` is a symlink, so applying an
update is an atomic rename and rolling back is renaming it back. Archives are
verified against an ed25519 signature before anything is unpacked. Your apps,
data and settings are never touched.

Configure it in `/etc/homeos/config.yaml`; clear `update.channel_url` to turn
it off. Full detail in [docs/deployment.md](docs/deployment.md#updates).

---

## Running it in Docker

```bash
make dev     # hot reload, http://localhost:8080
make demo    # the built dashboard, as an appliance serves it
```

Container orchestration and the app store work for real against the host's
Docker socket. Storage, Samba, mDNS and updates do not — the stack says so
rather than pretending.


## Operating it

```bash
homeos-proxy-sync list       # routes that would be generated right now
homeos-proxy-sync status     # proxy health and config validity
homeos-proxy-sync sync       # force a regeneration + reload

systemctl status homeos-core homeos-proxy-sync caddy
journalctl -u homeos-proxy-sync -f
```

| Where | What |
|---|---|
| `/etc/homeos/config.yaml` | main configuration — edit this |
| `/etc/homeos/proxy/routes.d/` | generated routes — do not edit |
| `/var/lib/homeos/apps/` | per-app stacks and persistent data |
| `/mnt/storage/` | mounted data disks and SMB shares |
| `/var/log/homeos/` | installer transcript and proxy sync log |

---

## Repository layout

```
install.sh                       unified installer, idempotent
scripts/
├── lib/                         shell libraries, sourced by install.sh
├── homeos-proxy-sync            container -> route projection
├── homeos-disk-event            udev hotplug notifier
├── homeos-firstboot             one-shot image initialisation
├── homeos-apply-update          atomic release swap with rollback
├── homeos-update-check          nudges the daemon to check for updates
└── build-release.sh             signed release archives + channel manifest
config/
├── systemd/                     homeos-core, homeos-proxy-sync, homeos-firstboot
├── sudoers/homeos               privilege delegation, visudo-validated on install
└── udev/99-homeos-storage.rules block device hotplug
backend/                         homeos-core, a single static Go binary
├── cmd/homeos-core/             main, background workers, systemd integration
└── internal/
    ├── config/                  /etc/homeos/config.yaml
    ├── auth/                    one admin account, server-side sessions
    ├── hub/                     fan-out to connected dashboards
    ├── telemetry/               CPU, memory, hwmon sensors, filesystems
    ├── storage/                 lsblk, SMART, format, mount, fstab
    ├── samba/                   generates the managed share file
    ├── dockerx/                 Engine API wrapper + Compose driver
    ├── appstore/                manifest schema, catalogue, Compose generator
    └── api/                     REST routes, middleware, WebSocket and SSE
web/                             dashboard SPA (Svelte 5 + Vite + Tailwind 4)
├── src/lib/                     API client, telemetry stream, state, formatting
├── src/components/              gauge, sparkline, tiles, modal, menu, toasts
├── src/views/                   sign-in, dashboard, store, storage, shares, settings
└── e2e.mjs                      Playwright checks against a running daemon
image/                           appliance image and installer ISO builders
docker/                          containerised stack (dev + demo)
docs/
├── quickstart.md                the two-page version
├── deployment.md                install, build, update, back up, troubleshoot
├── phase1-architecture.md       substrate design decisions
├── phase2-backend.md            daemon design decisions
├── phase3-dashboard.md          dashboard design decisions
├── phase4-packaging.md          update model and release pipeline
├── api-reference.md             REST + streaming surface
└── app-manifest-schema.md       homeos-app.yml contract
examples/jellyfin.compose.yml    worked example of the label contract
```

## Documentation

The first four are practical; the phase documents explain why things are built
the way they are.

| Document | What it covers |
|---|---|
| [quickstart.md](docs/quickstart.md) | **start here** — install, add an app, add a disk, update, back up |
| [building-images.md](docs/building-images.md) | the flashable image and the installer ISO, and how CI builds them |
| [deployment.md](docs/deployment.md) | the full version: Docker, building from source, release signing, troubleshooting |
| [app-manifest-schema.md](docs/app-manifest-schema.md) | `homeos-app.yml`, and what the backend generates from it |
| [api-reference.md](docs/api-reference.md) | every endpoint, status codes, streaming |
| | |
| [phase1-architecture.md](docs/phase1-architecture.md) | layout, network fabric, routing modes, privilege model |
| [phase2-backend.md](docs/phase2-backend.md) | telemetry cadences, Docker guard rails, storage validation |
| [phase3-dashboard.md](docs/phase3-dashboard.md) | streaming transport, design decisions, the five views |
| [phase4-packaging.md](docs/phase4-packaging.md) | the update model, signing, reproducible releases |

## Roadmap

| Phase | Scope | Status |
|---|---|---|
| 1 | OS bootstrap, Docker, mDNS, Samba, dynamic reverse proxy | **done** |
| 2 | Backend: REST + WebSocket, telemetry, orchestration, app store, storage API | **done** |
| 3 | Dashboard SPA: widgets, launcher, marketplace, storage panel | **done** |
| 4 | OTA updates, dev stack dockerisation, deployment docs | **done** |

---

## Licence

MIT — see [LICENSE](LICENSE).
