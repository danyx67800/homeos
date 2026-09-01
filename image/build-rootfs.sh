#!/usr/bin/env bash
#
# build-rootfs.sh — bootstrap a Debian root filesystem with HomeOS installed.
#
#   sudo image/build-rootfs.sh <arch> <output-dir>
#
# This is the shared base: both the flashable disk image and the installer ISO
# are made from the tree this produces. It runs install.sh --image-build inside
# a chroot, so the image is configured by exactly the same code that configures
# a machine somebody installs by hand — there is no second implementation to
# drift out of step.
#
set -euo pipefail

ARCH="${1:-amd64}"
ROOTFS="${2:-}"
SUITE="${HOMEOS_SUITE:-bookworm}"
MIRROR="${HOMEOS_MIRROR:-http://deb.debian.org/debian}"
REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

step() { printf '\n\033[34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[31m[XX]\033[0m %s\n' "$*" >&2; exit 1; }

[[ "${EUID:-$(id -u)}" -eq 0 ]] || die "run with sudo: debootstrap and chroot need root"
[[ -n "$ROOTFS" ]] || die "usage: build-rootfs.sh <arch> <output-dir>"
case "$ARCH" in amd64|arm64) ;; *) die "arch must be amd64 or arm64" ;; esac

for t in debootstrap chroot; do
    command -v "$t" >/dev/null || die "$t is required (apt install debootstrap)"
done

# --------------------------------------------------------------------------
# Bootstrap
# --------------------------------------------------------------------------
step "debootstrap ${SUITE}/${ARCH}"
rm -rf "$ROOTFS"
mkdir -p "$ROOTFS"

DEBOOTSTRAP_ARGS=(
    --arch="$ARCH"
    --variant=minbase
    # ca-certificates and the apt plumbing have to exist before install.sh can
    # add the Docker and Caddy repositories.
    --include=systemd,systemd-sysv,dbus,ca-certificates,apt-transport-https,gnupg,curl,sudo,locales,tzdata,less,nano,openssh-server
)

if [[ "$ARCH" != "$(dpkg --print-architecture)" ]]; then
    command -v qemu-"${ARCH/arm64/aarch64}"-static >/dev/null \
        || die "cross-building ${ARCH} needs qemu-user-static and binfmt registered"
    DEBOOTSTRAP_ARGS+=(--foreign)
fi

debootstrap "${DEBOOTSTRAP_ARGS[@]}" "$SUITE" "$ROOTFS" "$MIRROR" \
    || die "debootstrap failed"

if [[ "$ARCH" != "$(dpkg --print-architecture)" ]]; then
    QEMU="/usr/bin/qemu-${ARCH/arm64/aarch64}-static"
    cp "$QEMU" "${ROOTFS}${QEMU}"
    chroot "$ROOTFS" /debootstrap/debootstrap --second-stage \
        || die "debootstrap second stage failed"
fi

# --------------------------------------------------------------------------
# chroot plumbing
# --------------------------------------------------------------------------
CLEANED=0
cleanup() {
    (( CLEANED )) && return 0
    CLEANED=1
    # Reverse order, and lazily: a leftover bind mount inside a rootfs that
    # then gets tarred or deleted is how build hosts lose their /dev.
    for m in dev/pts dev proc sys run; do
        mountpoint -q "${ROOTFS}/${m}" && umount -lf "${ROOTFS}/${m}" || true
    done
    rm -f "${ROOTFS}/usr/sbin/policy-rc.d" "${ROOTFS}/etc/resolv.conf.bak"
}
trap cleanup EXIT INT TERM

mount -t proc  proc   "${ROOTFS}/proc"
mount -t sysfs sysfs  "${ROOTFS}/sys"
mount --bind /dev     "${ROOTFS}/dev"
mount -t devpts devpts "${ROOTFS}/dev/pts"
mount -t tmpfs tmpfs  "${ROOTFS}/run"

cp /etc/resolv.conf "${ROOTFS}/etc/resolv.conf"

# apt starts daemons the moment it installs them. In a chroot that is at best
# useless and at worst wedges the build; policy-rc.d is the supported way to
# say no.
cat > "${ROOTFS}/usr/sbin/policy-rc.d" <<'POLICY'
#!/bin/sh
exit 101
POLICY
chmod 0755 "${ROOTFS}/usr/sbin/policy-rc.d"

in_chroot() { chroot "$ROOTFS" /usr/bin/env -i \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    HOME=/root TERM=dumb DEBIAN_FRONTEND=noninteractive \
    HOMEOS_IMAGE_BUILD=1 \
    /bin/bash -c "$*"; }

# --------------------------------------------------------------------------
# Base system
# --------------------------------------------------------------------------
step "Base configuration"
cat > "${ROOTFS}/etc/apt/sources.list" <<APT
deb ${MIRROR} ${SUITE} main contrib non-free-firmware
deb ${MIRROR} ${SUITE}-updates main contrib non-free-firmware
deb http://security.debian.org/debian-security ${SUITE}-security main contrib non-free-firmware
APT

in_chroot "apt-get update -qq" || die "apt-get update failed in the chroot"

# A kernel, firmware and the tools first boot needs to grow the root
# filesystem. cloud-guest-utils carries growpart, which homeos-firstboot uses.
KERNEL_PKGS="linux-image-${ARCH} firmware-linux-free"
[[ "$ARCH" == "amd64" ]] && KERNEL_PKGS="linux-image-amd64 firmware-linux-free"
[[ "$ARCH" == "arm64" ]] && KERNEL_PKGS="linux-image-arm64 firmware-linux-free"

# systemd-resolved is a separate package in Debian 12; minbase does not pull it
# in, and enabling a unit that does not exist is what broke networking here.
# No ifupdown or isc-dhcp-client: networkd is the one being configured, and two
# DHCP clients competing over the same interface is worse than either alone.
in_chroot "apt-get install -y -qq --no-install-recommends \
    ${KERNEL_PKGS} \
    initramfs-tools \
    cloud-guest-utils \
    systemd-resolved \
    systemd-timesyncd \
    haveged" || die "installing the base system failed"

step "Locale, hostname and console"
in_chroot "sed -i 's/^# *en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen && locale-gen >/dev/null"
echo "LANG=en_US.UTF-8" > "${ROOTFS}/etc/default/locale"
echo "homenas" > "${ROOTFS}/etc/hostname"
cat > "${ROOTFS}/etc/hosts" <<HOSTS
127.0.0.1	localhost
127.0.1.1	homenas homenas.local
::1		localhost ip6-localhost ip6-loopback
HOSTS

# DHCP on whatever the first wired interface turns out to be. An appliance is
# plugged into a home router; asking the user to configure networking before
# they can reach the dashboard defeats the point.
cat > "${ROOTFS}/etc/systemd/network/10-wired.network" <<NET
[Match]
Name=en* eth*

[Network]
DHCP=yes
NET
# One unit per command, and no swallowed errors. Enabling all three at once and
# hiding the output meant that when systemd-resolved turned out to be a separate
# package in Debian 12, systemctl failed on it and enabled *none* of them — so
# the image shipped with no network stack and no sign that anything was wrong.
for unit in systemd-networkd.service systemd-resolved.service ssh.service; do
    if in_chroot "systemctl enable ${unit}" >/dev/null 2>&1; then
        printf '  enabled %s\n' "$unit"
    else
        printf '  \033[33mcould not enable %s\033[0m\n' "$unit"
    fi
done

# An appliance with no network is useless and undiagnosable — the dashboard is
# the only interface it has. Refuse to build one rather than ship it.
in_chroot "systemctl is-enabled systemd-networkd.service" >/dev/null 2>&1 \
    || die "systemd-networkd is not enabled in the image; it would boot with no network"

# --------------------------------------------------------------------------
# HomeOS
# --------------------------------------------------------------------------
step "Installing HomeOS"

# The prebuilt binary and dashboard are expected beside this script. Building
# them inside the chroot would mean carrying a Go toolchain into the image, and
# cross-building a Go binary on the host is both faster and reproducible.
BIN="${REPO_ROOT}/dist/homeos-core-linux-${ARCH}"
WEB="${REPO_ROOT}/web/dist"
[[ -x "$BIN" ]] || die "missing ${BIN}
     Build it first:  make -C backend build-all"
[[ -f "${WEB}/index.html" ]] || die "missing ${WEB}/index.html
     Build it first:  make -C web build"

mkdir -p "${ROOTFS}/opt/homeos-src"
# --exclude keeps the build host's own artefacts out of the image.
tar -C "$REPO_ROOT" \
    --exclude=.git --exclude=node_modules --exclude=dist --exclude=.testbin \
    -cf - install.sh scripts config \
    | tar -C "${ROOTFS}/opt/homeos-src" -xf -

in_chroot "cd /opt/homeos-src && ./install.sh --image-build --hostname homenas" \
    || die "install.sh failed inside the chroot"

step "Baking in the backend and the dashboard"
install -D -m 0755 "$BIN" "${ROOTFS}/usr/lib/homeos/bin/homeos-core"
mkdir -p "${ROOTFS}/usr/lib/homeos/current/web"
cp -a "${WEB}/." "${ROOTFS}/usr/lib/homeos/current/web/"

# install.sh laid out releases/<version>/ and pointed current at it; the binary
# has to land inside that release, not beside it, or an update would replace a
# tree the running binary is not part of.
RELEASE_DIR="$(chroot "$ROOTFS" readlink -f /usr/lib/homeos/current 2>/dev/null || true)"
[[ -n "$RELEASE_DIR" ]] || die "install.sh did not create /usr/lib/homeos/current"
in_chroot "test -x /usr/lib/homeos/bin/homeos-core" \
    || die "the binary did not land where /usr/lib/homeos/bin points"
in_chroot "/usr/lib/homeos/bin/homeos-core -version" \
    || die "the baked-in binary does not run on ${ARCH}"

# --------------------------------------------------------------------------
# First boot
# --------------------------------------------------------------------------
step "Persistent journal"
# Without this directory journald keeps the log in RAM, and it is gone the
# moment the machine stops — which is exactly when you want to read it.
mkdir -p "${ROOTFS}/var/log/journal"

step "Console"
install -D -m 0755 "${REPO_ROOT}/scripts/homeos-console"     "${ROOTFS}/usr/lib/homeos/bin/homeos-console"

# tty1 shows status instead of a login prompt. An appliance's screen should say
# what its address is and whether it is working; a bare "homenas login:" says
# neither, and root is locked so nobody can get past it anyway.
mkdir -p "${ROOTFS}/etc/systemd/system/getty@tty1.service.d"
cat > "${ROOTFS}/etc/systemd/system/getty@tty1.service.d/homeos-console.conf" <<'GETTY'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I $TERM
Type=idle
GETTY

cat > "${ROOTFS}/root/.bash_profile" <<'PROFILE'
# The console screen on tty1 only. A serial console or SSH gets a normal shell,
# which is what you want when you are deliberately debugging.
if [ "$(tty)" = "/dev/tty1" ] && [ -x /usr/lib/homeos/bin/homeos-console ]; then
    exec /usr/lib/homeos/bin/homeos-console
fi
PROFILE

step "First-boot wiring"
in_chroot "systemctl enable homeos-firstboot homeos-core homeos-proxy-sync >/dev/null 2>&1" || true
# Belt and braces: install.sh already refuses to write this in image mode, but
# an image that skips first boot ships with cloned SSH host keys.
rm -f "${ROOTFS}/var/lib/homeos/.firstboot-done"

# No password is ever set, so SSH cannot authenticate as root and the account
# is unusable remotely. It is deliberately not *locked*, because locking it
# stops the physical console autologin too — which left the appliance with no
# way in at all when something went wrong.
in_chroot "passwd -d root >/dev/null 2>&1" || true
sed -i 's/^#*PermitRootLogin.*/PermitRootLogin prohibit-password/'     "${ROOTFS}/etc/ssh/sshd_config" 2>/dev/null || true

# --------------------------------------------------------------------------
# Slim down
# --------------------------------------------------------------------------
step "Cleaning up"
in_chroot "apt-get autoremove -y -qq && apt-get clean"
rm -rf "${ROOTFS}/opt/homeos-src" \
       "${ROOTFS}/var/lib/apt/lists"/* \
       "${ROOTFS}/var/cache/apt"/*.bin \
       "${ROOTFS}/tmp"/* \
       "${ROOTFS}/var/tmp"/*
# The build host's resolv.conf was copied in so apt could reach the network.
# Leaving it there ships the runner's nameserver to every appliance, and every
# DNS lookup on the user's network fails. systemd-resolved owns this at runtime.
ln -sfn /run/systemd/resolve/stub-resolv.conf "${ROOTFS}/etc/resolv.conf"

: > "${ROOTFS}/etc/machine-id"          # regenerated per machine on first boot
rm -f "${ROOTFS}"/etc/ssh/ssh_host_*    # regenerated per machine on first boot
find "${ROOTFS}/var/log" -type f -delete 2>/dev/null || true

step "Units this image will start"
# Cheap, and it answers directly the question that otherwise costs a 50-minute
# boot to ask: is the thing that is not running actually enabled?
in_chroot "systemctl list-unit-files --state=enabled --no-legend --no-pager" 2>/dev/null \
    | awk '{print "  " $1}' | sort || true

cleanup
printf '\n\033[32m[ok]\033[0m rootfs ready: %s (%s)\n' \
    "$ROOTFS" "$(du -sh "$ROOTFS" | cut -f1)"
