// Package storage enumerates block devices, reads SMART health, and formats,
// mounts and shares them.
//
// Everything privileged in here runs through sudo against the allowlist that
// install.sh puts in /etc/sudoers.d/homeos. Commands are always built as an
// explicit argv and never handed to a shell, so there is no word-splitting or
// metacharacter interpretation to defeat.
//
// That still leaves the arguments themselves. `mkfs.ext4 /etc/shadow` needs no
// shell to be catastrophic, so every device path is validated against a strict
// pattern before it reaches a command. Validation is the security boundary
// here; treat any new privileged call site as needing it.
package storage

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	ErrInvalidDevice = errors.New("invalid device path")
	ErrInvalidMount  = errors.New("invalid mount point")
	ErrInvalidLabel  = errors.New("invalid label")
)

// Whole disks and their partitions, as Linux names them:
//
//	/dev/sda, /dev/sda1          SCSI/SATA/USB
//	/dev/nvme0n1, /dev/nvme0n1p2 NVMe
//	/dev/mmcblk0, /dev/mmcblk0p1 SD/eMMC
//	/dev/vda, /dev/vda1          virtio
//	/dev/hda                     legacy IDE
var deviceRE = regexp.MustCompile(
	`^/dev/(?:` +
		`sd[a-z]{1,2}[0-9]{0,3}` +
		`|nvme[0-9]{1,3}n[0-9]{1,3}(?:p[0-9]{1,3})?` +
		`|mmcblk[0-9]{1,3}(?:p[0-9]{1,3})?` +
		`|vd[a-z]{1,2}[0-9]{0,3}` +
		`|hd[a-z]{1,2}[0-9]{0,3}` +
		`)$`)

// ValidateDevice accepts only a literal block-device path in /dev.
//
// path.Clean is applied first so that "/dev/../etc/shadow" is normalised to
// "/etc/shadow" and then rejected by the pattern, rather than sneaking through
// as a string that merely starts with "/dev/".
func ValidateDevice(devPath string) error {
	if devPath == "" {
		return fmt.Errorf("%w: empty", ErrInvalidDevice)
	}
	// Reject before cleaning too, so the error message names the real input.
	if strings.ContainsAny(devPath, "\x00\n\r\t ") {
		return fmt.Errorf("%w: %q contains whitespace or a null byte", ErrInvalidDevice, devPath)
	}
	clean := path.Clean(devPath)
	if clean != devPath {
		return fmt.Errorf("%w: %q is not a normalised path", ErrInvalidDevice, devPath)
	}
	if !deviceRE.MatchString(clean) {
		return fmt.Errorf("%w: %q is not a recognised block device", ErrInvalidDevice, devPath)
	}
	return nil
}

// IsWholeDisk reports whether the path names a disk rather than a partition.
// Formatting or partitioning must target a whole disk; mounting must not.
func IsWholeDisk(devPath string) bool {
	if ValidateDevice(devPath) != nil {
		return false
	}
	base := strings.TrimPrefix(devPath, "/dev/")
	switch {
	case strings.HasPrefix(base, "nvme"), strings.HasPrefix(base, "mmcblk"):
		// Partitions carry a "p<N>" suffix; the disk itself does not.
		return !strings.Contains(base[strings.LastIndex(base, "n")+1:], "p")
	default:
		// sd/vd/hd partitions end in digits.
		return !strings.ContainsAny(base[len(base)-1:], "0123456789")
	}
}

// mountRE keeps mount points inside the storage root and free of traversal.
var mountNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateMountPoint confines a mount to a direct child of root, so a caller
// cannot mount over /etc or bind something outside the storage tree.
func ValidateMountPoint(root, mountPath string) error {
	clean := path.Clean(mountPath)
	if clean != mountPath {
		return fmt.Errorf("%w: %q is not normalised", ErrInvalidMount, mountPath)
	}
	parent, name := path.Split(clean)
	if path.Clean(parent) != path.Clean(root) {
		return fmt.Errorf("%w: %q must be directly under %s", ErrInvalidMount, mountPath, root)
	}
	if !mountNameRE.MatchString(name) {
		return fmt.Errorf("%w: %q is not a usable directory name", ErrInvalidMount, name)
	}
	return nil
}

// ValidateLabel guards filesystem and share labels, which end up in mkfs
// arguments and in smb.conf section headers.
func ValidateLabel(label string) error {
	if !mountNameRE.MatchString(label) {
		return fmt.Errorf("%w: %q must be 1-64 chars of letters, digits, dot, dash or underscore",
			ErrInvalidLabel, label)
	}
	return nil
}

// ValidateFilesystem restricts mkfs to the types HomeOS actually supports.
func ValidateFilesystem(fs string) error {
	switch fs {
	case "ext4", "btrfs", "xfs":
		return nil
	}
	return fmt.Errorf("unsupported filesystem %q (want ext4, btrfs or xfs)", fs)
}
