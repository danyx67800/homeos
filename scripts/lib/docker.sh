#!/usr/bin/env bash
# HomeOS — Docker Engine, daemon tuning and the HomeOS network fabric.
# shellcheck shell=bash

[[ -n "${_HOMEOS_DOCKER_SH:-}" ]] && return 0
_HOMEOS_DOCKER_SH=1

# The edge network is the only one the reverse proxy joins. Every app also gets
# a private per-app bridge, so apps can reach their own sidecars (database,
# redis) but never each other. See docs/phase1-architecture.md.
HOMEOS_EDGE_NET="homeos-edge"
HOMEOS_EDGE_SUBNET="10.20.0.0/24"

# Pool the per-app bridges are carved from by the phase-2 orchestrator.
HOMEOS_APP_POOL_BASE="10.21.0.0/16"
HOMEOS_APP_POOL_SIZE=24

docker::purge_distro_packages() {
    # Debian/Ubuntu ship docker.io / podman-docker variants that conflict with
    # the upstream docker-ce packages. Remove only if the upstream one is absent.
    have docker && docker version --format '{{.Server.Version}}' >/dev/null 2>&1 && return 0
    local p
    for p in docker.io docker-doc docker-compose podman-docker containerd runc; do
        if dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q "^install ok installed$"; then
            log::warn "removing conflicting distro package: $p"
            run_quiet apt-get remove -y -qq "$p" || log::warn "could not remove $p"
        fi
    done
}

docker::add_repo() {
    local origin="$HOMEOS_OS_ID"
    # Raspberry Pi OS and other Debian derivatives track the Debian channel.
    [[ "$origin" == "raspbian" ]] && origin="debian"
    [[ "$origin" == "debian" || "$origin" == "ubuntu" ]] || origin="debian"

    apt::add_repo "docker" \
        "https://download.docker.com/linux/${origin}/gpg" \
        "deb [arch=\${ARCH} signed-by=\${KEYRING}] https://download.docker.com/linux/${origin} \${CODENAME} stable"
}

docker::install() {
    log::step "Docker Engine + Compose V2"

    if have docker && docker compose version >/dev/null 2>&1; then
        log::skip "docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo '?') with compose v2 present"
    else
        docker::purge_distro_packages
        docker::add_repo
        apt::install docker-ce docker-ce-cli containerd.io \
                     docker-buildx-plugin docker-compose-plugin
    fi

    docker::configure_daemon

    # A chroot has no running daemon to talk to. The packages and the
    # configuration are what the image needs; the runtime checks belong on the
    # machine that eventually boots it.
    if [[ -n "${HOMEOS_IMAGE_BUILD:-}" ]]; then
        run systemctl enable docker.service >/dev/null 2>&1 || true
        log::skip "not starting Docker (building an image)"
        log::ok "docker $(docker --version 2>/dev/null | awk '{print $3}' | tr -d ,)"
        return 0
    fi

    svc_enable_now docker.service || die "Docker failed to start"

    # The socket can take a moment after the unit reports active.
    local i
    for i in 1 2 3 4 5 6 7 8 9 10; do
        docker info >/dev/null 2>&1 && break
        sleep 1
    done
    docker info >/dev/null 2>&1 || die "Docker daemon is not responding on its socket"

    log::ok "docker $(docker version --format '{{.Server.Version}}')"
    log::ok "compose $(docker compose version --short)"
}

docker::configure_daemon() {
    ensure_dir /etc/docker 0755 root:root

    # Log rotation matters most here: an unbounded json-file driver is the
    # single most common way a self-hosting box fills its root filesystem.
    write_file /etc/docker/daemon.json 0644 root:root <<'JSON'
{
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  },
  "storage-driver": "overlay2",
  "live-restore": true,
  "userland-proxy": false,
  "no-new-privileges": false,
  "default-address-pools": [
    { "base": "10.21.0.0/16", "size": 24 },
    { "base": "10.22.0.0/16", "size": 24 }
  ],
  "features": { "buildkit": true },
  "metrics-addr": "127.0.0.1:9323"
}
JSON

    if [[ "$HOMEOS_FILE_CHANGED" == yes ]] && systemctl is-active --quiet docker 2>/dev/null; then
        log::info "daemon.json changed - restarting docker"
        run systemctl restart docker || log::warn "docker restart failed"
    fi
}

# The proxy needs to reach app containers by name. Attaching every published app
# to one shared edge bridge is what makes hostname-based routing work without
# publishing host ports.
docker::create_networks() {
    log::step "Container network fabric"

    # Creating a network needs a live daemon. homeos-firstboot does it on the
    # first real boot instead, which is also when the address pool in
    # daemon.json actually takes effect.
    if [[ -n "${HOMEOS_IMAGE_BUILD:-}" ]]; then
        log::skip "deferring the $HOMEOS_EDGE_NET network to first boot"
        return 0
    fi

    if docker network inspect "$HOMEOS_EDGE_NET" >/dev/null 2>&1; then
        log::skip "network $HOMEOS_EDGE_NET exists"
    else
        run docker network create \
            --driver bridge \
            --subnet "$HOMEOS_EDGE_SUBNET" \
            --opt "com.docker.network.bridge.name=homeos0" \
            --label "homeos.managed=true" \
            --label "homeos.role=edge" \
            "$HOMEOS_EDGE_NET" >/dev/null \
            || die "could not create the $HOMEOS_EDGE_NET network"
        log::ok "created network $HOMEOS_EDGE_NET ($HOMEOS_EDGE_SUBNET)"
    fi
}

# Grant the service account Docker API access. This is root-equivalent on the
# host; the trade-off is documented in docs/phase1-architecture.md and is why
# homeos-core is otherwise sandboxed hard in its unit file.
docker::grant_service_account() {
    local user="$1"
    if id -nG "$user" 2>/dev/null | tr ' ' '\n' | grep -qx docker; then
        log::skip "$user already in the docker group"
    else
        run usermod -aG docker "$user"
        log::ok "added $user to the docker group"
        log::warn "docker group membership is root-equivalent - homeos-core is
     sandboxed via systemd to compensate"
    fi
}
