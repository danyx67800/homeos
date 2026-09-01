#!/usr/bin/env bash
#
# install-build-deps.sh — install the toolchain needed to build HomeOS from
# source on the appliance itself.
#
#   sudo scripts/install-build-deps.sh
#
# install.sh deliberately does not do this. An appliance has no business
# carrying a Go compiler: the normal way to get HomeOS onto a box is a prebuilt
# release. This script exists for the case where you are building from source
# on the machine itself, which the quick start describes.
#
# Node comes from apt — every supported release ships a version new enough.
# Go does not: HomeOS needs 1.25 and the newest any supported distribution
# packages is 1.24, so it is fetched from go.dev and checksum-verified.
#
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
GO_INSTALL_DIR="${GO_INSTALL_DIR:-/usr/local}"
PROFILE_D=/etc/profile.d/homeos-go.sh

c_ok=''; c_warn=''; c_err=''; c_off=''
if [[ -t 1 ]]; then c_ok=$'\033[32m'; c_warn=$'\033[33m'; c_err=$'\033[31m'; c_off=$'\033[0m'; fi
step() { printf '\n%s==>%s %s\n' $'\033[34m' "$c_off" "$*"; }
ok()   { printf '  %s[ok]%s %s\n' "$c_ok" "$c_off" "$*"; }
warn() { printf '  %s[!!]%s %s\n' "$c_warn" "$c_off" "$*"; }
die()  { printf '  %s[XX]%s %s\n' "$c_err" "$c_off" "$*" >&2; exit 1; }

[[ "${EUID:-$(id -u)}" -eq 0 ]] || die "run this with sudo"

# Under `set -euo pipefail` a command substitution whose command does not exist
# aborts the script with no output at all — which is exactly the experience this
# script exists to spare people. Name the line instead.
trap 'rc=$?; printf "
  %s[XX]%s failed at line %s (exit %s)
" "$c_err" "$c_off" "$LINENO" "$rc" >&2' ERR

# --------------------------------------------------------------------------
# Which Go version? Read it from go.mod so this script can never drift from
# what the code actually requires.
# --------------------------------------------------------------------------
[[ -r "${REPO_ROOT}/backend/go.mod" ]] \
    || die "cannot read ${REPO_ROOT}/backend/go.mod — run this from a HomeOS checkout"
GO_REQUIRED="$(awk '/^go [0-9]/ {print $2; exit}' "${REPO_ROOT}/backend/go.mod")" || true
[[ -n "$GO_REQUIRED" ]] || die "backend/go.mod has no 'go' directive"
# go.mod may say 1.25 or 1.25.0; the download needs a full version.
[[ "$GO_REQUIRED" == *.*.* ]] || GO_REQUIRED="${GO_REQUIRED}.0"

case "$(uname -m)" in
    x86_64|amd64)  GOARCH=amd64 ;;
    aarch64|arm64) GOARCH=arm64 ;;
    *) die "unsupported architecture $(uname -m); HomeOS builds for amd64 and arm64" ;;
esac

# probe_version <binary> <args...> — print a version string, or nothing if the
# binary is absent. Guarded by command -v because a missing command inside a
# pipeline returns 127, and pipefail would turn "not installed" into a fatal
# error.
probe_version() {
    local bin="$1"; shift
    command -v "$bin" >/dev/null 2>&1 || return 0
    "$bin" "$@" 2>/dev/null || true
}

# version_ge A B — true when A >= B, comparing dotted numbers.
version_ge() {
    [[ "$1" == "$2" ]] && return 0
    local first
    first="$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n1)"
    [[ "$first" == "$2" ]]
}

step "Base tooling"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates curl tar git >/dev/null
ok "curl, tar, git, ca-certificates"

# --------------------------------------------------------------------------
# Node.js — apt is fine here
# --------------------------------------------------------------------------
step "Node.js (dashboard build)"
NODE_MIN=18

have_node="$(probe_version node --version | sed 's/^v//')"

if [[ -n "$have_node" ]] && version_ge "$have_node" "$NODE_MIN"; then
    ok "node ${have_node} already installed"
else
    apt-get install -y -qq --no-install-recommends nodejs npm >/dev/null \
        || die "could not install nodejs from apt"
    have_node="$(probe_version node --version | sed 's/^v//')"
    [[ -n "$have_node" ]] || die "nodejs installed but 'node' is not on PATH"
    version_ge "$have_node" "$NODE_MIN" \
        || die "this release packages node ${have_node}, but the dashboard needs
     ${NODE_MIN}+. Install a newer one from https://github.com/nodesource/distributions"
    ok "node ${have_node}"
fi
ok "npm $(npm --version 2>/dev/null || echo '?')"

# --------------------------------------------------------------------------
# Go — must come from go.dev
# --------------------------------------------------------------------------
step "Go ${GO_REQUIRED} (backend build)"

installed_go="$(probe_version "${GO_INSTALL_DIR}/go/bin/go" version | awk '{print $3}' | sed 's/^go//')"
if [[ -z "$installed_go" ]]; then
    installed_go="$(probe_version go version | awk '{print $3}' | sed 's/^go//')"
fi

if [[ -n "$installed_go" ]] && version_ge "$installed_go" "$GO_REQUIRED"; then
    ok "go ${installed_go} already installed and new enough"
else
    [[ -n "$installed_go" ]] && warn "go ${installed_go} is installed but older than ${GO_REQUIRED}"

    TARBALL="go${GO_REQUIRED}.linux-${GOARCH}.tar.gz"
    URL="https://go.dev/dl/${TARBALL}"

    # The index is pretty-printed, so a file's sha256 sits a few lines below its
    # filename. grep works on lines, not on records, so -A is what ties the two
    # together; splitting on braces looks right and silently finds nothing.
    WANT_SHA="$(curl -fsSL --max-time 60 'https://go.dev/dl/?mode=json&include=all' \
        | grep -A6 -F "\"filename\": \"${TARBALL}\"" \
        | grep -o '"sha256": "[a-f0-9]\{64\}"' | head -n1 | cut -d'"' -f4)" || true
    [[ -n "$WANT_SHA" ]] \
        || die "could not find a published checksum for ${TARBALL}.
     Check that Go ${GO_REQUIRED} exists for linux/${GOARCH} at https://go.dev/dl/"

    TMP="$(mktemp -d)"
    trap 'rm -rf "$TMP"' EXIT

    printf '  downloading %s\n' "$URL"
    curl -fSL --retry 3 --retry-delay 2 --progress-bar -o "${TMP}/${TARBALL}" "$URL" \
        || die "download failed"

    GOT_SHA="$(sha256sum "${TMP}/${TARBALL}" | cut -d' ' -f1)"
    [[ "$GOT_SHA" == "$WANT_SHA" ]] \
        || die "checksum mismatch for ${TARBALL}
     expected ${WANT_SHA}
     got      ${GOT_SHA}
     The download was corrupted or tampered with. Nothing was installed."
    ok "checksum verified"

    # Replace rather than merge: unpacking a new Go over an old tree leaves
    # stale files from packages that were removed upstream.
    rm -rf "${GO_INSTALL_DIR}/go"
    tar -C "$GO_INSTALL_DIR" -xzf "${TMP}/${TARBALL}" || die "could not unpack Go"
    ok "installed to ${GO_INSTALL_DIR}/go"
fi

# --------------------------------------------------------------------------
# PATH
# --------------------------------------------------------------------------
step "PATH"
cat > "$PROFILE_D" <<'PROFILE'
# Added by HomeOS install-build-deps.sh
# Go is installed from go.dev rather than apt, so it is not on the default PATH.
export PATH="$PATH:/usr/local/go/bin"
PROFILE
chmod 0644 "$PROFILE_D"
ok "wrote ${PROFILE_D}"

# A login shell picks that up next time. `sudo make build` will not, because
# sudo resets PATH from secure_path, so the symlink is what actually makes the
# documented commands work in this session and under sudo.
ln -sfn "${GO_INSTALL_DIR}/go/bin/go" /usr/local/bin/go
ln -sfn "${GO_INSTALL_DIR}/go/bin/gofmt" /usr/local/bin/gofmt
ok "linked go and gofmt into /usr/local/bin"

# --------------------------------------------------------------------------
step "Done"
export PATH="$PATH:${GO_INSTALL_DIR}/go/bin"
printf '  %-8s %s\n' "go"   "$(go version 2>/dev/null || echo 'NOT FOUND')"
printf '  %-8s %s\n' "node" "$(node --version 2>/dev/null || echo 'NOT FOUND')"
printf '  %-8s %s\n' "npm"  "$(npm --version 2>/dev/null || echo 'NOT FOUND')"

cat <<'NEXT'

Now build and install HomeOS:

    make build
    sudo make install
    sudo systemctl restart homeos-core

NEXT
