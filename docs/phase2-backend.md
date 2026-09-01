# HomeOS — Phase 2: Backend Core Engine

Phase 1 built the substrate: Docker, mDNS, Samba, storage plumbing and a
reverse proxy that turns container labels into URLs. Phase 2 is the daemon that
drives all of it — `homeos-core`, a single static Go binary that drops into the
`/usr/lib/homeos/bin/homeos-core` slot the phase-1 unit file was already
waiting on.

The moment it lands, `ConditionPathExists` stops holding the unit back and the
appliance comes up complete.

---

## 1. Why Go, and why one binary

The unit file phase 1 wrote is `Type=notify` with `WatchdogSec=60`. That shaped
the choice more than language preference did:

- **One static binary** (`CGO_ENABLED=0`) means the phase-4 OTA update is a file
  replacement plus a `systemctl restart`, not a package transaction that can
  half-apply.
- **`sd_notify` readiness** is native, so `systemctl start homeos-core` blocks
  until the API is genuinely accepting connections rather than merely forked.
- **~23 MB resident, no runtime** matters on a 1 GB ARM box where every
  megabyte the daemon takes is one the user's apps do not get.
- **Cross-compilation** to `linux/arm64` from any machine, with no toolchain on
  the target.

`modernc.org/sqlite` was pulled in early and then dropped: the pure-Go driver
avoids cgo, but phase 2 has no relational data. One admin account is a JSON file
at mode `0600`; share definitions are a JSON file beside the generated Samba
config. Adding a database before there is a schema to put in it is how you end
up migrating an empty table.

---

## 2. Layout

```
backend/
├── cmd/homeos-core/       main, background workers, systemd integration
└── internal/
    ├── config/            /etc/homeos/config.yaml, defaults, validation
    ├── auth/              one admin account, server-side sessions
    ├── hub/               fan-out to every connected dashboard
    ├── telemetry/         CPU, memory, load, hwmon sensors, filesystems
    ├── storage/           lsblk, SMART, format, mount, fstab
    ├── samba/             generates the managed share file
    ├── dockerx/           Engine API wrapper + Compose driver
    ├── appstore/          manifest schema, catalogue, Compose generator
    └── api/               REST routes, middleware, WebSocket and SSE
```

Every package that touches the outside world takes its executor as an
interface or a function value, so the logic is testable without root, without
disks and without a Docker daemon. That is why the test suite runs on a laptop.

---

## 3. Telemetry: two cadences, deliberately

The cheap counters — CPU, memory, load, network, filesystem usage — are sampled
every two seconds and fanned out to the dashboard.

SMART is **not** part of that snapshot, and this is the single most important
decision in the telemetry package. `smartctl` against a sleeping drive spins it
up. A dashboard polling SMART every two seconds would keep every disk in the box
awake permanently, which is both a power draw and real mechanical wear. So:

- disk **topology** is polled every 30 seconds (`lsblk`, cheap)
- disk **health** is swept every 30 minutes, serially, with `smartctl -n standby`
  so a sleeping drive stays asleep and is reported as such
- the first sweep is delayed two minutes after boot, because spinning up every
  disk while the rest of the system is still competing for I/O is the worst
  possible moment
- the API serves cached health, with `?force=true` for an explicit "check now"

### Sensors

Temperatures and fan speeds are read from `/sys/class/hwmon` in one pass rather
than through gopsutil, for two reasons: gopsutil cannot report fan RPM at all,
and doing both in one walk keeps the chip/label association consistent.

Picking *the* CPU temperature out of a dozen sensors is guesswork, so it is
guesswork with rules: `coretemp` "Package id 0" and `k10temp` "Tctl" are whole-die
readings and win; per-core sensors are noisier and less useful on a dial;
`cpu_thermal` covers the Raspberry Pi; `acpitz` is a last resort because it is
frequently the board, not the CPU. Exactly one reading is marked `primary`.

A sensor reading exactly 0 °C is dropped — it is almost always an unpopulated
channel. A fan reading 0 RPM is **kept**, because a stopped fan is real
information.

The parser takes its sysfs root as a parameter, so it is exercised against
fixture trees for Intel, AMD and Raspberry Pi layouts.

---

## 4. Docker orchestration

### The guard rail

`homeos-core` runs with full Docker API access — that is unavoidable for a
container manager, and phase 1 documented it as root-equivalent. The
compensating control here is that **every mutating call first checks the target
carries `homeos.managed=true`**:

```go
func (c *Client) requireManaged(ctx, id) (string, error)
```

Without it, a bug or a crafted request could stop a container the user runs by
hand, or one belonging to an entirely unrelated tool. Stopping the wrong
container is not catastrophic; silently doing it to something HomeOS never
created would be a breach of what an appliance is allowed to touch.

Removal never takes volumes with it. An app's data outlives its container, and
deleting data is a separate, explicit act.

### Compose, not a Go library

Stacks are driven through the `docker compose` CLI plugin rather than an
in-process Compose implementation. It is what the operator has installed, what
the generated file is validated against, and what they will reach for when
debugging by hand. Reimplementing its semantics would mean HomeOS and the
operator seeing different behaviour from the same file.

### Degrading instead of dying

An unreachable Docker socket is a **degraded** state, not a fatal one.
Telemetry, storage and shares are all still useful and manageable without it,
and dockerd may simply be slow to start. So the daemon logs it, serves a clear
`502 Docker is not available: …` from container routes, reports `"docker": false`
on `/health`, and retries the connection every 15 seconds in the background.

This was found by running the daemon, not by reading it: the first version
refused to start at all when the socket was missing.

---

## 5. App store

The manifest schema is documented in full in
[app-manifest-schema.md](app-manifest-schema.md). What matters architecturally
is where the boundary sits.

A manifest says **what an app is**. It never says how to run it. Networks,
labels, volume paths, security options and generated secrets are all derived by
the backend. A catalogue author cannot expose a database by writing the wrong
network list, because they never get to write a network list at all:

```
app service    -> networks: [edge, private]   labels: homeos.enable=true
sidecar        -> networks: [private]         labels: homeos.role=sidecar
edge network   -> external, shared with the proxy
private network-> internal: true, one per app
```

`internal: true` on the per-app bridge means a sidecar cannot reach the LAN or
the internet at all. No `ports:` mapping is ever emitted — the reverse proxy is
the only ingress, which is exactly what lets an app be reachable at
`http://nextcloud.local/` without opening a host port.

The generated labels are phase 1's contract, consumed by `homeos-proxy-sync`.
There is a test asserting the exact key/value pairs, because changing one
without changing the sync script would silently unpublish every app on the box.

### Catalogue as a git checkout

`git clone --depth 1` into `/var/lib/homeos/store`, fast-forwarded on a timer.
A checkout rather than a packaged index makes the catalogue forkable, diffable
and reviewable, and updating it is a pull rather than a bespoke protocol.

Two failure modes are handled deliberately:

- **A network failure keeps the existing checkout.** A box whose internet is
  down still needs its apps managed.
- **One broken manifest does not take the store offline.** It is recorded with
  its parse error and skipped; `GET /api/v1/store` returns those under
  `rejected` so a contributor can see why their app is missing.

`GIT_TERMINAL_PROMPT=0` is set, because a private repository URL would otherwise
hang the sync on a credential prompt nothing can answer.

### Install-time values

Three rules, in order: an explicit answer wins; a `password` field marked
`generate` gets a fresh random value when left blank; otherwise the manifest
default applies. A required field still empty at the end is an error raised
*before* anything is written — not a container crash-looping ten seconds later.

Generated secrets are per-installation, which is the point: a documented default
password that every installation shares is worse than none, because it looks
deliberate.

Installs run detached from the HTTP request. The browser closing its connection
must not abort a pull already in flight, so `POST /store/:id/install` returns
`202` immediately and progress arrives on the telemetry stream — the same
connection the dashboard already holds open, rather than a second polling loop.

---

## 6. Storage: validation is the security boundary

Everything privileged goes through `sudo` against the allowlist phase 1 wrote to
`/etc/sudoers.d/homeos`. Commands are always built as an explicit argv and never
handed to a shell, so there is no word-splitting to defeat.

That still leaves the *arguments*. `mkfs.ext4 /etc/shadow` needs no shell to be
catastrophic. So every device path is validated against a strict pattern before
it reaches a command, and `path.Clean` is applied first so `/dev/../etc/shadow`
normalises out of `/dev` and is then rejected — rather than sneaking through as
a string that merely starts with `/dev/`.

The test suite includes twenty adversarial inputs: traversal, shell
metacharacters, null-byte truncation, newline injection, whitespace argument
smuggling, wrong-case names, and real devices HomeOS deliberately does not
manage (`/dev/mapper/*`, `/dev/loop0`).

Formatting additionally requires `confirm` to repeat the device path. It is the
one operation that destroys every byte on a disk, and a mistyped device name in
a JSON body should not be enough to trigger it.

### fstab

Entries are written by **UUID, never device path**: `/dev/sdb` becomes
`/dev/sdc` when a USB disk is replugged or the SATA ports enumerate differently,
and a stale path in fstab makes the machine fail to boot.

Two options are not optional:

| Option | Without it |
|---|---|
| `nofail` | a disk that is unplugged at boot drops the machine to an emergency shell |
| `x-systemd.device-timeout=10` | boot hangs waiting for a disk that is not coming back |

Only the delimited HomeOS block is rewritten; operator lines are preserved
byte for byte. `renderFstab` is a pure function with its own tests, including
idempotency, because getting it wrong makes a machine unbootable.

### Samba

Phase 1 split `smb.conf` so `[global]` is installer-owned and `include`s a
generated share file. The daemon therefore never parses or rewrites `smb.conf`
— it regenerates one file it fully controls, validates with `testparm`, and
**rolls back on failure**.

Share names reach the file as section headers, so a name containing `]` or a
newline would inject arbitrary Samba directives. Names, paths, comments and user
names are all validated; paths are confined to `/mnt/storage` and
`/var/lib/homeos/data`.

`Public` must be set explicitly. It is never inferred from an empty user list,
because that is how somebody publishes their files by forgetting a field.

---

## 7. Authentication

Phase 1 shipped with the API reachable by anyone on the LAN and said so plainly.
This closes it.

The model is deliberately small: **one admin account, server-side sessions, no
registration flow**. A home appliance with one owner does not need roles, and
every extra concept is another way to get authorisation wrong.

- bcrypt at default cost
- `Setup` refuses once an account exists, so an unauthenticated caller cannot
  reset the box by replaying the first-run wizard
- login compares the password hash even when the username is wrong, so a wrong
  username and a wrong password take the same time
- one error message for every failure: distinguishing "no such user" from
  "wrong password" tells an attacker which half to keep trying
- changing the password invalidates every session, because that is what you do
  when you think someone else has access

### The token-in-URL trade-off

Neither `EventSource` nor the WebSocket constructor lets a browser set an
`Authorization` header. So `/events` and `/ws/telemetry` also accept
`?token=…`.

That puts a session token in a URL, which is normally a mistake. It is
acceptable here only because the URL never leaves loopback: Caddy proxies to
`127.0.0.1`, and the access log it writes is on the same trusted box. It would
not be acceptable over the public internet, and if HomeOS ever grows remote
access this is the first thing that has to change.

---

## 8. Streaming

One hub, two transports. WebSocket is primary; SSE is the fallback for
environments where a proxy mangles upgrades.

The property that matters is that **a stalled browser tab cannot stall the
collector**. Each subscriber has a buffered channel; `Publish` never blocks, and
a subscriber whose buffer is full misses that event and is counted. Telemetry is
a stream of samples — dropping a stale one is strictly better than wedging the
sampler for everyone else. There is a test that hammers this.

The hub also replays the latest event *per type* to a new subscriber, so a
dashboard opened between ticks renders immediately instead of showing empty
dials for two seconds.

Two details that are easy to get wrong and were not:

- The WebSocket handler runs a reader goroutine even though the client sends
  nothing. Without it, close and pong frames are never processed and a dead
  connection is never noticed.
- SSE sets `X-Accel-Buffering: no` and emits a `retry:` directive. Without the
  first, proxies buffer the stream and nothing arrives until close; without the
  second, browsers reconnect aggressively enough to hammer a restarting daemon.

Install progress rides the same stream as telemetry (`type: "install"`), so the
dashboard needs one connection rather than a second polling loop.

---

## 9. Testing

```
$ make test          # go test -race ./...
ok  internal/appstore    ok  internal/samba
ok  internal/config      ok  internal/storage
ok  internal/hub         ok  internal/telemetry
```

Coverage is concentrated where a mistake is expensive rather than spread evenly:

| Area | What is asserted |
|---|---|
| device validation | 20 adversarial paths, including traversal and injection |
| fstab rendering | operator lines preserved, idempotent, sorted, empty-file safe |
| SMART | a drive reporting `passed: true` with 12 reallocated sectors reads as **degraded** |
| hwmon | Intel, AMD and Raspberry Pi sensor layouts; 0 °C dropped, 0 RPM kept |
| Compose generation | the exact phase-1 label contract; sidecars never routable; no secret leaks between services |
| hub | a slow subscriber is dropped, not blocking; no goroutine leaks under concurrent churn |
| Samba | share-name injection into section headers is refused |
| config | loads the exact file `install.sh` renders, so the two cannot drift |

Two bugs the tests caught before the code ever ran on Linux:

1. `filepath.IsAbs` and `filepath.Split` follow **host** rules, so `/var/lib/homeos`
   read as a relative path on a Windows development machine. HomeOS paths are
   always POSIX; the `path` package is the correct one.
2. The env-key pattern required upper snake case, which would have rejected real
   images — Jellyfin ships `JELLYFIN_PublishedServerUrl`.

---

## 10. Deliberate limits of phase 2

- **No dashboard yet.** Every endpoint here has been exercised with `curl`;
  phase 3 is the UI that consumes them.
- **No app updates.** `installed.json` records the manifest and resolved
  environment specifically so an update can diff against what is actually
  installed, but the update path itself is phase 4.
- **Dependencies are validated, not resolved.** A manifest may declare
  `dependencies`, and they are checked for well-formedness, but nothing installs
  them automatically yet.
- **No RAID or pooling.** Single disks, formatted and mounted. btrfs is offered
  as a filesystem but its multi-device features are not exposed.
- **Sessions are in memory.** A daemon restart logs everyone out. For a
  single-user appliance that restarts on updates, that is the right trade
  against persisting session state.
- **Still no TLS**, unchanged from phase 1: `.local` cannot be validated by a
  public CA, and `tls internal` needs a manual trust step on every client.
