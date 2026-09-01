# HomeOS — Quick Start

Four steps to a running appliance, then the handful of things you will actually
do afterwards. For Docker, building from source, release signing and the longer
troubleshooting list, see [deployment.md](deployment.md).

---

## 1. Before you start

| | |
|---|---|
| **Machine** | any x86_64 or ARM64 PC / mini PC / Raspberry Pi 4+ |
| **RAM** | 2 GB |
| **System disk** | 16 GB |
| **OS** | Debian 12+ or Ubuntu 22.04+, minimal install |
| **Network** | wired (Wi-Fi often blocks `.local` names) |

A second disk for your files is the whole point, but you can install without one
and add it later. **HomeOS never formats your system disk.**

---

## 2. Install

```bash
git clone https://github.com/danyx67800/homeos.git
cd homeos
sudo ./install.sh --hostname mynas --timezone Europe/Rome
```

That sets up Docker, the proxy, file sharing and the system services — but not
the HomeOS software itself, which has to be built.

Building needs Go 1.25+ and Node 18+. **No distribution packages a Go that
new**, so `install.sh` does not install one: an appliance has no business
carrying a compiler. Install the toolchain once, then build:

```bash
sudo scripts/install-build-deps.sh
make build
sudo make install
```

> Prefer not to put a compiler on the appliance? Build on any other machine
> with `make -C backend build-all` and `make -C web build`, then copy
> `backend/dist/homeos-core-linux-<arch>` to
> `/usr/lib/homeos/bin/homeos-core` and `web/dist/` to `/opt/homeos/web/`.

Open **`http://mynas.local`** and create your admin account.

> There is one account and **no password recovery**. Save the password somewhere
> you will still have it in two years.

**Useful flags**

| Flag | What it does |
|---|---|
| `--dry-run` | shows every action, changes nothing |
| `--hostname NAME` | the address becomes `NAME.local` |
| `--skip-docker` | keep a Docker you already installed |
| `--uninstall` | remove HomeOS, keep all your data |

Re-running `install.sh` is safe — it only changes what needs changing.

---

## 3. Add an app

From the dashboard: **Store → pick an app → Install**.

Or label any container yourself and it appears in the launcher:

```yaml
services:
  jellyfin:
    image: jellyfin/jellyfin:10.9.11
    networks: [homeos-edge]
    labels:
      homeos.enable: "true"
      homeos.app: "jellyfin"
      homeos.port: "8096"

networks:
  homeos-edge: { external: true }
```

It becomes reachable at `http://jellyfin.local` within a couple of seconds. No
proxy config, no restart.

---

## 4. Add a disk

**Storage → Format → Mount.** Formatting asks you to type the device path,
because it erases everything on that disk.

Then **Shares → New share** to expose a folder over the network. Windows, macOS
and Linux all connect without extra software.

---

*The four steps above are the setup. Everything below is what you do
afterwards, in no particular order.*

## Everyday commands

```bash
# Is everything running?
systemctl status homeos-core homeos-proxy-sync caddy

# What is the backend doing?
journalctl -u homeos-core -f

# Which apps have URLs?
homeos-proxy-sync list

# Check the config without starting anything
/usr/lib/homeos/current/bin/homeos-core -config /etc/homeos/config.yaml -check
```

---

## Updates

HomeOS checks daily, downloads in the background, and **waits for you** before
installing. Installing restarts the appliance.

**Settings → Software updates → Install**, or:

```bash
sudo systemctl start homeos-update-check   # check and download now
homeos-apply-update                         # install what was downloaded
```

If the new version does not come up healthy, **HomeOS puts the old one back on
its own**. You do not need to do anything.

Turn updates off by clearing `update.channel_url` in
`/etc/homeos/config.yaml`.

---

## Back up

Two things matter:

```bash
# Settings, shares, admin account, app passwords — a few kilobytes
sudo tar -czf homeos-config.tar.gz /etc/homeos

# App data — gigabytes. Stop the service first or databases corrupt.
sudo systemctl stop homeos-core
sudo tar -czf homeos-apps.tar.gz /var/lib/homeos/apps
sudo systemctl start homeos-core
```

Your files under `/mnt/storage` are yours — back them up however you already
back up files.

---

*Reference.*

## When something is wrong

| Symptom | Most likely cause |
|---|---|
| `mynas.local` does not resolve | Wi-Fi blocks mDNS — use the IP (`hostname -I`) |
| Dashboard says **Offline** | backend down (`journalctl -u homeos-core`) or the proxy is blocking WebSockets |
| An app has no URL | its container is missing `homeos.enable=true` |
| Storage says **unavailable** | `apt install util-linux smartmontools` |
| An update rolled back | that is the safety net working — reason in `journalctl -t homeos-update` |
| Cannot stop a container | HomeOS only touches what it created; use `docker stop` |

---

## Where things live

| Path | What |
|---|---|
| `/etc/homeos/config.yaml` | settings — **edit this one** |
| `/var/lib/homeos/apps/` | your apps and their data |
| `/mnt/storage/` | your disks and network shares |
| `/usr/lib/homeos/` | the software itself — managed by updates |

---

**Next:** [deployment.md](deployment.md) for Docker, building from source,
publishing your own release channel, and the full troubleshooting guide.
