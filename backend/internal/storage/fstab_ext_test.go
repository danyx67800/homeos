package storage_test

import (
	"strings"
	"testing"

	"github.com/danyx67800/homeos/backend/internal/storage"
)

const operatorFstab = `# /etc/fstab: static file system information.
UUID=aaaa-1111 /               ext4    errors=remount-ro 0 1
UUID=bbbb-2222 /boot/efi       vfat    umask=0077        0 1
/swapfile      none            swap    sw                0 0
`

func TestRenderFstabPreservesOperatorLines(t *testing.T) {
	out := storage.RenderFstabForTest([]byte(operatorFstab), []storage.FstabEntryForTest{
		{UUID: "cccc-3333", Target: "/mnt/storage/media", Fstype: "ext4"},
	})
	s := string(out)

	for _, must := range []string{
		"UUID=aaaa-1111 /               ext4",
		"UUID=bbbb-2222 /boot/efi",
		"/swapfile      none            swap",
	} {
		if !strings.Contains(s, must) {
			t.Errorf("operator line lost: %q\n---\n%s", must, s)
		}
	}
	if !strings.Contains(s, "UUID=cccc-3333\t/mnt/storage/media\text4") {
		t.Errorf("new entry missing:\n%s", s)
	}
	// nofail is the difference between "a USB disk is unplugged" and "the box
	// will not boot".
	if !strings.Contains(s, "nofail") {
		t.Error("nofail option missing from a generated entry")
	}
	if !strings.Contains(s, "x-systemd.device-timeout=10") {
		t.Error("device-timeout missing; boot would hang on an absent disk")
	}
}

// Repeated writes must converge, not accumulate blocks.
func TestRenderFstabIsIdempotent(t *testing.T) {
	entries := []storage.FstabEntryForTest{
		{UUID: "cccc-3333", Target: "/mnt/storage/media", Fstype: "ext4"},
	}
	once := storage.RenderFstabForTest([]byte(operatorFstab), entries)
	twice := storage.RenderFstabForTest(once, entries)
	thrice := storage.RenderFstabForTest(twice, entries)

	if string(twice) != string(thrice) {
		t.Errorf("not idempotent:\n--- twice ---\n%s\n--- thrice ---\n%s", twice, thrice)
	}
	if n := strings.Count(string(thrice), storage.FstabBeginForTest); n != 1 {
		t.Errorf("got %d managed blocks, want exactly 1", n)
	}
	if n := strings.Count(string(thrice), "UUID=cccc-3333"); n != 1 {
		t.Errorf("entry duplicated %d times", n)
	}
}

func TestRenderFstabRemovesEntries(t *testing.T) {
	with := storage.RenderFstabForTest([]byte(operatorFstab), []storage.FstabEntryForTest{
		{UUID: "cccc-3333", Target: "/mnt/storage/media", Fstype: "ext4"},
		{UUID: "dddd-4444", Target: "/mnt/storage/backup", Fstype: "btrfs"},
	})
	if got := len(storage.ParseManagedEntriesForTest(with)); got != 2 {
		t.Fatalf("round trip lost entries: %d", got)
	}

	without := storage.RenderFstabForTest(with, []storage.FstabEntryForTest{
		{UUID: "cccc-3333", Target: "/mnt/storage/media", Fstype: "ext4"},
	})
	if strings.Contains(string(without), "dddd-4444") {
		t.Error("removed entry still present")
	}
	if !strings.Contains(string(without), "cccc-3333") {
		t.Error("surviving entry was dropped")
	}
	if !strings.Contains(string(without), "UUID=aaaa-1111") {
		t.Error("operator lines lost on removal")
	}
}

// Entries are sorted so two runs with the same set produce identical bytes,
// which keeps the file out of diffs and backups for no reason.
func TestRenderFstabSortsEntries(t *testing.T) {
	a := storage.RenderFstabForTest(nil, []storage.FstabEntryForTest{
		{UUID: "2", Target: "/mnt/storage/zzz", Fstype: "ext4"},
		{UUID: "1", Target: "/mnt/storage/aaa", Fstype: "ext4"},
	})
	b := storage.RenderFstabForTest(nil, []storage.FstabEntryForTest{
		{UUID: "1", Target: "/mnt/storage/aaa", Fstype: "ext4"},
		{UUID: "2", Target: "/mnt/storage/zzz", Fstype: "ext4"},
	})
	if string(a) != string(b) {
		t.Error("entry order changes the output")
	}
	if strings.Index(string(a), "/mnt/storage/aaa") > strings.Index(string(a), "/mnt/storage/zzz") {
		t.Error("entries are not sorted by target")
	}
}

func TestRenderFstabOnEmptyFile(t *testing.T) {
	out := storage.RenderFstabForTest(nil, []storage.FstabEntryForTest{
		{UUID: "eeee-5555", Target: "/mnt/storage/data", Fstype: "xfs"},
	})
	if !strings.Contains(string(out), "UUID=eeee-5555") {
		t.Errorf("empty fstab not handled:\n%s", out)
	}
}
