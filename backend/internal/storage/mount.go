package storage

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
)

const fstabPath = "/etc/fstab"

// FstabBegin/End delimit the block HomeOS owns. Everything outside it belongs
// to the operator and is never touched, which is what makes it safe to rewrite
// fstab from a daemon at all.
const (
	fstabBegin = "# >>> homeos managed block >>>"
	fstabEnd   = "# <<< homeos managed block <<<"
)

type MountRequest struct {
	Device string `json:"device"` // partition, not a whole disk
	Name   string `json:"name"`   // directory under the storage root
	// Persist writes an fstab entry so the mount survives a reboot.
	Persist bool `json:"persist"`
}

// Mount attaches a partition under the storage root.
//
// The fstab entry is written by UUID rather than device path: /dev/sdb can
// become /dev/sdc when a USB disk is replugged or the SATA ports enumerate in a
// different order, and a stale path in fstab makes the machine fail to boot.
func (m *Manager) Mount(ctx context.Context, req MountRequest) (string, error) {
	if err := ValidateDevice(req.Device); err != nil {
		return "", err
	}
	if IsWholeDisk(req.Device) {
		// A disk with no partition table can carry a filesystem directly, but
		// accepting that here makes it far too easy to mount the wrong thing.
		return "", fmt.Errorf("%w: mount a partition, not the whole disk %s",
			ErrInvalidDevice, req.Device)
	}
	if err := ValidateLabel(req.Name); err != nil {
		return "", fmt.Errorf("mount name: %w", err)
	}
	target := path.Join(m.mountRoot, req.Name)
	if err := ValidateMountPoint(m.mountRoot, target); err != nil {
		return "", err
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()

	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create mount point %s: %w", target, err)
	}

	if _, err := m.priv.Run(ctx, "mount", req.Device, target); err != nil {
		return "", fmt.Errorf("mount %s at %s: %w", req.Device, target, err)
	}

	if req.Persist {
		uuid, fstype, err := m.blkid(ctx, req.Device)
		if err != nil {
			// The mount succeeded; only persistence failed. Say so rather than
			// unwinding a mount the user asked for.
			m.log.Warn("mounted but not persisted", "device", req.Device, "error", err)
			return target, nil
		}
		if err := m.addFstabEntry(uuid, target, fstype); err != nil {
			m.log.Warn("mounted but fstab not updated", "device", req.Device, "error", err)
		}
	}

	m.log.Info("mounted", "device", req.Device, "target", target, "persist", req.Persist)
	return target, nil
}

func (m *Manager) Unmount(ctx context.Context, name string) error {
	if err := ValidateLabel(name); err != nil {
		return err
	}
	target := path.Join(m.mountRoot, name)
	if err := ValidateMountPoint(m.mountRoot, target); err != nil {
		return err
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()

	if _, err := m.priv.Run(ctx, "umount", target); err != nil {
		return fmt.Errorf("unmount %s: %w", target, err)
	}
	if err := m.removeFstabEntry(target); err != nil {
		m.log.Warn("unmounted but fstab not updated", "target", target, "error", err)
	}
	m.log.Info("unmounted", "target", target)
	return nil
}

// blkid reads the filesystem UUID and type of a partition.
func (m *Manager) blkid(ctx context.Context, devPath string) (uuid, fstype string, err error) {
	out, err := m.priv.Run(ctx, "blkid", "-o", "export", devPath)
	if err != nil {
		return "", "", fmt.Errorf("read filesystem identity of %s: %w", devPath, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "UUID":
			uuid = v
		case "TYPE":
			fstype = v
		}
	}
	if uuid == "" {
		return "", "", fmt.Errorf("%s has no filesystem UUID (is it formatted?)", devPath)
	}
	return uuid, fstype, nil
}
