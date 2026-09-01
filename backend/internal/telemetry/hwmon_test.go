package telemetry

import (
	"os"
	"path/filepath"
	"testing"
)

// buildHwmonFixture writes a sysfs-shaped tree: chip name in `name`, readings in
// `<kind><n>_input`, optional `<kind><n>_label`.
func buildHwmonFixture(t *testing.T, chips []struct {
	name  string
	files map[string]string
}) string {
	t.Helper()
	root := t.TempDir()
	for i, c := range chips {
		dir := filepath.Join(root, "hwmon"+itoa(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "name"), []byte(c.name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for f, v := range c.files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte(v+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestReadHwmonIntelBoard(t *testing.T) {
	root := buildHwmonFixture(t, []struct {
		name  string
		files map[string]string
	}{
		{"coretemp", map[string]string{
			"temp1_input": "47000", "temp1_label": "Package id 0",
			"temp1_max": "84000", "temp1_crit": "100000",
			"temp2_input": "45000", "temp2_label": "Core 0",
			"temp3_input": "49000", "temp3_label": "Core 1",
		}},
		{"nct6797", map[string]string{
			"fan1_input": "1120", "fan1_label": "CPU Fan",
			"fan2_input": "0", "fan2_label": "Chassis Fan",
			"temp1_input": "0", // unpopulated channel: must be skipped
		}},
		{"nvme", map[string]string{
			"temp1_input": "38850", "temp1_label": "Composite",
		}},
	})

	temps, fans := readHwmon(root)

	if len(temps) != 4 {
		t.Fatalf("got %d temps, want 4 (the 0-value channel must be dropped): %+v", len(temps), temps)
	}

	var primary *TempReading
	for i := range temps {
		if temps[i].Primary {
			if primary != nil {
				t.Fatalf("two primaries: %+v and %+v", *primary, temps[i])
			}
			primary = &temps[i]
		}
	}
	if primary == nil {
		t.Fatal("no primary temperature selected")
	}
	if primary.Label != "Package id 0" {
		t.Errorf("primary = %q, want the package sensor", primary.Label)
	}
	if primary.Celsius != 47 {
		t.Errorf("primary celsius = %v, want 47", primary.Celsius)
	}
	if primary.CritC != 100 {
		t.Errorf("crit = %v, want 100", primary.CritC)
	}

	byLabel := map[string]TempReading{}
	for _, tr := range temps {
		byLabel[tr.Label] = tr
	}
	if got := byLabel["Composite"].Category; got != "drive" {
		t.Errorf("nvme category = %q, want drive", got)
	}
	if got := byLabel["Composite"].Celsius; got != 38.9 {
		t.Errorf("nvme celsius = %v, want 38.9 (rounded)", got)
	}

	// A stopped fan is real information and must survive.
	if len(fans) != 2 {
		t.Fatalf("got %d fans, want 2: %+v", len(fans), fans)
	}
	if fans[0].RPM != 1120 || fans[0].Label != "CPU Fan" {
		t.Errorf("fan0 = %+v", fans[0])
	}
	if fans[1].RPM != 0 {
		t.Errorf("stopped fan was dropped: %+v", fans[1])
	}
}

// AMD reports the die as "Tctl" with no "package" in the label.
func TestReadHwmonAMDPrimary(t *testing.T) {
	root := buildHwmonFixture(t, []struct {
		name  string
		files map[string]string
	}{
		{"k10temp", map[string]string{
			"temp1_input": "52125", "temp1_label": "Tctl",
			"temp2_input": "41000", "temp2_label": "Tccd1",
		}},
		{"acpitz", map[string]string{"temp1_input": "27800"}},
	})

	temps, _ := readHwmon(root)
	for _, tr := range temps {
		if tr.Primary {
			if tr.Label != "Tctl" {
				t.Errorf("primary = %q (%s), want Tctl", tr.Label, tr.Chip)
			}
			return
		}
	}
	t.Error("no primary selected")
}

// A Raspberry Pi has one thermal zone and no labels at all.
func TestReadHwmonRaspberryPi(t *testing.T) {
	root := buildHwmonFixture(t, []struct {
		name  string
		files map[string]string
	}{
		{"cpu_thermal", map[string]string{"temp1_input": "61200"}},
	})

	temps, fans := readHwmon(root)
	if len(temps) != 1 {
		t.Fatalf("got %d temps", len(temps))
	}
	if !temps[0].Primary || temps[0].Category != "cpu" {
		t.Errorf("pi sensor = %+v, want primary cpu", temps[0])
	}
	if temps[0].Label != "temp1" {
		t.Errorf("unlabelled sensor got name %q, want temp1", temps[0].Label)
	}
	if len(fans) != 0 {
		t.Errorf("expected no fans, got %+v", fans)
	}
}

func TestReadHwmonMissingRoot(t *testing.T) {
	temps, fans := readHwmon(filepath.Join(t.TempDir(), "nope"))
	if temps != nil || fans != nil {
		t.Errorf("expected nil,nil for an absent tree")
	}
}
