#!/usr/bin/env bash
# HomeOS — APT repositories and base system packages.
# shellcheck shell=bash

[[ -n "${_HOMEOS_PACKAGES_SH:-}" ]] && return 0
_HOMEOS_PACKAGES_SH=1

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a          # never open the interactive restart TUI
export NEEDRESTART_SUSPEND=1

_APT_UPDATED=""

apt::update() {
    local force="${1:-}"
    if [[ -n "$_APT_UPDATED" && -z "$force" ]]; then
        log::skip "apt index already refreshed"
        return 0
    fi
    log::info "refreshing package index"
    run_quiet apt-get update -qq || die "apt-get update failed - check the APT sources"
    _APT_UPDATED=1
}

# apt::install <pkg>... — installs only what is missing, so re-runs are cheap.
apt::install() {
    local want=("$@") missing=() p
    for p in "${want[@]}"; do
        if dpkg-query -W -f='${Status}' "$p" 2>/dev/null | grep -q "^install ok installed$"; then
            continue
        fi
        missing+=("$p")
    done

    if (( ${#missing[@]} == 0 )); then
        log::skip "already installed: ${want[*]}"
        return 0
    fi

    apt::update
    log::info "installing: ${missing[*]}"
    run_quiet apt-get install -y -qq --no-install-recommends "${missing[@]}" \
        || die "failed to install: ${missing[*]}"
    log::ok "installed ${#missing[@]} package(s)"
}

# Package is installed only if it exists in the index; used for optional extras
# whose names differ across Debian and Ubuntu releases.
apt::install_if_available() {
    local p avail=()
    for p in "$@"; do
        if apt-cache show "$p" >/dev/null 2>&1; then
            avail+=("$p")
        else
            log::skip "not in this release's index: $p"
        fi
    done
    (( ${#avail[@]} > 0 )) && apt::install "${avail[@]}"
    return 0
}

# apt::add_repo <name> <gpg-url> <repo-line-template>
# The template may reference ${ARCH}, ${CODENAME} and ${KEYRING}.
apt::add_repo() {
    local name="$1" key_url="$2" line_tmpl="$3"
    local keyring="/etc/apt/keyrings/${name}.gpg"
    local listfile="/etc/apt/sources.list.d/${name}.list"

    ensure_dir /etc/apt/keyrings 0755 root:root

    if [[ ! -s "$keyring" ]]; then
        log::info "fetching signing key for ${name}"
        local tmpkey; tmpkey="$(mktemp)"
        if ! run curl -fsSL --retry 3 --retry-delay 2 --max-time 60 -o "$tmpkey" "$key_url"; then
            rm -f "$tmpkey"
            die "could not download the ${name} signing key from ${key_url}"
        fi
        # Accept both ASCII-armoured and binary keyrings.
        if grep -q "BEGIN PGP PUBLIC KEY BLOCK" "$tmpkey" 2>/dev/null; then
            run gpg --dearmor --yes --output "$keyring" "$tmpkey" \
                || die "could not dearmor the ${name} key"
        else
            run install -m 0644 "$tmpkey" "$keyring"
        fi
        rm -f "$tmpkey"
        run chmod 0644 "$keyring"
        log::ok "installed keyring $keyring"
    else
        log::skip "keyring present: $keyring"
    fi

    local ARCH="$HOMEOS_ARCH" CODENAME="$HOMEOS_OS_CODENAME" KEYRING="$keyring"
    local line="$line_tmpl"
    line="${line//\$\{ARCH\}/$ARCH}"
    line="${line//\$\{CODENAME\}/$CODENAME}"
    line="${line//\$\{KEYRING\}/$KEYRING}"

    if printf '%s\n' "$line" | write_file "$listfile" 0644 root:root; then
        _APT_UPDATED=""     # new source: force a refresh on the next install
    fi
}

# --------------------------------------------------------------------------
# Package sets
# --------------------------------------------------------------------------

# Bootstrap tools needed before any third-party repository can be added.
packages::bootstrap() {
    log::step "Base tooling"
    apt::install ca-certificates curl gnupg apt-transport-https lsb-release
}

# Everything HomeOS itself relies on at runtime, grouped by the subsystem that
# needs it so a future slimming pass knows what it is dropping.
packages::core() {
    log::step "System packages"

    # Storage + NAS: partitioning, filesystems, SMART, mount helpers.
    apt::install \
        smartmontools udev util-linux parted gdisk \
        e2fsprogs btrfs-progs xfsprogs dosfstools exfatprogs ntfs-3g \
        rsync psmisc lsof

    # File sharing.
    apt::install samba samba-common-bin

    # Service discovery: homenas.local over mDNS.
    apt::install avahi-daemon avahi-utils libnss-mdns

    # Backend runtime helpers: JSON handling, telemetry sources, git for the
    # app-store checkout, iproute2 for the network helpers in common.sh.
    apt::install jq git iproute2 procps

    # Optional sensors package: absent on some minimal ARM images.
    apt::install_if_available lm-sensors

    # unattended-upgrades keeps the OS patched without touching HomeOS itself,
    # which updates over its own OTA channel (phase 4).
    apt::install_if_available unattended-upgrades
}

# Verify the binaries the later stages actually shell out to.
packages::verify() {
    log::step "Verifying required binaries"
    local missing=() b
    for b in docker curl jq git awk sed smartctl lsblk blkid \
             avahi-daemon smbd caddy systemctl; do
        if have "$b"; then
            log::debug "found $b"
        else
            missing+=("$b")
        fi
    done
    if (( ${#missing[@]} > 0 )); then
        die "installation incomplete, these binaries are missing: ${missing[*]}"
    fi
    log::ok "all required binaries present"
}
