package storage

import (
	"context"
	"fmt"
	"strings"
)

// PartitionPath returns the first partition of a disk, following the kernel's
// two naming conventions: sd/vd/hd append the number directly, nvme and mmcblk
// insert a "p" first (because "nvme0n11" would otherwise be ambiguous).
func PartitionPath(disk string, n int) string {
	base := strings.TrimPrefix(disk, "/dev/")
	if strings.HasPrefix(base, "nvme") || strings.HasPrefix(base, "mmcblk") {
		return fmt.Sprintf("%sp%d", disk, n)
	}
	return fmt.Sprintf("%s%d", disk, n)
}

// FormatRequest describes a destructive whole-disk format.
type FormatRequest struct {
	Device     string `json:"device"`
	Filesystem string `json:"filesystem"`
	Label      string `json:"label"`
	// Confirm must equal the device path. The API refuses otherwise: this is
	// the one operation that silently destroys every byte on a disk, and a
	// mistyped device name in a JSON body should not be enough to trigger it.
	Confirm string `json:"confirm"`
}

// Format writes a fresh GPT with a single full-disk partition and makes a
// filesystem on it. It returns the partition path.
//
// A partition table rather than a bare filesystem on the raw disk: partitioned
// disks are what every other OS expects, so the drive stays readable if it is
// ever moved out of the appliance.
func (m *Manager) Format(ctx context.Context, req FormatRequest) (string, error) {
	if err := ValidateDevice(req.Device); err != nil {
		return "", err
	}
	if !IsWholeDisk(req.Device) {
		return "", fmt.Errorf("%w: %s is a partition; format targets a whole disk",
			ErrInvalidDevice, req.Device)
	}
	fs := req.Filesystem
	if fs == "" {
		fs = m.defaultFS
	}
	if err := ValidateFilesystem(fs); err != nil {
		return "", err
	}
	if req.Label != "" {
		if err := ValidateLabel(req.Label); err != nil {
			return "", err
		}
	}
	if req.Confirm != req.Device {
		return "", fmt.Errorf("refusing to format %s: confirm must repeat the device path",
			req.Device)
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()

	// Re-check under the lock, against live state rather than a cached list.
	devs, err := m.List(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range devs {
		if d.Path != req.Device {
			continue
		}
		if d.InUse {
			return "", fmt.Errorf("refusing to format %s: it has a mounted filesystem", req.Device)
		}
	}

	m.log.Warn("formatting disk", "device", req.Device, "filesystem", fs, "label", req.Label)

	// Old signatures confuse the kernel and udev after repartitioning.
	if _, err := m.priv.Run(ctx, "wipefs", "-a", req.Device); err != nil {
		return "", fmt.Errorf("wipe signatures on %s: %w", req.Device, err)
	}
	if _, err := m.priv.Run(ctx, "sgdisk", "--zap-all", req.Device); err != nil {
		return "", fmt.Errorf("clear partition table on %s: %w", req.Device, err)
	}
	// 8300 = Linux filesystem. 0:0 means "first usable sector to last".
	if _, err := m.priv.Run(ctx, "sgdisk", "-n", "1:0:0", "-t", "1:8300", req.Device); err != nil {
		return "", fmt.Errorf("create partition on %s: %w", req.Device, err)
	}
	// Without this the partition node may not exist yet when mkfs runs.
	if _, err := m.priv.Run(ctx, "partprobe", req.Device); err != nil {
		m.log.Warn("partprobe failed; continuing", "device", req.Device, "error", err)
	}

	part := PartitionPath(req.Device, 1)
	if err := m.mkfs(ctx, part, fs, req.Label); err != nil {
		return "", err
	}
	m.log.Info("disk formatted", "device", req.Device, "partition", part, "filesystem", fs)
	return part, nil
}

// mkfs builds the argv per filesystem. The label flag differs between them,
// which is the only reason this is not one generic call.
func (m *Manager) mkfs(ctx context.Context, part, fs, label string) error {
	var args []string
	switch fs {
	case "ext4":
		// -F because the partition node was just created and mkfs would
		// otherwise prompt about it not being a block special device yet.
		args = []string{"-F"}
		if label != "" {
			args = append(args, "-L", label)
		}
	case "xfs":
		args = []string{"-f"}
		if label != "" {
			args = append(args, "-L", label)
		}
	case "btrfs":
		args = []string{"-f"}
		if label != "" {
			args = append(args, "-L", label)
		}
	default:
		return fmt.Errorf("unsupported filesystem %q", fs)
	}
	args = append(args, part)

	if _, err := m.priv.Run(ctx, "mkfs."+fs, args...); err != nil {
		return fmt.Errorf("create %s filesystem on %s: %w", fs, part, err)
	}
	return nil
}
