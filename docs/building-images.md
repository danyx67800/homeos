# Building appliance images

HomeOS ships two artefacts per architecture:

| Artefact | What it is | Size |
|---|---|---|
| `homeos-<version>-<arch>.img.xz` | a disk image you flash | ~400 MB |
| `homeos-<version>-<arch>.iso` | a bootable installer | ~640 MB |

Both are built by [`.github/workflows/image.yml`](../.github/workflows/image.yml)
on native `amd64` and `arm64` runners, in about nine minutes each.

---

## How to get one

**From CI**, which is the normal way:

```bash
gh workflow run image.yml -f version=1.0.0
gh workflow run image.yml -f version=1.0.0 -f publish=true   # also draft a release
```

Pushing a `v*` tag builds and drafts a release automatically.

**Locally**, on a Linux machine with root — `debootstrap`, loop devices and
`chroot` have no equivalent on macOS or Windows:

```bash
sudo apt install debootstrap squashfs-tools xorriso grub-common \
                 grub-pc-bin grub-efi-amd64-bin mtools gdisk rsync

make -C backend build-all          # dist/homeos-core-linux-{amd64,arm64}
make -C web build                  # web/dist/

sudo image/build-rootfs.sh amd64 /tmp/rootfs
sudo image/build-disk.sh   amd64 /tmp/rootfs dist/homeos.img
xz -T0 dist/homeos.img
sudo image/build-iso.sh    amd64 dist/homeos.img.xz dist/homeos.iso
```

---

## Which one do people want?

**The `.img.xz`** if they have another computer: write it to the appliance's
disk with Raspberry Pi Imager, balenaEtcher or `dd`, put the disk back, boot.
It is also the only option on a Raspberry Pi.

**The `.iso`** if the appliance is the only machine: write it to a USB stick,
boot from it, pick a disk, remove the stick.

```bash
xzcat homeos-1.0.0-amd64.img.xz | sudo dd of=/dev/sdX bs=4M conv=fsync status=progress
```

Either way the first boot grows the filesystem to fill whatever disk it landed
on, generates unique SSH host keys and a unique machine-id, and brings the
dashboard up at `http://homenas.local`.

---

## What the pipeline actually does

```
  backend  ──> homeos-core-linux-<arch>  ─┐
  web      ──> web/dist/                 ─┤
                                          ├─> build-rootfs.sh ──> a Debian tree
                                          │      (debootstrap + install.sh
                                          │       --image-build, in a chroot)
                                          │
                        ┌─────────────────┴──────────────┐
                        ▼                                ▼
                 build-disk.sh                     build-iso.sh
                        │                                │
                   .img ──> xz ──> .img.xz ─────────────>│  (carried inside)
                                                         ▼
                                                       .iso
```

Three decisions are worth knowing about.

### The image is built by install.sh, not by a second implementation

`build-rootfs.sh` runs `install.sh --image-build` inside the chroot. The image
is configured by exactly the code that configures a machine somebody installs by
hand, so there is no parallel path to drift out of step.

`--image-build` changes only what a chroot makes impossible: nothing is started,
the runtime checks are skipped, and the container network is deferred. The
checks that were measuring the build host rather than the image — free disk, RAM,
listening ports, the running kernel — are skipped too, because `uname` is not
namespaced and reporting the runner's kernel while building a Debian image is
simply untrue.

### The ISO's live system does not contain HomeOS

It is a small Debian whose only job is to write the `.img` to a disk — the same
artefact people flash by hand. The ISO therefore cannot install something subtly
different from what was built and tested, because it does not know how to
install anything at all.

The installer refuses to offer the medium it booted from, and asks for the
device path to be typed out rather than accepting a `y`. The `.img.xz` sits on
the ISO outside the squashfs: compressing an xz stream again gains nothing.

### One root partition, not two

A plain GPT: an ESP and one ext4 root. No A/B pair, because HomeOS updates by
swapping a symlink inside the filesystem — a second root would buy nothing and
cost the ability to grow the one that exists.

`homeos-firstboot`, written in phase 1, already grows the filesystem and
regenerates the host identity, which is exactly what a flashed image needs.

---

## What has and has not been verified

CI builds both architectures and checks the results: the xz stream decompresses,
and `file` reports the ISO as `ISO 9660 CD-ROM filesystem data (DOS/MBR boot
sector) (bootable)` — a hybrid image that boots under BIOS and UEFI.

**Nobody has booted one.** A build that produces a well-formed bootable ISO can
still fail on real firmware, and the installer has not been run against a real
disk. Try the ISO in a VM before trusting it with hardware:

```bash
qemu-system-x86_64 -m 2048 -cdrom homeos-1.0.0-amd64.iso \
    -drive file=test.qcow2,format=qcow2 -boot d \
    -bios /usr/share/ovmf/OVMF.fd          # UEFI; drop it to test legacy BIOS
```

---

## Notes

- **Raspberry Pi.** The arm64 image boots via UEFI, which covers ARM servers and
  boards with UEFI firmware. A Pi needs `raspi-firmware` and its own boot
  partition layout; that is a separate target, not a flag on this one.
- **Size.** The compressed image is ~400 MB, most of it the kernel, Docker and
  Samba. It is comparable to what Umbrel and Zima ship.
- **Release assets, not artifacts.** A GitHub Actions artifact is the wrong home
  for a 640 MB file; the workflow attaches images to a draft release and keeps
  only the checksums as artifacts.
