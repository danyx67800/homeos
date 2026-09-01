#!/usr/bin/env bash
# HomeOS — Caddy reverse proxy.
#
# Caddy runs on the host (not in a container) so it can own ports 80/443 and be
# supervised by systemd like every other HomeOS service. The consequence is that
# Docker's embedded DNS is not available to it: container *names* do not resolve
# from the host. homeos-proxy-sync therefore resolves each backend to a concrete
# address and regenerates the route files on every Docker event.
# shellcheck shell=bash

[[ -n "${_HOMEOS_PROXY_SH:-}" ]] && return 0
_HOMEOS_PROXY_SH=1

HOMEOS_PROXY_DIR=/etc/homeos/proxy
HOMEOS_PROXY_ROUTES="${HOMEOS_PROXY_DIR}/routes.d"
HOMEOS_CADDYFILE="${HOMEOS_PROXY_DIR}/Caddyfile"

proxy::install() {
    log::step "Reverse proxy (Caddy)"

    if have caddy; then
        log::skip "caddy $(caddy version 2>/dev/null | awk '{print $1}') present"
    else
        apt::add_repo "caddy" \
            "https://dl.cloudsmith.io/public/caddy/stable/gpg.key" \
            "deb [signed-by=\${KEYRING}] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main"
        apt::install caddy
    fi

    proxy::write_base_config
    proxy::write_placeholders
    proxy::write_unit_override
    proxy::grant_access

    if proxy::validate; then
        svc_enable_now caddy.service || die "Caddy failed to start"
    else
        die "the generated Caddyfile is invalid - Caddy was not started"
    fi
}

# Caddy's own packaged user needs to read our config tree.
proxy::grant_access() {
    getent passwd caddy >/dev/null || return 0
    run usermod -aG "$HOMEOS_GROUP" caddy 2>/dev/null || true
    run chmod 0755 "$HOMEOS_PROXY_DIR" "$HOMEOS_PROXY_ROUTES"
    ensure_dir /var/log/caddy 0750 caddy:caddy
}

# Point the packaged unit at our Caddyfile instead of /etc/caddy/Caddyfile, so
# an apt upgrade of the caddy package never clobbers HomeOS routing.
proxy::write_unit_override() {
    ensure_dir /etc/systemd/system/caddy.service.d 0755 root:root
    write_file /etc/systemd/system/caddy.service.d/10-homeos.conf 0644 root:root <<CONF
# Managed by HomeOS.
[Service]
ExecStart=
ExecStart=/usr/bin/caddy run --environ --config ${HOMEOS_CADDYFILE}
ExecReload=
ExecReload=/usr/bin/caddy reload --config ${HOMEOS_CADDYFILE} --force
ReadWritePaths=/var/log/caddy
CONF
    svc_reload_units
}

proxy::validate() {
    have caddy || return 0
    [[ -n "${HOMEOS_DRY_RUN:-}" ]] && return 0
    run_quiet caddy validate --config "$HOMEOS_CADDYFILE"
}

# Caddy errors out on an import glob that matches nothing, so both globs always
# have at least one (comment-only, no-op) file behind them.
proxy::write_placeholders() {
    write_file "${HOMEOS_PROXY_ROUTES}/00-placeholder.site.caddy" 0644 root:root <<'CONF'
# Placeholder so `import *.site.caddy` always matches at least one file.
# Host- and port-mode app routes are written here by homeos-proxy-sync.
CONF
    write_file "${HOMEOS_PROXY_ROUTES}/00-placeholder.path.caddy" 0644 root:root <<'CONF'
# Placeholder so `import *.path.caddy` always matches at least one file.
# Path-mode app routes are written here by homeos-proxy-sync.
CONF
}

proxy::write_base_config() {
    ensure_dir "$HOMEOS_PROXY_DIR" 0755 root:root
    ensure_dir "$HOMEOS_PROXY_ROUTES" 0755 root:root

    write_file "$HOMEOS_CADDYFILE" 0644 root:root <<CADDY
#
# HomeOS base Caddyfile - managed by install.sh.
#
# Per-app routes are NOT written here. homeos-proxy-sync generates one file per
# published container under routes.d/ and reloads Caddy; both globs below are
# imported, so nothing in this file changes when apps come and go.
#
{
	admin 127.0.0.1:2019
	# .local names cannot be validated by a public CA, so ACME stays off. To add
	# LAN TLS, add 'tls internal' to a site block and trust Caddy's local root CA.
	# Minimum Caddy version: 2.7 (private_ranges placeholder).
	auto_https off
	persist_config off

	log default {
		output file /var/log/caddy/homeos.log {
			roll_size 10MiB
			roll_keep 5
		}
		format console
		level INFO
	}

	servers {
		trusted_proxies static private_ranges
	}
}

# --------------------------------------------------------------------------
# Shared snippets, used by generated route files too.
# --------------------------------------------------------------------------
(homeos_headers) {
	header {
		-Server
		X-Content-Type-Options nosniff
		X-Frame-Options SAMEORIGIN
		Referrer-Policy strict-origin-when-cross-origin
	}
}

# Refuse anything that did not come from RFC1918 space or loopback. Remove this
# import from the site blocks below if you front HomeOS with your own VPN or
# tunnel that presents public source addresses.
(homeos_lan_only) {
	@homeos_external not remote_ip private_ranges
	handle @homeos_external {
		respond "HomeOS is reachable from the local network only." 403
	}
}

# Applied to every proxied app: preserves the client address for the upstream
# and disables buffering so SSE and websockets stream rather than stall.
(homeos_upstream) {
	header_up X-Real-IP {remote_host}
	header_up X-Forwarded-Host {host}
	flush_interval -1
}

# --------------------------------------------------------------------------
# Host-mode and port-mode app routes (top-level site blocks).
# --------------------------------------------------------------------------
import ${HOMEOS_PROXY_ROUTES}/*.site.caddy

# --------------------------------------------------------------------------
# Dashboard + API. Answers on every name that resolves to this host, so
# http://${HOMEOS_HOSTNAME}.local, http://${HOMEOS_HOSTNAME} and the raw LAN IP
# all work without the installer having to know the address in advance.
# --------------------------------------------------------------------------
:80 {
	import homeos_headers
	import homeos_lan_only

	encode zstd gzip

	# REST API.
	handle /api/* {
		reverse_proxy 127.0.0.1:8790 {
			import homeos_upstream
		}
	}

	# Telemetry: websocket upgrade and SSE fallback.
	handle /ws/* {
		reverse_proxy 127.0.0.1:8790 {
			import homeos_upstream
		}
	}
	handle /events* {
		reverse_proxy 127.0.0.1:8790 {
			import homeos_upstream
		}
	}

	# Path-mode app routes must precede the SPA catch-all below.
	import ${HOMEOS_PROXY_ROUTES}/*.path.caddy

	# Single-page dashboard: unknown paths fall back to index.html so
	# client-side routing survives a hard refresh.
	handle {
		root * /opt/homeos/web
		try_files {path} /index.html
		file_server
	}

	handle_errors {
		respond "HomeOS: {http.error.status_code} {http.error.status_text}"
	}
}
CADDY
}
