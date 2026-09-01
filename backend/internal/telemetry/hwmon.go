package telemetry

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultHwmonRoot is where the kernel exposes sensor chips.
const DefaultHwmonRoot = "/sys/class/hwmon"

// readHwmon walks the hwmon tree once and returns both temperatures and fan
// speeds. gopsutil can produce temperatures, but not fan RPM, and doing both in
// one pass keeps the chip/label association consistent between them.
//
// root is a parameter so the parser can be exercised against a fixture tree;
// production callers pass DefaultHwmonRoot.
func readHwmon(root string) ([]TempReading, []FanReading) {
	chips, err := filepath.Glob(filepath.Join(root, "hwmon*"))
	if err != nil || len(chips) == 0 {
		return nil, nil
	}
	sort.Strings(chips)

	var temps []TempReading
	var fans []FanReading

	for _, chipDir := range chips {
		chip := readTrim(filepath.Join(chipDir, "name"))
		if chip == "" {
			chip = filepath.Base(chipDir)
		}

		for _, in := range globSorted(chipDir, "temp*_input") {
			milli, ok := readInt(in)
			if !ok {
				continue
			}
			// A sensor reading exactly 0 is almost always an unpopulated
			// channel rather than a genuine 0 degrees C.
			if milli == 0 {
				continue
			}
			prefix := strings.TrimSuffix(in, "_input")
			label := readTrim(prefix + "_label")
			if label == "" {
				label = "temp" + numericSuffix(filepath.Base(prefix))
			}
			t := TempReading{
				Chip:    chip,
				Label:   label,
				Celsius: round1(float64(milli) / 1000),
			}
			if v, ok := readInt(prefix + "_max"); ok && v > 0 {
				t.HighC = round1(float64(v) / 1000)
			}
			if v, ok := readInt(prefix + "_crit"); ok && v > 0 {
				t.CritC = round1(float64(v) / 1000)
			}
			t.Category, t.Primary = classifyTemp(chip, label)
			temps = append(temps, t)
		}

		for _, in := range globSorted(chipDir, "fan*_input") {
			rpm, ok := readInt(in)
			if !ok {
				continue
			}
			prefix := strings.TrimSuffix(in, "_input")
			label := readTrim(prefix + "_label")
			if label == "" {
				label = "fan" + numericSuffix(filepath.Base(prefix))
			}
			// A stopped fan reports 0; that is real information (a failed or
			// PWM-idled fan), so unlike temperatures it is kept.
			fans = append(fans, FanReading{Chip: chip, Label: label, RPM: rpm})
		}
	}

	promotePrimary(temps)
	return temps, fans
}

// classifyTemp buckets a sensor and says whether it is a candidate for "the"
// CPU temperature. The chip names are the ones Linux uses for the common cases:
// coretemp (Intel), k10temp/zenpower (AMD), cpu_thermal (Raspberry Pi),
// nvme/drivetemp (disks).
func classifyTemp(chip, label string) (category string, primaryCandidate bool) {
	c, l := strings.ToLower(chip), strings.ToLower(label)

	switch {
	case strings.Contains(c, "coretemp"), strings.Contains(c, "k10temp"),
		strings.Contains(c, "zenpower"), strings.Contains(c, "cpu_thermal"),
		strings.Contains(c, "cpu-thermal"), strings.Contains(c, "soc_thermal"):
		// "Package id 0" (Intel) and "Tctl" (AMD) are the whole-die readings;
		// the per-core sensors are noisier and less useful on a dashboard.
		isAggregate := strings.Contains(l, "package") || l == "tctl" ||
			strings.Contains(c, "cpu_thermal") || strings.Contains(c, "cpu-thermal")
		return "cpu", isAggregate
	case strings.Contains(c, "nvme"), strings.Contains(c, "drivetemp"):
		return "drive", false
	case strings.Contains(c, "acpitz"):
		// Fallback only: acpitz is frequently the board, not the CPU.
		return "board", true
	default:
		return "other", false
	}
}

// promotePrimary marks exactly one reading as primary, preferring a real CPU
// die sensor over the ACPI thermal zone.
func promotePrimary(temps []TempReading) {
	best := -1
	for i := range temps {
		if !temps[i].Primary {
			continue
		}
		if best == -1 || (temps[best].Category != "cpu" && temps[i].Category == "cpu") {
			best = i
		}
	}
	// Nothing self-identified: fall back to the hottest CPU-category sensor.
	if best == -1 {
		for i := range temps {
			if temps[i].Category == "cpu" && (best == -1 || temps[i].Celsius > temps[best].Celsius) {
				best = i
			}
		}
	}
	for i := range temps {
		temps[i].Primary = i == best
	}
}

func globSorted(dir, pattern string) []string {
	m, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return nil
	}
	sort.Strings(m)
	return m
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(path string) (int, bool) {
	s := readTrim(path)
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// numericSuffix pulls "3" out of "temp3", so an unlabelled sensor still gets a
// stable, human-meaningful name.
func numericSuffix(base string) string {
	i := strings.IndexFunc(base, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return ""
	}
	return base[i:]
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
