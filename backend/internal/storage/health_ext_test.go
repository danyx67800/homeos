package storage_test

import (
	"github.com/danyx67800/homeos/backend/internal/storage"
	"os"
	"path/filepath"
	"testing"
)

// Fixtures live in testdata/ rather than as string constants: real smartctl
// output is long, and keeping it as data makes it easy to drop in a capture
// from a new drive when one misbehaves.
func loadSMART(t *testing.T, name string) *storage.Health {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	h, err := storage.ParseSMARTForTest(raw)
	if err != nil {
		t.Fatalf("storage.ParseSMARTForTest(%s): %v", name, err)
	}
	return h
}

// The case that matters most: SMART says "passed" while the drive is quietly
// reallocating sectors. Reporting only smart_status would paint this green.
func TestParseSMARTPassingButFailingDrive(t *testing.T) {
	h := loadSMART(t, "smart_ata_degraded.json")

	if !h.Supported || !h.Passed {
		t.Fatalf("fixture should be supported and passing: %+v", h)
	}
	if h.TemperatureC != 41 || h.PowerOnHours != 38104 || h.PowerCycles != 91 {
		t.Errorf("headline values wrong: %+v", h)
	}
	if len(h.Attributes) != 4 {
		t.Errorf("got %d attributes, want 4", len(h.Attributes))
	}
	if !h.Degraded() {
		t.Error("12 reallocated and 3 pending sectors must read as degraded")
	}

	want := map[string]bool{
		"12 reallocated sectors":         false,
		"3 sectors pending reallocation": false,
	}
	for _, w := range h.Warnings {
		if _, ok := want[w]; !ok {
			t.Errorf("unexpected warning %q", w)
			continue
		}
		want[w] = true
	}
	for w, seen := range want {
		if !seen {
			t.Errorf("missing warning %q", w)
		}
	}
}

func TestParseSMARTHealthy(t *testing.T) {
	h := loadSMART(t, "smart_ata_healthy.json")
	if h.Degraded() {
		t.Errorf("healthy drive marked degraded: %v", h.Warnings)
	}
	if len(h.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", h.Warnings)
	}
	if len(h.Attributes) != 2 {
		t.Errorf("attributes should still be reported for the detail view")
	}
}

// NVMe carries no ATA attribute table, and smart_support is an ATA-only field
// so it is absent entirely.
func TestParseSMARTNVMeWear(t *testing.T) {
	h := loadSMART(t, "smart_nvme_worn.json")
	if !h.Supported {
		t.Error("an NVMe drive that answered should count as SMART-capable")
	}
	if h.TemperatureC != 44 {
		t.Errorf("temperature = %d, want 44 from the NVMe log", h.TemperatureC)
	}
	if h.PercentageUsed != 93 {
		t.Errorf("percentage used = %d", h.PercentageUsed)
	}
	if !h.Degraded() {
		t.Error("93% endurance consumed should read as degraded")
	}
}

func TestParseSMARTOutrightFailure(t *testing.T) {
	h := loadSMART(t, "smart_ata_failed.json")
	if h.Passed || !h.Degraded() {
		t.Errorf("failed drive = %+v", h)
	}
	if !h.Attributes[0].Failing {
		t.Error("when_failed should mark the attribute as failing")
	}
	if len(h.Warnings) != 2 {
		t.Errorf("warnings = %v, want the raw count and the failing attribute", h.Warnings)
	}
}

// A drive behind a USB bridge that does not pass SMART through is unknown, not
// unhealthy: showing it red would train users to ignore the indicator.
func TestParseSMARTUnsupported(t *testing.T) {
	h := loadSMART(t, "smart_unsupported.json")
	if h.Supported {
		t.Error("should report unsupported")
	}
	if h.Degraded() {
		t.Error("unsupported must not read as degraded")
	}
	if h.Error == "" {
		t.Error("the smartctl error message should be surfaced")
	}
}
