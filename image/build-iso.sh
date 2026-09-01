#!/usr/bin/env bash
#
# build-iso.sh — build the HomeOS installer ISO.
#
#   sudo image/build-iso.sh <arch> <homeos.img.xz> <output.iso>
#
# The live system deliberately does not contain HomeOS. It is a small Debian
# that boots, and its only job is to write the .img to a disk — the same image
# people flash by hand. That means the ISO cannot install something subtly
# different from what was built and tested, because it does not know how to
# install anything at all.
#
# The .img.xz sits on the ISO outside the squashfs: compressing an xz stream
# again gains nothing and costs build time.
#
set -euo pipefail

ARCH="${1:-amd64}"
PAYLOAD="${2:-}"
OUT="${3:-}"
SUITE="${HOMEOS_SUITE:-bookworm}"
MIRROR="${HOMEOS_MIRROR:-http://deb.debian.org/debian}"
REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${HOMEOS_VERSION:-dev}"

step() { printf '\n\033[34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[31m[XX]\033[0m %s\n' "$*" >&2; exit 1; }

[[ "${EUID:-$(id -u)}" -eq 0 ]] || die "run with sudo"
[[ -f "$PAYLOAD" && -n "$OUT" ]] || die "usage: build-iso.sh <arch> <image.img.xz> <out.iso>"
for t in debootstrap mksquashfs xorriso grub-mkrescue; do
    command -v "$t" >/dev/null || die "$t is required (apt install squashfs-tools xorriso grub-common)"
done

WORK="$(mktemp -d)"
LIVE="${WORK}/live-rootfs"
ISO="${WORK}/iso"
cleanup() {
    for m in dev/pts dev proc sys; do
        mountpoint -q "${LIVE}/${m}" && umount -lf "${LIVE}/${m}" || true
    done
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

# --------------------------------------------------------------------------
# A minimal live system
# --------------------------------------------------------------------------
step "Bootstrapping the live system"
debootstrap --arch="$ARCH" --variant=minbase \
    --include=systemd,systemd-sysv,dbus,ca-certificates \
    "$SUITE" "$LIVE" "$MIRROR" >/dev/null 2>&1 \
    || die "debootstrap failed"

mount -t proc  proc   "${LIVE}/proc"
mount -t sysfs sysfs  "${LIVE}/sys"
mount --bind /dev     "${LIVE}/dev"
mount -t devpts devpts "${LIVE}/dev/pts"
cp /etc/resolv.conf "${LIVE}/etc/resolv.conf"

cat > "${LIVE}/usr/sbin/policy-rc.d" <<'POLICY'
#!/bin/sh
exit 101
POLICY
chmod 0755 "${LIVE}/usr/sbin/policy-rc.d"

in_live() { chroot "$LIVE" /usr/bin/env -i \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    HOME=/root TERM=dumb DEBIAN_FRONTEND=noninteractive \
    /bin/bash -c "$*"; }

step "Installing the live tooling"
case "$ARCH" in
    amd64) KERNEL=linux-image-amd64 ;;
    arm64) KERNEL=linux-image-arm64 ;;
    *) die "unsupported arch ${ARCH}" ;;
esac

# live-boot is what makes the squashfs bootable as a root filesystem. The rest
# is what the installer shells out to.
in_live "apt-get update -qq && apt-get install -y -qq --no-install-recommends \
    ${KERNEL} live-boot live-boot-initramfs-tools \
    xz-utils util-linux parted gdisk dosfstools e2fsprogs \
    pv coreutils kbd console-setup" >/dev/null \
    || die "installing the live tooling failed"

# --------------------------------------------------------------------------
# Make it land the user in the installer
# --------------------------------------------------------------------------
step "Wiring the installer"
install -D -m 0755 "${REPO_ROOT}/image/installer/homeos-install" \
    "${LIVE}/usr/local/sbin/homeos-install"

echo "homeos-live" > "${LIVE}/etc/hostname"
in_live "passwd -d root >/dev/null 2>&1" || true

# The installer is the point of the ISO, so it runs on tty1 instead of a login
# prompt. Dropping to a shell is one Ctrl-C away, which is the escape hatch
# anyone debugging needs.
mkdir -p "${LIVE}/etc/systemd/system/getty@tty1.service.d"
cat > "${LIVE}/etc/systemd/system/getty@tty1.service.d/homeos.conf" <<'GETTY'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin root --noclear %I $TERM
GETTY

cat > "${LIVE}/root/.bash_profile" <<'PROFILE'
# Straight into the installer on the console, but only there: an SSH session or
# a second tty gets a normal shell, which is what you want when something has
# gone wrong.
if [ "$(tty)" = "/dev/tty1" ] && [ -z "${HOMEOS_NO_INSTALLER:-}" ]; then
    /usr/local/sbin/homeos-install || {
        echo
        echo "The installer exited. Run 'homeos-install' to try again."
        echo
    }
fi
PROFILE

in_live "systemctl enable systemd-networkd >/dev/null 2>&1" || true

step "Cleaning the live system"
in_live "apt-get clean"
rm -f "${LIVE}/usr/sbin/policy-rc.d" "${LIVE}/etc/resolv.conf"
rm -rf "${LIVE}/var/lib/apt/lists"/* "${LIVE}/tmp"/*

KERNEL_FILE="$(basename "$(ls "${LIVE}"/boot/vmlinuz-* | tail -n1)")"
INITRD_FILE="$(basename "$(ls "${LIVE}"/boot/initrd.img-* | tail -n1)")"
[[ -n "$KERNEL_FILE" && -n "$INITRD_FILE" ]] || die "no kernel in the live system"

for m in dev/pts dev proc sys; do
    mountpoint -q "${LIVE}/${m}" && umount -lf "${LIVE}/${m}" || true
done

# --------------------------------------------------------------------------
# Assemble the ISO tree
# --------------------------------------------------------------------------
step "Building the squashfs"
mkdir -p "${ISO}/live" "${ISO}/boot/grub" "${ISO}/homeos"
mksquashfs "$LIVE" "${ISO}/live/filesystem.squashfs" \
    -comp xz -b 1M -noappend -no-progress >/dev/null \
    || die "mksquashfs failed"

cp "${LIVE}/boot/${KERNEL_FILE}" "${ISO}/live/vmlinuz"
cp "${LIVE}/boot/${INITRD_FILE}" "${ISO}/live/initrd.img"

# Outside the squashfs on purpose: already xz, and the installer reads it
# straight off the medium.
cp "$PAYLOAD" "${ISO}/homeos/"
[[ -f "${PAYLOAD}.sha256" ]] && cp "${PAYLOAD}.sha256" "${ISO}/homeos/"

cat > "${ISO}/boot/grub/grub.cfg" <<GRUBCFG
set default=0
set timeout=10

menuentry "Install HomeOS" {
    linux  /live/vmlinuz boot=live quiet loglevel=3 console=tty0 console=ttyS0,115200
    initrd /live/initrd.img
}

menuentry "Install HomeOS (verbose, for troubleshooting)" {
    linux  /live/vmlinuz boot=live console=tty0 console=ttyS0,115200
    initrd /live/initrd.img
}

menuentry "Live shell, no installer" {
    linux  /live/vmlinuz boot=live quiet HOMEOS_NO_INSTALLER=1 console=tty0
    initrd /live/initrd.img
}
GRUBCFG

printf 'HomeOS %s (%s)\n' "$VERSION" "$ARCH" > "${ISO}/homeos/VERSION"

# --------------------------------------------------------------------------
step "Writing the ISO"
# grub-mkrescue produces a hybrid image that boots from BIOS and UEFI and from
# both optical media and a USB stick. Hand-assembling El Torito and an EFI boot
# image is the alternative, and it is not worth the surface.
grub-mkrescue -o "$OUT" "$ISO" \
    -- -volid "HOMEOS_$(echo "$VERSION" | tr -cd '[:alnum:]' | cut -c1-16)" \
    >/dev/null 2>&1 \
    || die "grub-mkrescue failed"

step "Done"
printf '  %s (%s)\n' "$OUT" "$(du -h "$OUT" | cut -f1)"
