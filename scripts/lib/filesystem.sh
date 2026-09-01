#!/usr/bin/env bash
# HomeOS — service account and on-disk layout.
# shellcheck shell=bash

[[ -n "${_HOMEOS_FILESYSTEM_SH:-}" ]] && return 0
_HOMEOS_FILESYSTEM_SH=1

HOMEOS_USER="homeos"
HOMEOS_GROUP="homeos"
HOMEOS_UID_HINT=970

# Members of this group get read/write access to the NAS shares without being
# able to administer HomeOS itself.
HOMEOS_SHARE_GROUP="homeos-share"

fs::create_account() {
    log::step "Service account"

    if getent group "$HOMEOS_GROUP" >/dev/null; then
        log::skip "group $HOMEOS_GROUP exists"
    else
        run groupadd --system "$HOMEOS_GROUP"
        log::ok "created group $HOMEOS_GROUP"
    fi

    if getent group "$HOMEOS_SHARE_GROUP" >/dev/null; then
        log::skip "group $HOMEOS_SHARE_GROUP exists"
    else
        run groupadd --system "$HOMEOS_SHARE_GROUP"
        log::ok "created group $HOMEOS_SHARE_GROUP"
    fi

    if id "$HOMEOS_USER" >/dev/null 2>&1; then
        log::skip "user $HOMEOS_USER exists"
    else
        run useradd --system \
            --gid "$HOMEOS_GROUP" \
            --home-dir /var/lib/homeos \
            --no-create-home \
            --shell /usr/sbin/nologin \
            --comment "HomeOS core service account" \
            "$HOMEOS_USER" \
            || die "could not create the $HOMEOS_USER account"
        log::ok "created user $HOMEOS_USER"
    fi

    run usermod -aG "$HOMEOS_SHARE_GROUP" "$HOMEOS_USER" 2>/dev/null || true
}

# The layout separates four lifetimes, which is what makes the phase-4 OTA
# update safe: /usr/lib and /opt are replaced wholesale on update, /etc is
# merged, /var/lib and /mnt are never touched.
fs::create_tree() {
    log::step "Directory layout"

    # --- Configuration: survives updates, backed up, root-owned -------------
    ensure_dir /etc/homeos                    0755 root:root
    ensure_dir /etc/homeos/apps               0755 root:root
    ensure_dir /etc/homeos/proxy              0755 root:root
    ensure_dir /etc/homeos/proxy/routes.d     0755 root:root
    ensure_dir /etc/homeos/samba              0755 root:root
    ensure_dir /etc/homeos/storage            0755 root:root
    # Secrets: API tokens, Samba machine account, app credentials.
    ensure_dir /etc/homeos/secrets            0700 "${HOMEOS_USER}:${HOMEOS_GROUP}"

    # --- Program code: replaced wholesale by an OTA update ------------------
    ensure_dir /usr/lib/homeos                0755 root:root
    ensure_dir /usr/lib/homeos/releases       0755 root:root
    # bin and scripts are deliberately NOT created here: stage::runtime_files
    # makes them symlinks into the current release, which is what lets an
    # over-the-air update swap everything by moving one link.
    ensure_dir /opt/homeos                    0755 root:root
    ensure_dir /opt/homeos/web                0755 root:root

    # --- Mutable state: never touched by an update --------------------------
    ensure_dir /var/lib/homeos                0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"
    ensure_dir /var/lib/homeos/apps           0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"
    ensure_dir /var/lib/homeos/data           0770 "${HOMEOS_USER}:${HOMEOS_SHARE_GROUP}"
    ensure_dir /var/lib/homeos/store          0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"
    ensure_dir /var/lib/homeos/db             0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"
    ensure_dir /var/lib/homeos/backups        0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"
    ensure_dir /var/lib/homeos/updates        0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"
    ensure_dir /var/lib/homeos/tmp            0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"

    # --- Logs ---------------------------------------------------------------
    ensure_dir /var/log/homeos                0750 "${HOMEOS_USER}:${HOMEOS_GROUP}"

    # --- NAS mount root -----------------------------------------------------
    # 0755 so Samba and the app containers can traverse into individual shares
    # whose own permissions do the real gating.
    ensure_dir /mnt/storage                   0755 root:root
}

# Runtime configuration read by homeos-core (phase 2) and by the shell helpers.
# Written once; a re-run preserves operator edits except for derived values.
fs::write_config() {
    log::step "Base configuration"

    local cfg=/etc/homeos/config.yaml
    if [[ -f "$cfg" ]]; then
        log::skip "$cfg exists - leaving operator edits intact"
    else
        write_file "$cfg" 0644 root:root <<YAML
# HomeOS core configuration.
# Values here are read at homeos-core start-up; edit and restart the service:
#   systemctl restart homeos-core
version: 1

system:
  hostname: ${HOMEOS_HOSTNAME}
  domain: local
  timezone: ${HOMEOS_TZ}

api:
  # Bound to loopback on purpose: all external access is terminated by the
  # reverse proxy, which owns TLS, auth and access logging.
  listen: 127.0.0.1
  port: 8790
  # Blank on a fresh install; the first-run wizard writes the admin account.
  session_ttl_hours: 168

paths:
  config: /etc/homeos
  apps: /var/lib/homeos/apps
  data: /var/lib/homeos/data
  store: /var/lib/homeos/store
  database: /var/lib/homeos/db/homeos.db
  backups: /var/lib/homeos/backups
  logs: /var/log/homeos
  storage_root: /mnt/storage
  web_root: /opt/homeos/web

docker:
  socket: /var/run/docker.sock
  edge_network: ${HOMEOS_EDGE_NET}
  app_subnet_pool: ${HOMEOS_APP_POOL_BASE}
  app_subnet_size: ${HOMEOS_APP_POOL_SIZE}
  compose_project_prefix: homeos

proxy:
  engine: caddy
  admin_endpoint: http://127.0.0.1:2019
  routes_dir: /etc/homeos/proxy/routes.d
  # host   -> http://<app>.local            (mDNS alias published automatically)
  # path   -> http://${HOMEOS_HOSTNAME}.local/app/<app>/
  # port   -> http://${HOMEOS_HOSTNAME}.local:<port>/
  default_route_mode: host
  publish_mdns_aliases: true

storage:
  mount_root: /mnt/storage
  default_filesystem: ext4
  smart_poll_interval_minutes: 30

samba:
  workgroup: WORKGROUP
  managed_config: /etc/homeos/samba/shares.conf
  share_group: ${HOMEOS_SHARE_GROUP}

appstore:
  # Replace with your own catalogue fork; phase 2 clones and parses it.
  repository: https://github.com/homeos-apps/appstore.git
  branch: main
  refresh_interval_hours: 12

telemetry:
  sample_interval_seconds: 2
  history_retention_minutes: 60

update:
  # Clear channel_url to turn over-the-air updates off entirely.
  channel_url: https://updates.homeos.dev/stable.json
  # auto_check downloads and stages; it never restarts the appliance.
  auto_check: true
  check_interval_hours: 24
  # auto_apply restarts the box unattended when an update lands. Off by
  # default: a NAS mid-transfer is a bad moment to be surprised by a reboot.
  auto_apply: false
  releases_dir: /usr/lib/homeos/releases
  public_key_file: /etc/homeos/update.pub
  keep_releases: 3
YAML
    fi

    # Derived, machine-owned values. Regenerated on every run so they always
    # reflect reality; nothing here is meant to be hand-edited.
    write_file /etc/homeos/homeos.env 0644 root:root <<ENV
# Generated by install.sh - do not edit; edit /etc/homeos/config.yaml instead.
HOMEOS_VERSION=${HOMEOS_VERSION}
HOMEOS_ARCH=${HOMEOS_ARCH}
HOMEOS_HOSTNAME=${HOMEOS_HOSTNAME}
HOMEOS_CONFIG=/etc/homeos/config.yaml
HOMEOS_DATA_DIR=/var/lib/homeos
HOMEOS_WEB_ROOT=/opt/homeos/web
HOMEOS_API_ADDR=127.0.0.1:8790
HOMEOS_EDGE_NET=${HOMEOS_EDGE_NET}
HOMEOS_LOG_LEVEL=info
ENV

    # API token used by the shell helpers to call homeos-core locally.
    local tokfile=/etc/homeos/secrets/api.token
    if [[ -s "$tokfile" ]]; then
        log::skip "API token exists"
    elif [[ -z "${HOMEOS_DRY_RUN:-}" ]]; then
        ( umask 077; head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$tokfile" )
        run chown "${HOMEOS_USER}:${HOMEOS_GROUP}" "$tokfile"
        run chmod 0600 "$tokfile"
        log::ok "generated API token"
    fi
}

# Keep the install log and future app logs from growing without bound.
fs::write_logrotate() {
    write_file /etc/logrotate.d/homeos 0644 root:root <<'CONF'
/var/log/homeos/*.log {
    weekly
    rotate 8
    missingok
    notifempty
    compress
    delaycompress
    copytruncate
    su homeos homeos
}
CONF
}
