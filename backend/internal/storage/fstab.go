package storage

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
)

type fstabEntry struct {
	UUID   string
	Target string
	Fstype string
}

// mountOptions are deliberate, and the first two matter a great deal on an
// appliance:
//
//	nofail                  a USB disk that is unplugged at boot must not drop
//	                        the machine into an emergency shell
//	x-systemd.device-timeout  bounds how long boot waits for a missing disk
//	noatime                 avoids a write for every read, which on a NAS is
//	                        both pointless and hard on flash
const mountOptions = "defaults,noatime,nofail,x-systemd.device-timeout=10"

// renderFstab replaces the HomeOS-owned block inside an existing fstab, leaving
// every operator-written line untouched. Split out as a pure function because
// getting this wrong makes a machine unbootable.
func renderFstab(existing []byte, entries []fstabEntry) []byte {
	var kept []string
	inBlock := false

	sc := bufio.NewScanner(bytes.NewReader(existing))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, fstabBegin):
			inBlock = true
		case strings.HasPrefix(line, fstabEnd):
			inBlock = false
		case !inBlock:
			kept = append(kept, line)
		}
	}
	// Trim trailing blank lines left where the old block was.
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}

	sorted := append([]fstabEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Target < sorted[j].Target })

	var b bytes.Buffer
	for _, l := range kept {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(fstabBegin + "\n")
	b.WriteString("# Written by homeos-core. Edit mounts through the HomeOS storage\n")
	b.WriteString("# manager; changes inside this block are overwritten.\n")
	for _, e := range sorted {
		fstype := e.Fstype
		if fstype == "" {
			fstype = "auto"
		}
		fmt.Fprintf(&b, "UUID=%s\t%s\t%s\t%s\t0\t2\n", e.UUID, e.Target, fstype, mountOptions)
	}
	b.WriteString(fstabEnd + "\n")
	return b.Bytes()
}

// parseManagedEntries returns the entries currently inside the HomeOS block.
func parseManagedEntries(existing []byte) []fstabEntry {
	var out []fstabEntry
	inBlock := false
	sc := bufio.NewScanner(bytes.NewReader(existing))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, fstabBegin):
			inBlock = true
			continue
		case strings.HasPrefix(line, fstabEnd):
			inBlock = false
			continue
		}
		if !inBlock || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[0], "UUID=") {
			continue
		}
		out = append(out, fstabEntry{
			UUID:   strings.TrimPrefix(f[0], "UUID="),
			Target: f[1],
			Fstype: f[2],
		})
	}
	return out
}

func (m *Manager) rewriteFstab(mutate func([]fstabEntry) []fstabEntry) error {
	existing, err := os.ReadFile(fstabPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", fstabPath, err)
	}
	updated := renderFstab(existing, mutate(parseManagedEntries(existing)))

	// Write to a sibling temp file and rename, so a crash mid-write cannot
	// leave a truncated fstab behind.
	tmp := fstabPath + ".homeos-tmp"
	if err := os.WriteFile(tmp, updated, 0o644); err != nil {
		return fmt.Errorf("stage %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, fstabPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", fstabPath, err)
	}
	return nil
}

func (m *Manager) addFstabEntry(uuid, target, fstype string) error {
	return m.rewriteFstab(func(cur []fstabEntry) []fstabEntry {
		for i, e := range cur {
			if e.Target == target {
				cur[i] = fstabEntry{UUID: uuid, Target: target, Fstype: fstype}
				return cur
			}
		}
		return append(cur, fstabEntry{UUID: uuid, Target: target, Fstype: fstype})
	})
}

func (m *Manager) removeFstabEntry(target string) error {
	return m.rewriteFstab(func(cur []fstabEntry) []fstabEntry {
		out := cur[:0]
		for _, e := range cur {
			if e.Target != target {
				out = append(out, e)
			}
		}
		return out
	})
}
