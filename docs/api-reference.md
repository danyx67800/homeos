# HomeOS REST API

Base path `/api/v1`. The daemon binds `127.0.0.1:8790`; phase 1's Caddy config
is the only thing that reaches the LAN, and it owns TLS, access logging and the
RFC1918 guard.

Authentication is a bearer token from `POST /auth/login`:

```
Authorization: Bearer <token>
```

Errors are always `{"error": "<message written for a person>"}`. The dashboard
shows that text verbatim.

| Status | Meaning here |
|---|---|
| 400 | the request is malformed, or a device/share/env value failed validation |
| 401 | no token, or an expired one |
| 403 | the container exists but is not managed by HomeOS |
| 404 | no such container, app or icon |
| 409 | an operation on this app is already running, or setup was replayed |
| 502 | Docker is unreachable |

---

## Unauthenticated

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/health` | liveness, version, uptime, whether Docker is up, whether setup is done |
| `GET` | `/setup/status` | whether the first-run wizard should be shown |
| `POST` | `/setup` | create the one admin account; `409` if one exists |
| `POST` | `/auth/login` | `{username, password}` → `{token, expires_at}` |

```console
$ curl -s localhost:8790/api/v1/health
{"docker":true,"setup":true,"status":"ok","uptime":"3h14m","version":"1.0.0-phase2"}
```

## Session

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/auth/logout` | invalidate this token |
| `GET` | `/auth/me` | the logged-in username |
| `POST` | `/auth/password` | `{current, next}`; ends **all** sessions |

## System

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/system/info` | hostname, FQDN, timezone, architecture, Docker version, route mode |
| `GET` | `/system/metrics` | the latest telemetry snapshot |
| `GET` | `/system/metrics/history?samples=120` | recent snapshots for sparklines |
| `POST` | `/system/power/reboot` \| `/shutdown` | `202`, then acts after 2 s so the response lands |

A snapshot carries CPU (aggregate and per core), memory, swap, load average,
hwmon temperatures with one marked `primary`, fan RPM, per-interface network
rates, and mounted filesystems.

## Containers

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/containers?all=true` | managed containers; `all` includes unmanaged ones |
| `POST` | `/containers/:id/start` \| `/stop` \| `/restart` | lifecycle |
| `DELETE` | `/containers/:id?force=true` | remove; volumes are always kept |
| `GET` | `/containers/:id/logs?tail=200` | demultiplexed stdout+stderr |
| `GET` | `/apps` | the launcher view: one entry per app, with state, health and URL |

Mutating calls return `403` unless the target carries `homeos.managed=true`.

## App store

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/store?category=media` | catalogue entries runnable on this architecture |
| `GET` | `/store/:id` | full manifest plus whether it is installed |
| `GET` | `/store/:id/icon` | the icon file, cached for a day |
| `POST` | `/store/:id/install` | `{env: {...}}` → `202`; progress on the stream |
| `DELETE` | `/store/:id?purge=true` | uninstall; `purge` also deletes app data |
| `POST` | `/store/refresh` | pull the catalogue now |
| `GET` | `/store/jobs` | install/uninstall jobs with state and progress |

`GET /store` also returns `rejected`: manifests that failed validation, with the
reason, so a catalogue author can see why their app is missing.

## Storage

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/storage/disks` | disks, partitions, usage, cached SMART |
| `GET` | `/storage/disks/:device/health?force=true` | SMART for one device (`sda`, not `/dev/sda`) |
| `POST` | `/storage/format` | `{device, filesystem, label, confirm}` — **destructive** |
| `POST` | `/storage/mount` | `{device, name, persist}` → mounts under the storage root |
| `POST` | `/storage/unmount` | `{name}` |
| `POST` | `/storage/events` | called by phase 1's udev helper; advisory |

`confirm` must repeat the device path exactly, or the format is refused.

```console
$ curl -sX POST localhost:8790/api/v1/storage/format -H "Authorization: Bearer $T" \
    -d '{"device":"/dev/sdb","filesystem":"ext4","label":"media","confirm":"/dev/sdb"}'
{"partition":"/dev/sdb1","status":"formatted"}
```

## Shares

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/shares` | current SMB shares |
| `PUT` | `/shares` | `{shares: [...]}` replaces the whole set |

A whole-set `PUT` rather than per-share verbs because the generated file is
written atomically: the API shape matches what actually happens on disk. An
invalid set is rejected and the previous config rolled back.

## Streaming

| Transport | Path |
|---|---|
| WebSocket | `/ws/telemetry` |
| SSE | `/events` |

Both accept `?token=…` as well as the header, because browsers cannot set
headers on an `EventSource` or a WebSocket handshake — see
[phase2-backend.md §7](phase2-backend.md#7-authentication) for why that is
acceptable here and where it would not be.

Event types: `metrics` (every 2 s), `disks` (every 30 s and on hotplug),
`install` (on every install/uninstall state change).

```console
$ curl -sN "localhost:8790/events?token=$T"
retry: 5000

event: metrics
data: {"timestamp":"...","cpu":{"usage_percent":21.4,...}}
```
