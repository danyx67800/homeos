# HomeOS App Manifest (`homeos-app.yml`)

The manifest is the contract between a catalogue author and HomeOS. It is
declarative on purpose: it describes what an app *is* and what it *needs*, never
how to install it. Networks, labels, volume paths and generated secrets are all
derived by the backend, so an app cannot opt out of the isolation model by
writing its own Compose file.

A manifest that fails validation is skipped with a recorded reason rather than
half-applied. `GET /api/v1/store` returns those reasons under `rejected`, so a
contributor can see why their app is missing instead of guessing.

## Repository layout

```
apps/
├── jellyfin/
│   ├── homeos-app.yml
│   ├── icon.svg
│   └── screenshots/*.png
└── nextcloud/
    └── homeos-app.yml
```

The directory name must equal the manifest `id`.

## Complete example

```yaml
manifestVersion: 1              # required; a newer version than the build
                                # understands is rejected, not half-parsed
id: nextcloud                   # 2-32 chars, [a-z0-9-]; matches the directory
name: Nextcloud
tagline: Your files, calendar and contacts, self-hosted
description: |
  Longer prose for the app detail page.
category: productivity          # see the list below
version: "29.0.4"
icon: nextcloud.svg             # resolved inside the app's own directory
website: https://nextcloud.com
developer: Nextcloud GmbH
license: AGPL-3.0

architectures: [amd64, arm64]   # an app absent for this machine's arch is
                                # hidden rather than offered and then failing

image: nextcloud:29.0.4-apache  # explicit tag or @sha256 digest required
port: 80                        # container port the proxy targets
route: host                     # host | path | port; omit to use the default
path: /                         # entry path within the app, optional

env:
  - key: NEXTCLOUD_ADMIN_USER
    label: Admin username       # shown in the install form
    type: string                # string | password | number | bool | select
    default: admin
    required: true
  - key: NEXTCLOUD_ADMIN_PASSWORD
    label: Admin password
    type: password
    required: true
    generate: true              # a fresh random value when left blank
  - key: POSTGRES_PASSWORD
    label: Database password
    type: password
    generate: true
    advanced: true              # hidden behind a disclosure in the form

volumes:
  - name: html                  # persistent, under the app's data directory
    path: /var/www/html
  - hostPath: /mnt/storage/media  # bind mount; confined, see below
    path: /media
    readOnly: true

devices:
  - /dev/dri                    # hardware passthrough, e.g. GPU transcoding

resources:
  memory: 2g
  cpus: "2.0"

healthcheck:
  test: ["CMD-SHELL", "curl -fsS http://localhost:80/status.php || exit 1"]
  interval: 30s
  timeout: 5s
  retries: 3

dependencies: []                # other app ids that must be installed first

sidecars:
  db:
    image: postgres:16.4-alpine
    useEnv: [POSTGRES_PASSWORD] # app-level keys this sidecar also receives
    env:
      - key: POSTGRES_DB
        label: Database name
        type: string
        default: nextcloud
    volumes:
      - name: pgdata
        path: /var/lib/postgresql/data
```

## Field rules that are enforced

| Rule | Why |
|---|---|
| `image` needs a tag or digest | `:latest` is not reproducible — the same manifest would install different software on different days, and an update would be indistinguishable from a rollback |
| `hostPath` must be under `/mnt/storage` or `/var/lib/homeos/data` | otherwise any catalogue entry could mount `/` and read the whole machine |
| `devices` must be normalised paths under `/dev` | passthrough hands real hardware to a container |
| env keys are unique across the app **and** its sidecars | the install form asks for each key once, so the same key in two services is ambiguous to the person filling it in — hence `useEnv` to share one |
| mount paths are unique **per container** | the app and a sidecar may both legitimately mount `/data` |
| `generate` only applies to `type: password` | generating a random timezone is meaningless |
| unknown keys are an error | a typo silently becoming a default is worse than a rejected manifest |

Categories: `media`, `productivity`, `networking`, `developer`, `automation`,
`storage`, `security`, `games`, `monitoring`, `communication`, `other`.

## What the backend generates from it

One Compose stack per app, with the isolation model applied:

```yaml
name: homeos-nextcloud
services:
  nextcloud:
    networks: [edge, private]     # reachable by the proxy
    labels:
      homeos.enable: "true"       # <- phase 1's homeos-proxy-sync reads these
      homeos.app: nextcloud
      homeos.port: "80"
      homeos.route: host
    security_opt: [no-new-privileges:true]
  db:
    networks: [private]           # never routable
    labels:
      homeos.role: sidecar        # no homeos.enable, so the proxy ignores it
networks:
  edge:    {external: true, name: homeos-edge}
  private: {driver: bridge, internal: true, name: homeos-app-nextcloud}
```

No `ports:` mapping is emitted. The reverse proxy is the only ingress, which is
what keeps the app reachable at `http://nextcloud.local/` without opening a host
port.

`installed.json` is written beside the stack at mode `0600`, recording the
manifest and the resolved environment. It holds generated passwords, and it is
what lets an update diff against what is actually installed and an uninstall
work even after the app has left the catalogue.
