#!/usr/bin/env bash
#
# build-disk.sh — turn a rootfs into a flashable disk image.
#
#   sudo image/build-disk.sh <arch> <rootfs-dir> <output.img>
#
# The layout is deliberately plain: a GPT with an ESP and one ext4 root. No LVM,
# no A/B partitions. HomeOS updates itself by swapping a symlink inside the
# filesystem, so a second root partition would buy nothing and cost the ability
# to grow the one that exists.
#
# The image is made as small as the content allows and grown on first boot,
# because the compressed artefact people download is what its size actually
# costs them.
#
set -euo pipefail

ARCH="${1:-amd64}"
ROOTFS="${2:-}"
OUT="${3:-}"
ESP_MB="${HOMEOS_ESP_MB:-256}"
SLACK_MB="${HOMEOS_SLACK_MB:-512}"   # headroom so the first boot has room to work

step() { printf '\n\033[34m==>\033[0m %s\n' "$*"; }
die()  { printf '\033[31m[XX]\033[0m %s\n' "$*" >&2; exit 1; }

[[ "${EUID:-$(id -u)}" -eq 0 ]] || die "run with sudo: loop devices need root"
[[ -d "$ROOTFS" && -n "$OUT" ]] || die "usage: build-disk.sh <arch> <rootfs> <out.img>"
for t in sgdisk mkfs.vfat mkfs.ext4 losetup rsync; do
    command -v "$t" >/dev/null || die "$t is required"
done

# --------------------------------------------------------------------------
# Size the image from what is actually in it
# --------------------------------------------------------------------------
ROOT_MB=$(( $(du -sm --apparent-size "$ROOTFS" | cut -f1) + SLACK_MB ))
TOTAL_MB=$(( 1 + ESP_MB + ROOT_MB ))
step "Creating a ${TOTAL_MB} MiB image (root ${ROOT_MB} MiB)"

rm -f "$OUT"
truncate -s "${TOTAL_MB}M" "$OUT"

# --------------------------------------------------------------------------
# Partition
# --------------------------------------------------------------------------
sgdisk --clear \
    --new=1:2048:+${ESP_MB}M --typecode=1:ef00 --change-name=1:ESP \
    --new=2:0:0              --typecode=2:8300 --change-name=2:homeos-root \
    "$OUT" >/dev/null || die "partitioning failed"

# A BIOS boot partition is not used; amd64 boots via the ESP in both UEFI and
# legacy mode because GRUB is installed with --removable into EFI/BOOT.

LOOP="$(losetup --find --show --partscan "$OUT")" || die "losetup failed"
cleanup() {
    sync
    for m in "${MNT}/boot/efi" "${MNT}/dev/pts" "${MNT}/dev" "${MNT}/proc" "${MNT}/sys" "${MNT}"; do
        mountpoint -q "$m" && umount -lf "$m" || true
    done
    [[ -n "${LOOP:-}" ]] && losetup -d "$LOOP" 2>/dev/null || true
    [[ -n "${MNT:-}" ]] && rmdir "$MNT" 2>/dev/null || true
}
MNT="$(mktemp -d)"
trap cleanup EXIT INT TERM

# --partscan is asynchronous; the nodes may not exist for a moment.
for _ in 1 2 3 4 5 6 7 8 9 10; do
    [[ -b "${LOOP}p2" ]] && break
    sleep 1
done
[[ -b "${LOOP}p2" ]] || die "partition devices never appeared for ${LOOP}"

step "Formatting"
mkfs.vfat -F32 -n HOMEOS-ESP "${LOOP}p1" >/dev/null || die "mkfs.vfat failed"
# ^ metadata_csum_seed keeps the UUID stable across resize2fs on first boot.
mkfs.ext4 -q -L homeos-root -O ^has_journal,^metadata_csum_seed -E lazy_itable_init=0 \
    "${LOOP}p2" 2>/dev/null \
    || mkfs.ext4 -q -L homeos-root "${LOOP}p2" \
    || die "mkfs.ext4 failed"
# The journal is put back now that the filesystem exists; creating without it
# and adding it after is measurably faster on a CI runner's slow loop device.
tune2fs -O has_journal "${LOOP}p2" >/dev/null 2>&1 || true

ROOT_UUID="$(blkid -s UUID -o value "${LOOP}p2")"
ESP_UUID="$(blkid -s UUID -o value "${LOOP}p1")"
[[ -n "$ROOT_UUID" && -n "$ESP_UUID" ]] || die "could not read the new filesystem UUIDs"

# --------------------------------------------------------------------------
# Populate
# --------------------------------------------------------------------------
step "Copying the root filesystem"
mount "${LOOP}p2" "$MNT"
mkdir -p "${MNT}/boot/efi"
rsync -aHAX --numeric-ids "${ROOTFS}/" "${MNT}/" || die "rsync failed"
mount "${LOOP}p1" "${MNT}/boot/efi"

# fstab by UUID: the disk this is flashed to will not be the loop device it was
# built on, and a device path here would make the image unbootable everywhere.
cat > "${MNT}/etc/fstab" <<FSTAB
# Written by build-disk.sh. Mounted by UUID because the image is flashed to a
# disk whose device name is not known until it boots.
UUID=${ROOT_UUID}  /          ext4  errors=remount-ro,noatime  0 1
UUID=${ESP_UUID}   /boot/efi  vfat  umask=0077,nofail          0 2
FSTAB

# --------------------------------------------------------------------------
# Bootloader
# --------------------------------------------------------------------------
step "Installing GRUB"
mount -t proc  proc   "${MNT}/proc"
mount -t sysfs sysfs  "${MNT}/sys"
mount --bind /dev     "${MNT}/dev"
mount -t devpts devpts "${MNT}/dev/pts"
# The rootfs ships /etc/resolv.conf as a symlink to systemd-resolved's stub, so
# a plain cp resolves both sides to the same path and refuses. Replace it for
# the duration of the chroot, and put the symlink back before closing up.
rm -f "${MNT}/etc/resolv.conf"
cp -L /etc/resolv.conf "${MNT}/etc/resolv.conf"

in_img() { chroot "$MNT" /usr/bin/env -i \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    HOME=/root TERM=dumb DEBIAN_FRONTEND=noninteractive \
    /bin/bash -c "$*"; }

case "$ARCH" in
    amd64) GRUB_PKGS="grub-efi-amd64 grub-efi-amd64-bin grub-pc-bin efibootmgr"; GRUB_TARGET="x86_64-efi" ;;
    arm64) GRUB_PKGS="grub-efi-arm64 grub-efi-arm64-bin efibootmgr";             GRUB_TARGET="arm64-efi"  ;;
    *) die "unsupported arch ${ARCH}" ;;
esac

in_img "apt-get update -qq && apt-get install -y -qq --no-install-recommends ${GRUB_PKGS}" \
    || die "installing GRUB failed"

# --removable writes EFI/BOOT/BOOTX64.EFI, the path firmware falls back to when
# it has no NVRAM entry — which is always true for a freshly flashed disk.
in_img "grub-install --target=${GRUB_TARGET} --efi-directory=/boot/efi \
        --bootloader-id=HomeOS --removable --no-nvram" \
    || die "grub-install (EFI) failed"

if [[ "$ARCH" == "amd64" ]]; then
    # Also write a legacy MBR stage, so the image boots on machines whose
    # firmware has no UEFI at all. Failure here is not fatal: UEFI still works.
    in_img "grub-install --target=i386-pc --boot-directory=/boot ${LOOP}" \
        >/dev/null 2>&1 || printf '  legacy BIOS boot not installed (UEFI only)\n'
fi

cat > "${MNT}/etc/default/grub" <<'GRUBCFG'
GRUB_DISTRIBUTOR="HomeOS"
GRUB_DEFAULT=0
# Short, but not zero: a headless appliance still needs a window in which
# somebody can pick recovery after a bad update.
GRUB_TIMEOUT=3
GRUB_TIMEOUT_STYLE=menu
# console= twice on purpose: serial first for headless boards, then VGA, so
# whichever exists gets the output.
GRUB_CMDLINE_LINUX_DEFAULT="loglevel=4 systemd.show_status=yes console=ttyS0,115200 console=tty0"
GRUB_CMDLINE_LINUX=""
GRUB_DISABLE_RECOVERY=false
GRUBCFG

in_img "update-grub" >/dev/null 2>&1 || die "update-grub failed"
in_img "update-initramfs -u -k all" >/dev/null 2>&1 || true

# The loop device must not survive into the image's own GRUB config.
sed -i "s|${LOOP}p2|UUID=${ROOT_UUID}|g" "${MNT}/boot/grub/grub.cfg" 2>/dev/null || true

# Back to what the appliance should ship with.
ln -sfn /run/systemd/resolve/stub-resolv.conf "${MNT}/etc/resolv.conf"
cleanup
trap - EXIT INT TERM

step "Done"
printf '  %s (%s)\n' "$OUT" "$(du -h "$OUT" | cut -f1)"
