#!/usr/bin/env bash
#
# build-release.sh — produce signed HomeOS release archives and a channel
# manifest.
#
#   scripts/build-release.sh 1.1.0 https://dl.example.com/homeos
#
# Output lands in dist/:
#   homeos-<version>-linux-amd64.tar.gz
#   homeos-<version>-linux-arm64.tar.gz
#   stable.json
#
# The signing key is never generated here. Create it once with
# `homeos-release keygen`, keep it off the build machine's shared paths, and
# back it up: losing it means every appliance in the field stops trusting new
# releases.
#
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

VERSION="${1:-}"
BASE_URL="${2:-}"
SIGNING_KEY="${HOMEOS_SIGNING_KEY:-$HOME/.homeos/homeos-update.key}"
CHANNEL="${HOMEOS_CHANNEL:-stable}"
DIST="${REPO_ROOT}/dist"
ARCHES=(amd64 arm64)

die() { printf 'build-release: %s\n' "$*" >&2; exit 1; }
step() { printf '\n==> %s\n' "$*"; }

[[ -n "$VERSION" ]]  || die "usage: scripts/build-release.sh VERSION BASE_URL"
[[ -n "$BASE_URL" ]] || die "usage: scripts/build-release.sh VERSION BASE_URL"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$ ]] \
    || die "version '$VERSION' is not MAJOR.MINOR.PATCH[-PRERELEASE]"
[[ -r "$SIGNING_KEY" ]] \
    || die "no signing key at ${SIGNING_KEY}
     Generate one with:  homeos-release keygen -out ~/.homeos
     or point HOMEOS_SIGNING_KEY at an existing key."

command -v go >/dev/null  || die "go is required"
command -v npm >/dev/null || die "npm is required"
command -v tar >/dev/null || die "tar is required"

# The public half is stamped into every binary, so an appliance verifies
# against the key that signed its own release rather than one it was told about.
PUBKEY_FILE="${SIGNING_KEY%.key}.pub"
[[ -r "$PUBKEY_FILE" ]] || die "expected the public key beside the private one at ${PUBKEY_FILE}"
PUBKEY="$(tr -d '[:space:]' < "$PUBKEY_FILE")"

step "Building the dashboard"
( cd web && npm ci )
( cd web && npm run build )
[[ -f web/dist/index.html ]] || die "the dashboard build produced no index.html"

step "Building the release tool"
( cd backend && go build -o "${DIST}/homeos-release" ./cmd/homeos-release )

rm -rf "${DIST}/stage"
mkdir -p "$DIST"

for arch in "${ARCHES[@]}"; do
    step "Assembling linux/${arch}"
    stage="${DIST}/stage/${arch}"
    mkdir -p "${stage}/bin" "${stage}/scripts" "${stage}/config"

    # CGO off keeps the binary static, so it runs on glibc and musl alike
    # without matching the build host. -trimpath keeps build paths out of it,
    # which makes the archive reproducible for a given source tree.
    ( cd backend && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
        -trimpath \
        -ldflags "-s -w -X main.Version=${VERSION} -X main.UpdatePublicKey=${PUBKEY}" \
        -o "${stage}/bin/homeos-core" ./cmd/homeos-core )

    cp scripts/homeos-proxy-sync scripts/homeos-disk-event scripts/homeos-firstboot \
       scripts/homeos-apply-update scripts/homeos-update-check "${stage}/bin/"
    chmod 0755 "${stage}"/bin/*
    cp scripts/lib/*.sh "${stage}/scripts/"

    # Unit files and rules ship with the release so an update can carry a
    # changed unit; the applier reloads systemd after the swap.
    cp -r config/systemd config/udev config/sudoers "${stage}/config/"

    # The installer travels with the release so that homeos-apply-update can run
    # its --reconfigure path. Without it an update can replace every executable
    # and still not fix a line in the Caddyfile the executables read.
    cp install.sh "${stage}/"

    cp -r web/dist "${stage}/web"
    printf '%s\n' "$VERSION" > "${stage}/VERSION"

    # --sort=name and a fixed mtime make the archive byte-identical across
    # builds of the same tree, so a rebuilt release verifies against the
    # signature that was published for it.
    tar --sort=name \
        --mtime="@0" --owner=0 --group=0 --numeric-owner \
        -czf "${DIST}/homeos-${VERSION}-linux-${arch}.tar.gz" \
        -C "$stage" .
    printf '    %s (%s)\n' "homeos-${VERSION}-linux-${arch}.tar.gz" \
        "$(du -h "${DIST}/homeos-${VERSION}-linux-${arch}.tar.gz" | cut -f1)"
done

step "Signing and writing the channel manifest"
MERGE=()
[[ -f "${DIST}/${CHANNEL}.json" ]] && MERGE=(-merge "${DIST}/${CHANNEL}.json")
NOTES=()
[[ -f "CHANGELOG-${VERSION}.md" ]] && NOTES=(-notes "@CHANGELOG-${VERSION}.md")

"${DIST}/homeos-release" channel \
    -key "$SIGNING_KEY" \
    -version "$VERSION" \
    -base-url "$BASE_URL" \
    -channel "$CHANNEL" \
    "${MERGE[@]}" "${NOTES[@]}" \
    "${DIST}"/homeos-"${VERSION}"-linux-*.tar.gz > "${DIST}/${CHANNEL}.json.new"
mv -f "${DIST}/${CHANNEL}.json.new" "${DIST}/${CHANNEL}.json"

step "Verifying what was just produced"
"${DIST}/homeos-release" verify \
    -pub "$PUBKEY_FILE" -manifest "${DIST}/${CHANNEL}.json" -dir "$DIST"

rm -rf "${DIST}/stage"

cat <<SUMMARY

Release ${VERSION} is ready in ${DIST}

  Publish the archives and ${CHANNEL}.json to ${BASE_URL}
  Appliances polling that URL will offer the update on their next check.

SUMMARY
