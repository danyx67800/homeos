#!/usr/bin/env bash
# HomeOS — preflight validation.
# shellcheck shell=bash disable=SC1091

[[ -n "${_HOMEOS_PREFLIGHT_SH:-}" ]] && return 0
_HOMEOS_PREFLIGHT_SH=1

# Populated by preflight::run, consumed by the later install stages.
HOMEOS_ARCH=""          # amd64 | arm64
HOMEOS_OS_ID=""         # debian | ubuntu | raspbian
HOMEOS_OS_VERSION=""    # 12 | 24.04 | ...
HOMEOS_OS_CODENAME=""   # bookworm | noble | ...
HOMEOS_KERNEL=""        # 6.1.0-18-amd64
HOMEOS_VIRT=""          # none | kvm | lxc | ...

HOMEOS_MIN_KERNEL_MAJOR=6
HOMEOS_MIN_DISK_MB=6144
HOMEOS_MIN_RAM_MB=1024

preflight::root() {
    [[ "${EUID:-$(id -u)}" -eq 0 ]] \
        || die "install.sh must run as root (try: sudo ./install.sh)"
    log::ok "running as root"
}

preflight::arch() {
    local m; m="$(uname -m)"
    case "$m" in
        x86_64|amd64)  HOMEOS_ARCH="amd64" ;;
        aarch64|arm64) HOMEOS_ARCH="arm64" ;;
        armv7l|armhf)
            die "32-bit ARM ($m) is not supported: the HomeOS app catalogue publishes
     amd64/arm64 images only. Reinstall a 64-bit OS." ;;
        *) die "unsupported architecture: $m (need x86_64 or aarch64)" ;;
    esac
    log::ok "architecture $m -> $HOMEOS_ARCH"
}

preflight::kernel() {
    HOMEOS_KERNEL="$(uname -r)"

    # uname reports the host's kernel inside a chroot, not the one being
    # installed into the image, so the check would be measuring the wrong thing.
    if [[ -n "${HOMEOS_IMAGE_BUILD:-}" ]]; then
        log::skip "kernel check (the image ships its own; the host runs ${HOMEOS_KERNEL})"
        return 0
    fi

    local major minor
    major="${HOMEOS_KERNEL%%.*}"
    minor="${HOMEOS_KERNEL#*.}"; minor="${minor%%.*}"
    [[ "$major" =~ ^[0-9]+$ ]] || die "cannot parse kernel version: $HOMEOS_KERNEL"
    [[ "$minor" =~ ^[0-9]+$ ]] || minor=0

    if (( major >= HOMEOS_MIN_KERNEL_MAJOR )); then
        log::ok "kernel $HOMEOS_KERNEL"
        return 0
    fi

    # 5.15 LTS (Ubuntu 22.04) runs Docker, overlay2 and cgroup v2 correctly, so
    # it warns and continues; anything older is refused.
    if (( major == 5 && minor >= 15 )); then
        log::warn "kernel $HOMEOS_KERNEL is below the recommended ${HOMEOS_MIN_KERNEL_MAJOR}.x"
        log::warn "supported, but btrfs and some SMART paths are less capable"
        return 0
    fi

    if [[ -n "${HOMEOS_FORCE:-}" ]]; then
        log::warn "kernel $HOMEOS_KERNEL unsupported - continuing due to --force"
        return 0
    fi
    die "kernel $HOMEOS_KERNEL is too old (want >= ${HOMEOS_MIN_KERNEL_MAJOR}.x; >= 5.15 tolerated).
     Upgrade the kernel, or re-run with --force to override."
}

preflight::os() {
    [[ -r /etc/os-release ]] || die "/etc/os-release missing - unsupported distribution"
    # shellcheck disable=SC1091
    . /etc/os-release
    HOMEOS_OS_ID="${ID:-unknown}"
    HOMEOS_OS_VERSION="${VERSION_ID:-unknown}"
    HOMEOS_OS_CODENAME="${VERSION_CODENAME:-${UBUNTU_CODENAME:-}}"

    case "$HOMEOS_OS_ID" in
        debian)
            [[ "${HOMEOS_OS_VERSION%%.*}" -ge 12 ]] 2>/dev/null \
                || log::warn "Debian ${HOMEOS_OS_VERSION} predates the supported 12 (bookworm)" ;;
        ubuntu)
            [[ "${HOMEOS_OS_VERSION%%.*}" -ge 22 ]] 2>/dev/null \
                || log::warn "Ubuntu ${HOMEOS_OS_VERSION} predates the supported 22.04" ;;
        raspbian)
            log::warn "Raspberry Pi OS detected - Docker repo will use the Debian channel" ;;
        *)
            if [[ -n "${HOMEOS_FORCE:-}" ]]; then
                log::warn "untested distribution '$HOMEOS_OS_ID' - continuing due to --force"
            else
                die "unsupported distribution '$HOMEOS_OS_ID' (need Debian 12+ / Ubuntu 22.04+).
     Re-run with --force to attempt anyway."
            fi ;;
    esac

    [[ -n "$HOMEOS_OS_CODENAME" ]] \
        || die "cannot determine the APT codename - required for the Docker repository"
    have apt-get || die "apt-get not found - this installer targets Debian/Ubuntu only"
    log::ok "distribution ${HOMEOS_OS_ID} ${HOMEOS_OS_VERSION} (${HOMEOS_OS_CODENAME})"
}

preflight::systemd() {
    # Building an image needs systemd *installed*, so units can be enabled into
    # the tree. It cannot be *running*: nothing runs in a chroot. Demanding the
    # running state here is what stopped image builds.
    if [[ -n "${HOMEOS_IMAGE_BUILD:-}" ]]; then
        have systemctl || die "systemd is not installed in this root filesystem"
        log::ok "systemd $(systemctl --version | head -n1 | awk '{print $2}') (installed, not running)"
        return 0
    fi

    systemd_available \
        || die "systemd is not the active init - HomeOS services cannot be registered"
    log::ok "systemd $(systemctl --version | head -n1 | awk '{print $2}')"
}

preflight::cgroups() {
    if [[ -f /sys/fs/cgroup/cgroup.controllers ]]; then
        log::ok "cgroup v2 unified hierarchy"
    else
        log::warn "cgroup v1 - container resource accounting will be less accurate"
    fi
}

preflight::resources() {
    if [[ -n "${HOMEOS_IMAGE_BUILD:-}" ]]; then
        log::skip "disk and memory check (these belong to the build host)"
        return 0
    fi

    local disk_mb ram_mb
    disk_mb="$(df -Pm /var 2>/dev/null | awk 'NR == 2 {print $4}')"
    ram_mb="$(awk '/MemTotal/ {printf "%d", $2 / 1024}' /proc/meminfo)"

    if [[ -n "$disk_mb" ]] && (( disk_mb < HOMEOS_MIN_DISK_MB )); then
        if [[ -n "${HOMEOS_FORCE:-}" ]]; then
            log::warn "only ${disk_mb}MB free on /var (want >= ${HOMEOS_MIN_DISK_MB}MB)"
        else
            die "only ${disk_mb}MB free on /var; HomeOS needs >= ${HOMEOS_MIN_DISK_MB}MB for
     Docker images and app data. Free space, or re-run with --force."
        fi
    else
        log::ok "disk: ${disk_mb:-?}MB free on /var"
    fi

    if (( ram_mb < HOMEOS_MIN_RAM_MB )); then
        log::warn "only ${ram_mb}MB RAM - expect trouble beyond one or two apps"
    else
        log::ok "memory: ${ram_mb}MB"
    fi
}

preflight::network() {
    local host
    for host in deb.debian.org archive.ubuntu.com download.docker.com; do
        if getent hosts "$host" >/dev/null 2>&1; then
            log::ok "DNS resolves $host"
            return 0
        fi
    done
    die "no DNS resolution for the package mirrors - HomeOS needs internet access"
}

preflight::virt() {
    have systemd-detect-virt || return 0
    HOMEOS_VIRT="$(systemd-detect-virt 2>/dev/null || echo none)"
    [[ "$HOMEOS_VIRT" == "none" ]] || log::info "virtualised environment: $HOMEOS_VIRT"
    case "$HOMEOS_VIRT" in
        lxc|lxc-libvirt|openvz|docker|podman)
            log::warn "container-based virtualisation: SMART, udev disk events and Samba
     kernel features will be unavailable or degraded" ;;
    esac
}

# Refuse to co-install beside appliances that claim the same ports, the same
# Docker socket conventions and the same /etc namespace.
preflight::conflicts() {
    local found=()
    [[ -d /etc/casaos ]]               && found+=("CasaOS (/etc/casaos)")
    [[ -d /home/umbrel/umbrel ]]       && found+=("Umbrel (/home/umbrel/umbrel)")
    [[ -d /etc/openmediavault ]]       && found+=("OpenMediaVault (/etc/openmediavault)")
    [[ -d /usr/share/cockpit/storaged ]] && found+=("Cockpit storage plugin")

    if (( ${#found[@]} > 0 )); then
        log::warn "conflicting appliance stack detected: ${found[*]}"
        [[ -n "${HOMEOS_FORCE:-}" ]] \
            || die "refusing to install over an existing appliance stack.
     Remove it first, or re-run with --force if the ports really do not collide."
    fi

    # In a chroot the listening sockets belong to the build host, not to the
    # image being built, so checking them says nothing useful.
    if [[ -n "${HOMEOS_IMAGE_BUILD:-}" ]]; then
        log::skip "port conflict check (building an image)"
        return 0
    fi

    local p owner
    for p in 80 443; do
        port_in_use "$p" || continue
        # ss -p renders the holder as users:(("caddy",pid=...)); field 2 on a
        # double-quote split is the process name.
        owner="$(ss -lntpH "sport = :${p}" 2>/dev/null | awk -F'"' 'NF > 1 {print $2; exit}')"
        case "$owner" in
            caddy|docker-proxy)
                log::info "port ${p} already held by ${owner} (assumed ours)" ;;
            *)
                log::warn "port ${p} is in use by ${owner:-an unknown process}"
                [[ -n "${HOMEOS_FORCE:-}" ]] \
                    || die "the HomeOS reverse proxy needs ports 80/443.
     Stop the conflicting service, or re-run with --force." ;;
        esac
    done
    log::ok "no blocking conflicts"
}

preflight::run() {
    log::step "Preflight checks"
    preflight::root
    preflight::arch
    preflight::kernel
    preflight::os
    preflight::systemd
    preflight::cgroups
    preflight::resources
    preflight::network
    preflight::virt
    preflight::conflicts
}
