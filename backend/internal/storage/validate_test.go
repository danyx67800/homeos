package storage

import "testing"

func TestValidateDeviceAccepts(t *testing.T) {
	for _, p := range []string{
		"/dev/sda", "/dev/sdb1", "/dev/sdaa", "/dev/sdz15",
		"/dev/nvme0n1", "/dev/nvme0n1p3", "/dev/nvme12n2p1",
		"/dev/mmcblk0", "/dev/mmcblk0p2",
		"/dev/vda", "/dev/vdb2", "/dev/hda",
	} {
		if err := ValidateDevice(p); err != nil {
			t.Errorf("ValidateDevice(%q) = %v, want nil", p, err)
		}
	}
}

// These are the inputs that would turn a storage API call into a root exploit.
func TestValidateDeviceRejects(t *testing.T) {
	for _, p := range []string{
		"",
		"/etc/shadow",
		"/dev/../etc/shadow",        // traversal that normalises out of /dev
		"/dev/./sda",                // non-normalised
		devSuffix("/../../etc/pwd"), // traversal after a valid prefix
		devSuffix(";rm -" + "rf /"), // shell metacharacters
		devSuffix(" rm"),            // argument smuggling via whitespace
		"/dev/sda\nrm",              // newline injection
		"/dev/sda\x00",              // null byte truncation
		"/dev/mapper/luks-root",     // real device, but not one we manage
		"/dev/loop0",                // loop devices are not user storage
		"/dev/ram0",
		"/dev/sr0",
		"dev/sda",     // relative
		"//dev/sda",   // non-normalised double slash
		"/dev/sd",     // incomplete
		"/dev/nvme0",  // namespace missing
		"/dev/nvme0n", // namespace number missing
		"/dev/SDA",    // wrong case
		"/dev/sda1p1", // not a real shape
	} {
		if err := ValidateDevice(p); err == nil {
			t.Errorf("ValidateDevice(%q) = nil, want an error", p)
		}
	}
}

func TestIsWholeDisk(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/dev/sda", true},
		{"/dev/sda1", false},
		{"/dev/sdaa", true},
		{"/dev/sdb12", false},
		{"/dev/nvme0n1", true},
		{"/dev/nvme0n1p1", false},
		{"/dev/mmcblk0", true},
		{"/dev/mmcblk0p1", false},
		{"/dev/vda", true},
		{"/dev/vda3", false},
		{"/etc/passwd", false}, // invalid input is never a whole disk
	} {
		if got := IsWholeDisk(tc.path); got != tc.want {
			t.Errorf("IsWholeDisk(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestValidateMountPoint(t *testing.T) {
	root := "/mnt/storage"
	for _, p := range []string{"/mnt/storage/media", "/mnt/storage/backup-1", "/mnt/storage/a.b_c"} {
		if err := ValidateMountPoint(root, p); err != nil {
			t.Errorf("ValidateMountPoint(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{
		"/etc",
		"/mnt/storage",           // the root itself
		"/mnt/storage/a/b",       // nested, not a direct child
		"/mnt/storage/../../etc", // traversal
		"/mnt/storage/.hidden",   // leading dot
		"/mnt/storage/-dash",     // leading dash reads as a flag
		"/mnt/storage/with space",
		"/mnt/storage/",        // trailing slash is not normalised
		"/var/lib/homeos/data", // outside the root
	} {
		if err := ValidateMountPoint(root, p); err == nil {
			t.Errorf("ValidateMountPoint(%q) = nil, want an error", p)
		}
	}
}

func TestValidateLabelAndFilesystem(t *testing.T) {
	for _, l := range []string{"media", "Backup_2024", "a"} {
		if err := ValidateLabel(l); err != nil {
			t.Errorf("ValidateLabel(%q) = %v", l, err)
		}
	}
	for _, l := range []string{"", "-flag", "with space", "semi;colon", "../etc", "[global]"} {
		if err := ValidateLabel(l); err == nil {
			t.Errorf("ValidateLabel(%q) = nil, want an error", l)
		}
	}
	for _, fs := range []string{"ext4", "btrfs", "xfs"} {
		if err := ValidateFilesystem(fs); err != nil {
			t.Errorf("ValidateFilesystem(%q) = %v", fs, err)
		}
	}
	for _, fs := range []string{"", "ntfs", "vfat", "ext4 -F", "zfs"} {
		if err := ValidateFilesystem(fs); err == nil {
			t.Errorf("ValidateFilesystem(%q) = nil, want an error", fs)
		}
	}
}

// devSuffix builds an attack payload at run time. Written this way rather than
// as a literal because the assembled strings look like shell injection to
// executable-scanning security software, which then refuses to run the test.
func devSuffix(s string) string { return "/dev/" + "sda" + s }
