# Deploying HomeOS

> **Just want to get it running?** Start with the
> [Quick Start](quickstart.md) — it covers installing, adding an app, adding a
> disk, updating and backing up in two pages. Come back here for the detail.

Three ways to run HomeOS, in the order most people will want them:

1. [**Bare metal**](#bare-metal) — the real thing, on a spare PC or SBC
2. [**Docker**](#docker) — the whole stack in containers, for trying it out
3. [**From source**](#building-from-source) — for working on it

---

## Bare metal

### What you need

| | |
|---|---|
| Hardware | x86_64 or aarch64, 2 GB RAM, 16 GB system disk |
| OS | Debian 12+ or Ubuntu 22.04+, minimal install, SSH reachable |
| Network | wired is strongly preferred; mDNS over Wi-Fi is unreliable on many routers |
| Ports | 80, 443 and 445 free |

A second disk for data is not required to install, but it is the point of the
exercise. HomeOS never formats your system disk.

### Install

```bash
git clone https://github.com/danyx67800/homeos.git
cd homeos
sudo ./install.sh --hostname mynas --timezone Europe/Rome
```

The installer is idempotent and reports what it skipped, so re-running it after
a change is safe. `--dry-run` prints every action without making any.

It does not build the backend or the dashboard. On a released appliance those
arrive as a signed release; from a source checkout, build them:

```bash
make build
sudo make install
```

Then open `http://mynas.local` and complete the first-run wizard. There is one
administrator account and no password recovery, so put the password somewhere
you will still have it in two years.

### What ends up where

| Path | Lifetime | Contents |
|---|---|---|
| `/usr/lib/homeos/releases/<version>/` | one per release | binary, helper scripts, dashboard |
| `/usr/lib/homeos/current` | symlink | the release in use — swapping it *is* the update |
| `/etc/homeos/` | merged on update | `config.yaml`, proxy routes, share definitions, secrets |
| `/var/lib/homeos/` | never touched | app stacks, app data, database, staged releases |
| `/mnt/storage/` | never touched | your disks |

`/usr/lib/homeos/bin` and `/opt/homeos/web` are symlinks into `current`, so
every path the earlier phases documented still resolves and a `make install`
lands inside the current release.

### Services

```bash
systemctl status homeos-core homeos-proxy-sync caddy smbd avahi-daemon
journalctl -u homeos-core -f
```

| Unit | Role |
|---|---|
| `homeos-core` | the backend: API, telemetry, orchestration, storage |
| `homeos-proxy-sync` | watches Docker events and rewrites Caddy routes |
| `homeos-update-check.timer` | daily update check (staging only, never applies) |
| `homeos-update-apply` | one-shot: swaps the release symlink and restarts |
| `homeos-firstboot` | one-shot image initialisation, self-disabling |

---

## Docker

For trying HomeOS out, or for developing it.

```bash
docker compose -f docker-compose.demo.yml up --build   # built dashboard
docker compose -f docker-compose.dev.yml  up --build   # hot reload
```

Both land on <http://localhost:8080>.

The backend runs against the host's Docker socket, so container management and
the app store work exactly as they do on an appliance. Three things do not, and
the containers say so rather than pretending:

- **Storage.** Formatting and mounting need the host's block devices and root.
  The API answers `503`, the same honest response an LXC install gets.
- **Samba.** No `smbd` in the image. The share API validates and writes its
  config; nothing serves it.
- **mDNS and updates.** Both are systemd-shaped. Rebuild the image instead.

> The Docker socket is mounted read-write, which is root on the host. Run this
> on a development machine, not on anything you care about.

---

## Building from source

```bash
make build     # backend (Go 1.25+) and dashboard (Node 22+)
make test      # go test -race ./...
make -C web e2e   # 21 browser checks against a running daemon
```

Cross-compiling for the appliance from any machine:

```bash
make -C backend build-all   # dist/homeos-core-linux-{amd64,arm64}
```

`CGO_ENABLED=0` throughout, so the binary is static and runs on glibc and musl
alike without matching the build host.

---

## Updates

### How an update actually happens

```
  releases/1.0.0/     releases/1.1.0/          ← both on disk
        ▲                   ▲
        └── current ────────┘                  ← a symlink; swapping it is the update
```

1. The daemon fetches the channel manifest and picks the newest release it can
   apply.
2. It downloads the archive, checks the SHA-256, and **verifies an ed25519
   signature** against the key compiled into the running binary.
3. It unpacks into `releases/<version>.staging`, refusing any archive entry that
   is a link, a device, or a path containing `..`.
4. It runs the new binary's `-version` as a sanity check. A build that will not
   execute on this machine is rejected now rather than after the swap.
5. The staged directory is renamed into place. **Nothing live has changed yet.**

Applying is a separate, deliberate act:

6. `homeos-update-apply.service` renames the `current` symlink — atomic on
   POSIX — and restarts `homeos-core`.
7. It polls `/api/v1/health` for 90 seconds.
8. **If the new build does not become healthy, it points the symlink back and
   restarts the old one.** The outcome is written to
   `/var/lib/homeos/updates/last-result.json`, which the dashboard reads on its
   next start — so a rollback the daemon was not alive to observe is still
   reported.

The applier runs as a separate process because a daemon cannot supervise its own
replacement.

### Configuring it

```yaml
update:
  channel_url: https://updates.homeos.dev/stable.json   # clear this to turn updates off
  auto_check: true       # downloads and stages; never restarts anything
  auto_apply: false      # restarts the appliance unattended when an update lands
  keep_releases: 3       # older release directories are pruned
```

`auto_apply` is off by default. Applying restarts the box, and a NAS in the
middle of a transfer is a bad moment to be surprised by that.

### Manually

```bash
sudo systemctl start homeos-update-check      # check and stage now
homeos-apply-update                            # apply the staged release
```

### Rolling back on purpose

```bash
sudo ln -sfn "$(readlink -f /usr/lib/homeos/previous)" /usr/lib/homeos/current.new
sudo mv -Tf /usr/lib/homeos/current.new /usr/lib/homeos/current
sudo systemctl restart homeos-core
```

`previous` always points at the release that was current before the last swap.

---

## Publishing your own release channel

Anyone running a fork needs their own signing key. Generate it **once** and back
it up: losing it means every appliance in the field stops trusting new releases,
and there is no recovery except reinstalling them.

```bash
make -C backend release-tool
backend/homeos-release keygen -out ~/.homeos
```

Build and sign:

```bash
make release VERSION=1.1.0 BASE_URL=https://dl.example.com/homeos
```

That produces, in `dist/`:

```
homeos-1.1.0-linux-amd64.tar.gz
homeos-1.1.0-linux-arm64.tar.gz
stable.json
```

Publish all three at `BASE_URL`. Appliances polling that URL offer the update on
their next check.

The public half of the key is compiled into every binary it signs, so an
appliance verifies against the key that signed its own release rather than one
it is told about at run time. `update.public_key_file` overrides it for a
self-hosted channel.

Archives are built with `--sort=name --mtime=@0 --owner=0 --group=0`, so the
same source tree produces byte-identical archives and a rebuilt release still
verifies against its published signature.

Verify before publishing:

```bash
backend/homeos-release verify -pub ~/.homeos/homeos-update.pub \
    -manifest dist/stable.json -dir dist
```

---

## Backup and restore

Two directories matter, and they have different reasons:

| Path | Why | Size |
|---|---|---|
| `/etc/homeos/` | configuration, share definitions, the admin account, generated app passwords | kilobytes |
| `/var/lib/homeos/apps/` | every app's data and its `installed.json` | gigabytes |

```bash
sudo tar -czf homeos-config-$(date +%F).tar.gz /etc/homeos
sudo systemctl stop homeos-core
sudo tar -czf homeos-apps-$(date +%F).tar.gz /var/lib/homeos/apps
sudo systemctl start homeos-core
```

Stopping the daemon for the app backup is not superstition: a database
mid-write produces an archive that restores into a corrupt database.

Restoring onto a fresh install: run `install.sh`, restore both archives, then
`systemctl restart homeos-core`. Each app's `installed.json` records the
manifest and the resolved environment it was installed with, so reinstalling
reproduces the same stack rather than a default one.

Everything under `/mnt/storage` is your own data and is not HomeOS's to back up.

---

## Troubleshooting

**`homenas.local` does not resolve.** mDNS is blocked on many Wi-Fi networks
with client isolation on, and Windows only learned it natively in Windows 10.
Use the IP address; find it with `hostname -I` on the box. Check the responder
with `avahi-browse -at | grep -i homeos`.

**The dashboard loads but shows "Offline".** The page is served but the
telemetry stream is not connecting. `journalctl -u homeos-core -n 50` first;
if the daemon is up, something between it and the browser is not passing
WebSocket upgrades — the dashboard falls back to SSE after two failures, so a
persistent "Offline" means neither transport is getting through.

**Apps install but have no URL.** `homeos-proxy-sync list` shows what routing
believes. If the app is absent, its container is missing `homeos.enable=true`.
If it is listed but unreachable, check `caddy validate --config
/etc/homeos/proxy/Caddyfile` and `journalctl -u homeos-proxy-sync`.

**Storage says it is unavailable.** A `503` means `lsblk` or `smartctl` is not
installed, which happens on container-based installs (LXC, Proxmox
unprivileged) where phase 1's preflight already warned. `apt install
util-linux smartmontools` if they are genuinely missing.

**An update rolled back.** That is the design working. The reason is in
`/var/lib/homeos/updates/last-result.json` and in `journalctl -t homeos-update`.
The appliance is running the previous release and is not in a degraded state.

**A container will not stop from the dashboard.** A `403` means it does not
carry `homeos.managed=true`. HomeOS refuses to touch containers it did not
create; stop it with `docker stop` instead.

**The daemon will not start after an update.** The applier should have rolled
back automatically. If it did not:

```bash
journalctl -u homeos-core -n 100
ls -l /usr/lib/homeos/current /usr/lib/homeos/previous
/usr/lib/homeos/current/bin/homeos-core -config /etc/homeos/config.yaml -check
```

`-check` validates the configuration and the environment without starting
anything.

---

## Uninstalling

```bash
sudo ./install.sh --uninstall
```

Removes the services, the units, the sudoers drop-in and the udev rules, and
restores every file it replaced from the `.homeos-orig` copies it made.

It deliberately keeps `/var/lib/homeos`, `/etc/homeos` and `/mnt/storage`.
Removing your data is a separate decision, and one the uninstaller is not going
to make for you.
