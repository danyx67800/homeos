#!/usr/bin/env bash
# HomeOS — shared shell library.
# Sourced by install.sh and by the runtime helpers under /usr/lib/homeos/bin.
# shellcheck shell=bash disable=SC2034

[[ -n "${_HOMEOS_COMMON_SH:-}" ]] && return 0
_HOMEOS_COMMON_SH=1

# --------------------------------------------------------------------------
# Terminal styling
# --------------------------------------------------------------------------
if [[ -t 2 ]] && [[ "${TERM:-dumb}" != "dumb" ]] && [[ -z "${NO_COLOR:-}" ]]; then
    C_RESET=$'\033[0m'; C_BOLD=$'\033[1m';  C_DIM=$'\033[2m'
    C_RED=$'\033[31m';  C_GRN=$'\033[32m';  C_YLW=$'\033[33m'
    C_BLU=$'\033[34m';  C_MAG=$'\033[35m';  C_CYN=$'\033[36m'
else
    C_RESET=''; C_BOLD=''; C_DIM=''
    C_RED='';   C_GRN=''; C_YLW=''; C_BLU=''; C_MAG=''; C_CYN=''
fi

# --------------------------------------------------------------------------
# Logging
#   HOMEOS_LOG_FILE  append-target for a permanent transcript (optional)
#   HOMEOS_QUIET=1   suppress info/step chatter on stderr (file log unaffected)
#   HOMEOS_DEBUG=1   emit log::debug
# --------------------------------------------------------------------------
HOMEOS_LOG_FILE="${HOMEOS_LOG_FILE:-}"

_log_write() {
    local ts
    ts="$(date '+%Y-%m-%dT%H:%M:%S%z')"
    if [[ -n "$HOMEOS_LOG_FILE" ]]; then
        printf '%s [%s] %s\n' "$ts" "$1" "$2" >>"$HOMEOS_LOG_FILE" 2>/dev/null || true
    fi
}

log::step()  { _log_write STEP "$*"; [[ -n "${HOMEOS_QUIET:-}" ]] && return 0
               printf '\n%s==>%s %s%s%s\n' "$C_BLU" "$C_RESET" "$C_BOLD" "$*" "$C_RESET" >&2; }
log::info()  { _log_write INFO "$*"; [[ -n "${HOMEOS_QUIET:-}" ]] && return 0
               printf '  %s.%s %s\n' "$C_CYN" "$C_RESET" "$*" >&2; }
log::ok()    { _log_write OK "$*"; [[ -n "${HOMEOS_QUIET:-}" ]] && return 0
               printf '  %s[ok]%s %s\n' "$C_GRN" "$C_RESET" "$*" >&2; }
log::skip()  { _log_write SKIP "$*"; [[ -n "${HOMEOS_QUIET:-}" ]] && return 0
               printf '  %s[--]%s %s%s%s\n' "$C_DIM" "$C_RESET" "$C_DIM" "$*" "$C_RESET" >&2; }
log::warn()  { _log_write WARN "$*"
               printf '  %s[!!]%s %s\n' "$C_YLW" "$C_RESET" "$*" >&2; }
log::error() { _log_write ERROR "$*"
               printf '  %s[XX]%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; }
log::debug() { [[ -z "${HOMEOS_DEBUG:-}" ]] && return 0
               _log_write DEBUG "$*"
               printf '  %sdbg%s %s\n' "$C_MAG" "$C_RESET" "$*" >&2; }

die() { log::error "$*"; exit 1; }

# --------------------------------------------------------------------------
# Execution helpers
#   HOMEOS_DRY_RUN=1 prints commands instead of running them.
# --------------------------------------------------------------------------
run() {
    if [[ -n "${HOMEOS_DRY_RUN:-}" ]]; then
        printf '  %sdry%s %s\n' "$C_YLW" "$C_RESET" "$*" >&2
        return 0
    fi
    log::debug "exec: $*"
    "$@"
}

# Run a command, capture its output, surface it only on failure.
run_quiet() {
    if [[ -n "${HOMEOS_DRY_RUN:-}" ]]; then
        printf '  %sdry%s %s\n' "$C_YLW" "$C_RESET" "$*" >&2
        return 0
    fi
    local out rc=0
    out="$("$@" 2>&1)" || rc=$?
    if (( rc != 0 )); then
        log::error "command failed (rc=$rc): $*"
        printf '%s\n' "$out" | sed 's/^/      /' >&2
        return "$rc"
    fi
    log::debug "ok: $*"
    return 0
}

have()    { command -v "$1" >/dev/null 2>&1; }
require() { have "$1" || die "required command not found: $1"; }

# --------------------------------------------------------------------------
# Idempotent filesystem primitives
# --------------------------------------------------------------------------

# ensure_dir <path> [mode] [owner:group]
ensure_dir() {
    local path="$1" mode="${2:-0755}" own="${3:-root:root}"
    if [[ -d "$path" ]]; then
        run chmod "$mode" "$path"
        run chown "$own" "$path"
        log::skip "dir exists: $path"
    else
        run install -d -m "$mode" -o "${own%%:*}" -g "${own##*:}" "$path" \
            || die "cannot create directory: $path"
        log::ok "created $path ($mode $own)"
    fi
}

# backup_file <path> — keep one pristine copy the first time we touch a file we
# did not author, so uninstall/rollback has something to restore.
backup_file() {
    local path="$1"
    [[ -f "$path" ]] || return 0
    local marker="${path}.homeos-orig"
    [[ -e "$marker" ]] && return 0
    run cp -a "$path" "$marker"
    log::info "backed up $path -> $marker"
}

# write_file <path> [mode] [owner:group] — content on stdin, written atomically.
# Returns 0 when the file changed, 10 when it was already byte-identical.
write_file() {
    local path="$1" mode="${2:-0644}" own="${3:-root:root}"
    local tmp
    tmp="$(mktemp "${path}.homeos.XXXXXX" 2>/dev/null)" || tmp="$(mktemp)"
    cat >"$tmp"
    if [[ -f "$path" ]] && cmp -s "$tmp" "$path"; then
        rm -f "$tmp"
        log::skip "unchanged: $path"
        return 10
    fi
    if [[ -n "${HOMEOS_DRY_RUN:-}" ]]; then
        printf '  %sdry%s write %s\n' "$C_YLW" "$C_RESET" "$path" >&2
        rm -f "$tmp"
        return 0
    fi
    backup_file "$path"
    chmod "$mode" "$tmp"
    chown "$own" "$tmp" 2>/dev/null || true
    mv -f "$tmp" "$path"
    log::ok "wrote $path"
    return 0
}

# Insert (or replace) a HomeOS-managed block inside a foreign config file.
#   ensure_managed_block <file> [comment-prefix] [tag]   body on stdin
# Rewrites in place so the original inode, mode and ownership survive.
# Returns 0 when the file changed, 10 when it was already identical.
ensure_managed_block() {
    local file="$1" cprefix="${2:-#}" tag="${3:-homeos}"
    local begin="${cprefix} >>> ${tag} managed block >>>"
    local end="${cprefix} <<< ${tag} managed block <<<"
    local body tmp
    body="$(cat)"
    tmp="$(mktemp)"

    [[ -f "$file" ]] || : >"$file"
    backup_file "$file"

    # Drop any previous copy of the block, then re-append the current one.
    awk -v b="$begin" -v e="$end" '
        $0 == b { skip = 1; next }
        $0 == e { skip = 0; next }
        skip != 1 { print }
    ' "$file" >"$tmp"

    # Collapse trailing blank lines left behind by the removal.
    while [[ -s "$tmp" ]] && [[ -z "$(tail -n 1 "$tmp")" ]]; do
        sed -i '$ d' "$tmp"
    done

    {
        [[ -s "$tmp" ]] && printf '\n'
        printf '%s\n%s\n%s\n' "$begin" "$body" "$end"
    } >>"$tmp"

    if cmp -s "$tmp" "$file"; then
        rm -f "$tmp"
        log::skip "unchanged: $file"
        return 10
    fi
    if [[ -n "${HOMEOS_DRY_RUN:-}" ]]; then
        printf '  %sdry%s patch %s\n' "$C_YLW" "$C_RESET" "$file" >&2
        rm -f "$tmp"
        return 0
    fi
    cat "$tmp" >"$file"
    rm -f "$tmp"
    log::ok "patched $file"
    return 0
}

# --------------------------------------------------------------------------
# systemd helpers
# --------------------------------------------------------------------------
systemd_available() { have systemctl && [[ -d /run/systemd/system ]]; }

svc_reload_units() {
    systemd_available || return 0
    run systemctl daemon-reload
}

svc_enable_now() {
    local unit="$1"

    # `systemctl enable` only writes symlinks into the unit tree, so it works
    # perfectly well against a root filesystem whose systemd is not running —
    # which is exactly what an image build is. Only *starting* needs a live
    # systemd. Conflating the two meant an image shipped with avahi-daemon and
    # homeos-proxy-sync installed, configured, and never enabled.
    run systemctl enable "$unit" >/dev/null 2>&1 || log::warn "could not enable $unit"

    if ! systemd_available; then
        log::skip "enabled $unit; not starting it (systemd is not running here)"
        return 0
    fi

    if run systemctl restart "$unit"; then
        log::ok "$unit active"
    else
        log::warn "$unit failed to start - inspect: journalctl -u $unit -n 50"
        return 1
    fi
}

# --------------------------------------------------------------------------
# Networking helpers
# --------------------------------------------------------------------------

# Primary LAN IPv4 (route-based, so it picks the real egress NIC).
primary_ip() {
    local ip
    ip="$(ip -4 route get 1.1.1.1 2>/dev/null \
          | awk '{for (i = 1; i <= NF; i++) if ($i == "src") { print $(i+1); exit }}')"
    [[ -n "$ip" ]] || ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    printf '%s' "${ip:-127.0.0.1}"
}

primary_iface() {
    ip -4 route get 1.1.1.1 2>/dev/null \
        | awk '{for (i = 1; i <= NF; i++) if ($i == "dev") { print $(i+1); exit }}'
}

port_in_use() {
    local port="$1"
    if have ss; then
        ss -lntuH "sport = :${port}" 2>/dev/null | grep -q .
    elif have netstat; then
        netstat -lntu 2>/dev/null | awk '{print $4}' | grep -qE "[:.]${port}\$"
    else
        return 1
    fi
}

# --------------------------------------------------------------------------
# Misc
# --------------------------------------------------------------------------

# Lowercase, keep [a-z0-9-], collapse and trim dashes.
slugify() {
    printf '%s' "$1" \
        | tr '[:upper:]' '[:lower:]' \
        | sed -e 's/[^a-z0-9-]\{1,\}/-/g' -e 's/-\{2,\}/-/g' -e 's/^-//' -e 's/-$//'
}

confirm() {
    local prompt="${1:-Continue?}" reply
    [[ -n "${HOMEOS_ASSUME_YES:-}" ]] && return 0
    [[ -t 0 ]] || return 0
    read -r -p "  ${prompt} [y/N] " reply </dev/tty || return 1
    [[ "$reply" =~ ^[Yy]([Ee][Ss])?$ ]]
}
