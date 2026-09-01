#!/usr/bin/env bash
#
# HomeOS installer — turns a minimal Debian 12 / Ubuntu 22.04+ system into a
# headless self-hosting and NAS appliance.
#
#   sudo ./install.sh
#   sudo ./install.sh --hostname mynas --route-mode path --yes
#   sudo ./install.sh --dry-run --debug        # show every action, change nothing
#   sudo ./install.sh --uninstall              # remove HomeOS, keep user data
#
# The script is idempotent: re-running it converges the system rather than
# rebuilding it, and reports what it skipped.
#
set -euo pipefail

HOMEOS_VERSION="1.0.0-phase1"

# Debian puts the storage and Samba tooling in sbin, which is not on the PATH of
# every sudo configuration.
export PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:${PATH}"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
LIB_DIR="${HOMEOS_LIB_DIR:-${SCRIPT_DIR}/scripts/lib}"

for _lib in common preflight packages docker filesystem network samba proxy; do
    if [[ -r "${LIB_DIR}/${_lib}.sh" ]]; then
        # shellcheck source=/dev/null
        . "${LIB_DIR}/${_lib}.sh"
    else
        echo "install.sh: missing library ${LIB_DIR}/${_lib}.sh" >&2
        echo "Run the installer from a complete checkout of the repository." >&2
        exit 1
    fi
done

# --------------------------------------------------------------------------
# Defaults, overridable from the command line
# --------------------------------------------------------------------------
HOMEOS_HOSTNAME="homenas"
HOMEOS_TZ=""
HOMEOS_ROUTE_MODE="host"
HOMEOS_SKIP_DOCKER=""
HOMEOS_SKIP_SAMBA=""
HOMEOS_UNINSTALL=""
HOMEOS_RECONFIGURE=""
HOMEOS_PRIMARY_IFACE=""

usage() {
    cat <<'USAGE'
HomeOS installer

Usage: sudo ./install.sh [options]

  --hostname NAME     appliance hostname, reachable as NAME.local (default: homenas)
  --timezone TZ       IANA timezone, e.g. Europe/Rome (default: keep current)
  --route-mode MODE   default app routing: host | path | port (default: host)
  --image-build       building a disk image in a chroot: configure everything
                      but start nothing, and defer runtime setup to first boot
  --reconfigure       rewrite the managed configuration from this tree and
                      reload the services that read it. Installs nothing and
                      never touches config.yaml. Run by homeos-apply-update so
                      that a fix to a config file can ship over the air.
  --skip-docker       assume a working Docker Engine is already installed
  --skip-samba        do not install or configure SMB file sharing
  -y, --yes           never prompt
  --force             continue past unsupported kernel/distro/port conflicts
  --dry-run           print what would happen; change nothing
  --debug             verbose tracing
  --uninstall         remove HomeOS services and config, keep /var/lib/homeos
  -h, --help          this text
      --version       print the installer version
USAGE
}

parse_args() {
    while (( $# > 0 )); do
        case "$1" in
            --hostname)    HOMEOS_HOSTNAME="${2:?--hostname needs a value}"; shift 2 ;;
            --hostname=*)  HOMEOS_HOSTNAME="${1#*=}"; shift ;;
            --timezone)    HOMEOS_TZ="${2:?--timezone needs a value}"; shift 2 ;;
            --timezone=*)  HOMEOS_TZ="${1#*=}"; shift ;;
            --route-mode)  HOMEOS_ROUTE_MODE="${2:?--route-mode needs a value}"; shift 2 ;;
            --route-mode=*) HOMEOS_ROUTE_MODE="${1#*=}"; shift ;;
            --image-build) HOMEOS_IMAGE_BUILD=1; HOMEOS_ASSUME_YES=1; shift ;;
            --reconfigure) HOMEOS_RECONFIGURE=1; HOMEOS_ASSUME_YES=1; shift ;;
            --skip-docker) HOMEOS_SKIP_DOCKER=1; shift ;;
            --skip-samba)  HOMEOS_SKIP_SAMBA=1; shift ;;
            --uninstall)   HOMEOS_UNINSTALL=1; shift ;;
            -y|--yes)      HOMEOS_ASSUME_YES=1; shift ;;
            --force)       HOMEOS_FORCE=1; shift ;;
            --dry-run)     HOMEOS_DRY_RUN=1; shift ;;
            --debug)       HOMEOS_DEBUG=1; shift ;;
            -h|--help)     usage; exit 0 ;;
            --version)     printf 'HomeOS installer %s\n' "$HOMEOS_VERSION"; exit 0 ;;
            *)             printf 'unknown option: %s\n\n' "$1" >&2; usage >&2; exit 2 ;;
        esac
    done

    HOMEOS_HOSTNAME="$(slugify "$HOMEOS_HOSTNAME")"
    [[ -n "$HOMEOS_HOSTNAME" ]] || die "--hostname must contain at least one alphanumeric character"
    [[ ${#HOMEOS_HOSTNAME} -le 63 ]] || die "--hostname must be 63 characters or fewer"

    case "$HOMEOS_ROUTE_MODE" in
        host|path|port) ;;
        *) die "--route-mode must be one of: host, path, port" ;;
    esac

    export HOMEOS_IMAGE_BUILD="${HOMEOS_IMAGE_BUILD:-}"
    export HOMEOS_ASSUME_YES="${HOMEOS_ASSUME_YES:-}"
    export HOMEOS_FORCE="${HOMEOS_FORCE:-}"
    export HOMEOS_DRY_RUN="${HOMEOS_DRY_RUN:-}"
    export HOMEOS_DEBUG="${HOMEOS_DEBUG:-}"
}

# --------------------------------------------------------------------------
# Install stages
# --------------------------------------------------------------------------

stage::runtime_files() {
    log::step "HomeOS runtime files"

    # Program code lives in a versioned release directory, and `current` is a
    # symlink to it. That is what makes an over-the-air update atomic: applying
    # one is a rename of the symlink, and rolling back is pointing it at the
    # previous release. `bin` and `scripts` are themselves symlinks into
    # `current`, so every path phase 1 documented still resolves.
    local release="/usr/lib/homeos/releases/${HOMEOS_VERSION}"
    ensure_dir /usr/lib/homeos/releases 0755 root:root
    ensure_dir "${release}"         0755 root:root
    ensure_dir "${release}/bin"     0755 root:root
    ensure_dir "${release}/scripts" 0755 root:root
    ensure_dir "${release}/web"     0755 root:root

    local existing=""
    if [[ -L /usr/lib/homeos/current ]]; then
        existing="$(basename "$(readlink -f /usr/lib/homeos/current)")"
    fi
    if [[ -n "$existing" && "$existing" != "$HOMEOS_VERSION" ]]; then
        log::warn "this box currently runs release ${existing}; the installer will"
        log::warn "make ${HOMEOS_VERSION} current, which may be a downgrade"
    fi

    local bin
    for bin in homeos-proxy-sync homeos-disk-event homeos-firstboot \
               homeos-apply-update homeos-update-check; do
        [[ -f "${SCRIPT_DIR}/scripts/${bin}" ]] || die "missing ${SCRIPT_DIR}/scripts/${bin}"
        run install -m 0755 -o root -g root "${SCRIPT_DIR}/scripts/${bin}" "${release}/bin/${bin}"
    done
    log::ok "installed 5 helpers to ${release}/bin"

    # The shell libraries travel with them: the runtime helpers source common.sh.
    local lib
    for lib in "${LIB_DIR}"/*.sh; do
        run install -m 0644 -o root -g root "$lib" "${release}/scripts/$(basename "$lib")"
    done
    log::ok "installed shell libraries to ${release}/scripts"

    # A previously installed binary and dashboard are carried into the new
    # release directory, so re-running the installer does not leave the
    # appliance without a backend until the next `make install`.
    if [[ -n "$existing" && "$existing" != "$HOMEOS_VERSION" ]]; then
        local old="/usr/lib/homeos/releases/${existing}"
        [[ -x "${old}/bin/homeos-core" ]] && \
            run cp -a "${old}/bin/homeos-core" "${release}/bin/homeos-core"
        [[ -f "${old}/web/index.html" ]] && \
            run cp -a "${old}/web/." "${release}/web/"
    fi

    fs::link current  "releases/${HOMEOS_VERSION}" /usr/lib/homeos
    fs::link bin      "current/bin"                /usr/lib/homeos
    fs::link scripts  "current/scripts"            /usr/lib/homeos

    # The dashboard is part of a release too, so Caddy's web root follows the
    # symlink and an update swaps the assets with the binary, never separately.
    if [[ -d /opt/homeos/web && ! -L /opt/homeos/web ]]; then
        if [[ -f /opt/homeos/web/index.html ]]; then
            run cp -a /opt/homeos/web/. "${release}/web/"
            log::info "moved the existing dashboard into ${release}/web"
        fi
        run rm -rf /opt/homeos/web
    fi
    fs::link web "/usr/lib/homeos/current/web" /opt/homeos

    # Convenience symlinks so operators can run the tools by name.
    run ln -sfn /usr/lib/homeos/bin/homeos-proxy-sync /usr/local/bin/homeos-proxy-sync
    run ln -sfn /usr/lib/homeos/bin/homeos-apply-update /usr/local/bin/homeos-apply-update
}

# fs::link <name> <target> <dir> — create dir/name -> target atomically.
#
# ln -sfn writes the link and renames it over any existing one. Without -n, ln
# would follow an existing symlink and create the new one *inside* the directory
# it points at, which is how these layouts usually break.
# fs::link <name> <target> <dir> — make dir/name a symlink to target, atomically.
#
# ln -sfn writes the link and renames it over any existing one. Without -n, ln
# follows an existing symlink and creates the new one *inside* the directory it
# points at, which is how these layouts usually break.
fs::link() {
    local name="$1" target="$2" dir="$3"
    local path="${dir}/${name}"

    # An installation from before the release layout existed has a real
    # directory here. Its contents belong in the release the link will point
    # at, so move them rather than refusing to continue.
    if [[ -d "$path" && ! -L "$path" ]]; then
        local resolved="${dir}/${target}"
        [[ "$target" == /* ]] && resolved="$target"
        log::info "migrating ${path} into ${resolved}"
        run mkdir -p "$resolved"
        if [[ -n "$(ls -A "$path" 2>/dev/null)" ]]; then
            run cp -a "${path}/." "${resolved}/"
        fi
        run rm -rf "$path"
    elif [[ -e "$path" && ! -L "$path" ]]; then
        die "${path} exists and is neither a symlink nor a directory; move it aside and re-run"
    fi

    run ln -sfn "$target" "${path}.homeos-new"
    run mv -Tf "${path}.homeos-new" "$path"
    log::ok "${path} -> ${target}"
}

stage::udev() {
    log::step "Disk hotplug detection (udev)"
    run install -m 0644 -o root -g root \
        "${SCRIPT_DIR}/config/udev/99-homeos-storage.rules" \
        /etc/udev/rules.d/99-homeos-storage.rules
    if [[ -z "${HOMEOS_DRY_RUN:-}" ]]; then
        run_quiet udevadm control --reload-rules || log::warn "udevadm reload failed"
    fi
    log::ok "udev rules installed"
}

# A malformed sudoers file locks the whole machine out of sudo, so it is
# validated in a staging location and only then moved into sudoers.d.
stage::sudoers() {
    log::step "Privilege delegation (sudoers)"
    local src="${SCRIPT_DIR}/config/sudoers/homeos"
    local dst=/etc/sudoers.d/homeos
    local tmp; tmp="$(mktemp)"

    install -m 0440 -o root -g root "$src" "$tmp"

    if have visudo; then
        if ! visudo -cf "$tmp" >/dev/null 2>&1; then
            rm -f "$tmp"
            die "the generated sudoers file failed validation - refusing to install it"
        fi
        log::ok "sudoers syntax validated"
    else
        log::warn "visudo not found - installing the sudoers drop-in unvalidated"
    fi

    if [[ -n "${HOMEOS_DRY_RUN:-}" ]]; then
        rm -f "$tmp"; log::info "dry-run: would install $dst"; return 0
    fi
    install -m 0440 -o root -g root "$tmp" "$dst"
    rm -f "$tmp"
    log::ok "installed $dst"
}

stage::systemd() {
    log::step "systemd services"
    local unit
    for unit in homeos-core homeos-proxy-sync homeos-firstboot \
                homeos-update-apply homeos-update-check; do
        run install -m 0644 -o root -g root \
            "${SCRIPT_DIR}/config/systemd/${unit}.service" \
            "/etc/systemd/system/${unit}.service"
        log::ok "installed ${unit}.service"
    done
    run install -m 0644 -o root -g root \
        "${SCRIPT_DIR}/config/systemd/homeos-update-check.timer" \
        /etc/systemd/system/homeos-update-check.timer

    svc_reload_units

    # homeos-core only starts once phase 2 delivers the binary; enabling it now
    # means the appliance comes up complete the moment that lands.
    run systemctl enable homeos-core.service >/dev/null 2>&1 \
        || log::warn "could not enable homeos-core.service"
    if [[ -x /usr/lib/homeos/bin/homeos-core ]]; then
        svc_enable_now homeos-core.service || log::warn "homeos-core did not start"
    else
        log::info "homeos-core.service is enabled but has no binary yet."
        log::info "Building from source needs Go and Node, which are not installed"
        log::info "here on purpose — an appliance should not carry a compiler:"
        log::info "  sudo scripts/install-build-deps.sh"
        log::info "  make build && sudo make install"
    fi

    svc_enable_now homeos-proxy-sync.service || log::warn "proxy sync did not start"
    run systemctl enable homeos-firstboot.service >/dev/null 2>&1 || true

    # The timer only nudges the daemon to check; applying stays a decision.
    # homeos-update-apply is triggered on demand and is deliberately not enabled.
    if [[ -n "${HOMEOS_ENABLE_AUTO_UPDATE:-}" ]]; then
        svc_enable_now homeos-update-check.timer || log::warn "update timer did not start"
    else
        run systemctl enable homeos-update-check.timer >/dev/null 2>&1 || true
        log::info "update check timer enabled; it starts on the next boot"
    fi
}

stage::timezone() {
    [[ -n "$HOMEOS_TZ" ]] || return 0
    log::step "Timezone"

    # Set it the way timedatectl does underneath — a symlink to the zoneinfo
    # file — rather than through timedatectl itself, which needs a running
    # systemd and a dbus. That is never true inside a chroot, and validating a
    # timezone by asking a daemon that is not there is how the image build
    # failed on a name as ordinary as UTC.
    local zone="/usr/share/zoneinfo/${HOMEOS_TZ}"
    if [[ ! -f "$zone" ]]; then
        die "unknown timezone: ${HOMEOS_TZ}
     No such file ${zone}. Valid names look like Europe/Rome or Etc/UTC;
     list them with: find /usr/share/zoneinfo -type f -printf '%P\n' | sort"
    fi

    run ln -sfn "$zone" /etc/localtime.homeos-new
    run mv -Tf /etc/localtime.homeos-new /etc/localtime
    printf '%s\n' "$HOMEOS_TZ" | write_file /etc/timezone 0644 root:root >/dev/null || true
    log::ok "timezone set to ${HOMEOS_TZ}"
}

# A chroot has no address to advertise and nothing running to report on.
stage::image_summary() {
    cat >&2 <<BANNER

${C_GRN}${C_BOLD}HomeOS ${HOMEOS_VERSION} baked into the image.${C_RESET}

  Configured, enabled, and deliberately not started. On first boot the
  appliance generates its own host identity, grows the root filesystem to
  the disk it was written to, and creates the container network.

BANNER
}

# A chroot has no address to advertise and nothing running to report on, so the
# normal closing banner would be a page of lies.
stage::image_summary() {
    cat >&2 <<BANNER

${C_GRN}${C_BOLD}HomeOS ${HOMEOS_VERSION} baked into the image.${C_RESET}

  Configured and enabled, deliberately not started. On first boot the
  appliance generates its own host identity, grows the root filesystem to
  fill the disk it was written to, and creates the container network.

BANNER
}

# Rewrite the managed configuration from this tree without installing anything.
#
# This exists because an over-the-air update could replace the daemon, the
# dashboard and the helper scripts, but not one line of the files they read.
# The Caddyfile, the systemd units, the udev rules and the sudoers policy are
# written by the installer and were never revisited, so a bug in any of them
# could only be fixed by reinstalling the appliance — which is the thing
# updates exist to avoid.
#
# Deliberately a strict subset: config.yaml is not touched, because it holds
# the operator's settings and a release has no business overwriting them.
# Nothing is installed, no account is created, no filesystem tree is laid out.
stage::reconfigure() {
    log::step "Reapplying managed configuration"

    # Read the appliance's identity back out of the running system rather than
    # letting it default: rewriting the proxy for "homenas" on an appliance the
    # operator named something else would take the dashboard off the network.
    local cfg=/etc/homeos/config.yaml v
    if [[ -r "$cfg" ]]; then
        v="$(awk '/^system:/ { s = 1; next } /^[^[:space:]]/ { s = 0 }
                   s && $1 == "hostname:" { print $2; exit }' "$cfg")"
        [[ -n "$v" ]] && HOMEOS_HOSTNAME="$v"
        v="$(awk '/^  default_route_mode:/ { print $2; exit }' "$cfg")"
        [[ -n "$v" ]] && HOMEOS_ROUTE_MODE="$v"
    fi
    HOMEOS_PRIMARY_IFACE="$(primary_iface || true)"
    [[ -n "$HOMEOS_PRIMARY_IFACE" ]] || HOMEOS_PRIMARY_IFACE="eth0"
    log::info "hostname ${HOMEOS_HOSTNAME}.local, routing ${HOMEOS_ROUTE_MODE}"

    # Every stage is attempted even when an earlier one fails. They are
    # independent writers, and abandoning the run at the first error is how a
    # problem writing the mDNS config stopped the proxy config — the thing the
    # update was carrying — from ever being written. A partial reapply reports
    # failure at the end; it does not skip the rest on the way there.
    local stage failed=()
    for stage in net::configure_avahi proxy::install fs::write_logrotate \
                 stage::udev stage::sudoers stage::systemd; do
        if ! "$stage"; then
            log::warn "${stage} failed"
            failed+=("$stage")
        fi
    done

    if (( ${#failed[@]} > 0 )); then
        log::error "configuration reapplied with failures: ${failed[*]}"
        return 1
    fi
    log::ok "configuration reapplied"
}

stage::summary() {
    local ip; ip="$(primary_ip)"
    cat >&2 <<BANNER

${C_GRN}${C_BOLD}HomeOS ${HOMEOS_VERSION} installed.${C_RESET}

  Dashboard    http://${HOMEOS_HOSTNAME}.local/        (mDNS)
               http://${ip}/                            (direct)

  Apps         default route mode: ${HOMEOS_ROUTE_MODE}
               host -> http://<app>.local/
               path -> http://${HOMEOS_HOSTNAME}.local/app/<app>/
               port -> http://${HOMEOS_HOSTNAME}.local:<port>/

  Config       /etc/homeos/config.yaml
  App data     /var/lib/homeos/apps
  Shares       /mnt/storage
  Logs         journalctl -u homeos-core -u homeos-proxy-sync

  Publish a container by labelling it:
      homeos.enable=true
      homeos.app=jellyfin
      homeos.port=8096
  ...then: homeos-proxy-sync sync   (or let the watcher pick it up)

${C_YLW}The backend and dashboard are not installed yet. Build them with:
  sudo scripts/install-build-deps.sh   (Go and Node, one time)
  make build && sudo make install${C_RESET}

BANNER
}

# Deliberately conservative: services, program code and generated config go;
# /var/lib/homeos, /mnt/storage and the Docker volumes stay. Removing user data
# is a separate, explicit act.
stage::uninstall() {
    log::step "Uninstalling HomeOS"
    log::warn "this removes HomeOS services and configuration"
    log::warn "it keeps /var/lib/homeos, /mnt/storage and all Docker volumes"
    confirm "Proceed with uninstall?" || die "aborted"

    local unit
    for unit in homeos-core homeos-proxy-sync homeos-firstboot; do
        run systemctl disable --now "${unit}.service" >/dev/null 2>&1 || true
        run rm -f "/etc/systemd/system/${unit}.service"
    done
    # Transient mDNS alias units.
    systemctl list-units --state=active --no-legend --plain 'homeos-mdns-*.service' 2>/dev/null \
        | awk '{print $1}' | while read -r u; do
              [[ -n "$u" ]] && run systemctl stop "$u" >/dev/null 2>&1 || true
          done
    svc_reload_units

    run rm -f /etc/udev/rules.d/99-homeos-storage.rules
    run rm -f /etc/sudoers.d/homeos
    run rm -f /etc/systemd/system/caddy.service.d/10-homeos.conf
    run rm -f /etc/logrotate.d/homeos
    run rm -f /etc/avahi/services/homeos.service
    run rm -f /usr/local/bin/homeos-proxy-sync
    run rm -rf /usr/lib/homeos /opt/homeos /etc/homeos/proxy
    run udevadm control --reload-rules >/dev/null 2>&1 || true

    # Restore anything we replaced in place.
    local f
    for f in /etc/samba/smb.conf /etc/avahi/avahi-daemon.conf /etc/nsswitch.conf /etc/hosts; do
        if [[ -f "${f}.homeos-orig" ]]; then
            run mv -f "${f}.homeos-orig" "$f"
            log::ok "restored $f"
        fi
    done

    run systemctl restart caddy avahi-daemon smbd >/dev/null 2>&1 || true

    log::ok "HomeOS removed"
    log::info "user data left in place: /var/lib/homeos, /mnt/storage, /etc/homeos"
    log::info "remove it manually if that is what you want"
}

# --------------------------------------------------------------------------
main() {
    parse_args "$@"

    # Reconfigure runs ahead of preflight on purpose: preflight measures a
    # machine about to be installed onto, and one of its checks is that port 80
    # is free — which on a running appliance it is not, because this is the
    # thing holding it.
    if [[ -n "$HOMEOS_RECONFIGURE" ]]; then
        [[ "$(id -u)" -eq 0 ]] || die "--reconfigure must be run as root"
        ensure_dir /var/log/homeos 0750 root:root
        HOMEOS_LOG_FILE=/var/log/homeos/install.log
        stage::reconfigure
        exit 0
    fi

    # Preflight runs before anything is written, so a rejected system is left
    # exactly as it was found.
    preflight::run

    ensure_dir /var/log/homeos 0750 root:root
    HOMEOS_LOG_FILE=/var/log/homeos/install.log
    log::info "transcript: $HOMEOS_LOG_FILE"

    if [[ -n "$HOMEOS_UNINSTALL" ]]; then
        stage::uninstall
        exit 0
    fi

    [[ -n "$HOMEOS_TZ" ]] || HOMEOS_TZ="$(timedatectl show -p Timezone --value 2>/dev/null || echo UTC)"
    HOMEOS_PRIMARY_IFACE="$(primary_iface || true)"
    [[ -n "$HOMEOS_PRIMARY_IFACE" ]] || HOMEOS_PRIMARY_IFACE="eth0"

    log::info "target hostname : ${HOMEOS_HOSTNAME}.local"
    log::info "architecture    : ${HOMEOS_ARCH}"
    log::info "LAN interface   : ${HOMEOS_PRIMARY_IFACE} ($(primary_ip))"
    log::info "default routing : ${HOMEOS_ROUTE_MODE}"
    confirm "Install HomeOS onto this system?" || die "aborted"

    packages::bootstrap
    stage::timezone

    fs::create_account
    fs::create_tree

    if [[ -n "$HOMEOS_SKIP_DOCKER" ]]; then
        log::step "Docker Engine"
        have docker || die "--skip-docker was given but no docker binary is present"
        log::skip "using the existing Docker installation"
        docker::create_networks
    else
        docker::install
        docker::create_networks
    fi
    docker::grant_service_account "$HOMEOS_USER"

    packages::core
    net::set_hostname
    net::configure_avahi

    if [[ -n "$HOMEOS_SKIP_SAMBA" ]]; then
        log::step "Samba"
        log::skip "skipped at the operator's request"
    else
        samba::configure
    fi

    fs::write_config
    fs::write_logrotate
    proxy::install

    stage::runtime_files
    stage::udev
    stage::sudoers
    stage::systemd

    # config.yaml is written before the route mode is known to the proxy, so
    # reconcile the one value the operator can override on the command line.
    if [[ -z "${HOMEOS_DRY_RUN:-}" ]]; then
        sed -i "s/^\(  default_route_mode:\).*/\1 ${HOMEOS_ROUTE_MODE}/" /etc/homeos/config.yaml
    fi

    packages::verify

    if [[ -n "$HOMEOS_IMAGE_BUILD" ]]; then
        # The stamp is deliberately NOT written. An image is cloned to many
        # machines and every one of them needs first boot to run: unique SSH
        # host keys, a unique machine-id, the root filesystem grown to whatever
        # disk it was written to, and the container network created against a
        # daemon that is finally running.
        log::info "image build: first boot finishes the setup on the target"
        stage::image_summary
        return 0
    fi

    /usr/lib/homeos/bin/homeos-proxy-sync sync >/dev/null 2>&1 || true
    [[ -n "${HOMEOS_DRY_RUN:-}" ]] || \
        date -u '+%Y-%m-%dT%H:%M:%SZ' > /var/lib/homeos/.firstboot-done

    net::report
    stage::summary
}

main "$@"
