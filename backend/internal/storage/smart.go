package storage

import (
	"encoding/json"
	"fmt"
)

// smartctlOutput mirrors the subset of `smartctl -j -a` HomeOS uses. The schema
// is stable across smartmontools 7.x, which is what Debian 12 and Ubuntu 22.04+
// ship.
type smartctlOutput struct {
	Device struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"device"`
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`

	SmartSupport struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
	} `json:"smart_support"`

	SmartStatus struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`

	Temperature struct {
		Current int `json:"current"`
	} `json:"temperature"`

	PowerOnTime struct {
		Hours uint64 `json:"hours"`
	} `json:"power_on_time"`

	PowerCycleCount uint64 `json:"power_cycle_count"`

	ATAAttributes struct {
		Table []struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Value      int    `json:"value"`
			Worst      int    `json:"worst"`
			Thresh     int    `json:"thresh"`
			WhenFailed string `json:"when_failed"`
			Raw        struct {
				Value  uint64 `json:"value"`
				String string `json:"string"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`

	NVMeLog struct {
		CriticalWarning  int    `json:"critical_warning"`
		Temperature      int    `json:"temperature"`
		PercentageUsed   int    `json:"percentage_used"`
		MediaErrors      uint64 `json:"media_errors"`
		DataUnitsWritten uint64 `json:"data_units_written"`
	} `json:"nvme_smart_health_information_log"`

	// smartctl reports why it could not read a device here.
	Messages []struct {
		String   string `json:"string"`
		Severity string `json:"severity"`
	} `json:"smartctl_messages"`
}

// predictiveAttrs are the SMART attributes that actually correlate with
// imminent failure. A drive can report smart_status.passed while carrying
// hundreds of reallocated sectors, so "passed" alone is not a health verdict.
var predictiveAttrs = map[int]string{
	5:   "reallocated sectors",
	187: "uncorrectable errors reported",
	188: "command timeouts",
	197: "sectors pending reallocation",
	198: "offline uncorrectable sectors",
}

// parseSMART turns smartctl JSON into a Health verdict.
func parseSMART(raw []byte) (*Health, error) {
	var out smartctlOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse smartctl output: %w", err)
	}

	h := &Health{
		Supported:      out.SmartSupport.Available,
		Passed:         out.SmartStatus.Passed,
		TemperatureC:   out.Temperature.Current,
		PowerOnHours:   out.PowerOnTime.Hours,
		PowerCycles:    out.PowerCycleCount,
		PercentageUsed: out.NVMeLog.PercentageUsed,
	}

	// NVMe reports temperature in its own log rather than the generic field.
	if h.TemperatureC == 0 && out.NVMeLog.Temperature > 0 {
		h.TemperatureC = out.NVMeLog.Temperature
	}
	// An NVMe drive that answered at all supports SMART, even when
	// smart_support is absent from the JSON (it is an ATA-only field).
	if !h.Supported && (out.NVMeLog.Temperature > 0 || out.NVMeLog.PercentageUsed > 0) {
		h.Supported = true
	}

	for _, a := range out.ATAAttributes.Table {
		attr := SMARTAttribute{
			ID:        a.ID,
			Name:      a.Name,
			Value:     a.Value,
			Worst:     a.Worst,
			Threshold: a.Thresh,
			Raw:       a.Raw.String,
			Failing:   a.WhenFailed != "",
		}
		h.Attributes = append(h.Attributes, attr)

		if label, watched := predictiveAttrs[a.ID]; watched && a.Raw.Value > 0 {
			h.Warnings = append(h.Warnings,
				fmt.Sprintf("%d %s", a.Raw.Value, label))
		}
		if attr.Failing {
			h.Warnings = append(h.Warnings,
				fmt.Sprintf("attribute %s is failing", a.Name))
		}
	}

	// NVMe wear and error signals.
	if out.NVMeLog.CriticalWarning != 0 {
		h.Warnings = append(h.Warnings,
			fmt.Sprintf("NVMe critical warning flags 0x%02x", out.NVMeLog.CriticalWarning))
	}
	if out.NVMeLog.MediaErrors > 0 {
		h.Warnings = append(h.Warnings,
			fmt.Sprintf("%d NVMe media errors", out.NVMeLog.MediaErrors))
	}
	if out.NVMeLog.PercentageUsed >= 90 {
		h.Warnings = append(h.Warnings,
			fmt.Sprintf("NVMe endurance %d%% consumed", out.NVMeLog.PercentageUsed))
	}

	for _, m := range out.Messages {
		if m.Severity == "error" {
			h.Error = m.String
		}
	}
	return h, nil
}

// Degraded reports whether the UI should show this drive as needing attention.
// A drive is degraded if SMART failed it outright, or if any predictive
// attribute is non-zero even though the overall status still says "passed".
func (h *Health) Degraded() bool {
	if h == nil || !h.Supported {
		return false
	}
	return !h.Passed || len(h.Warnings) > 0
}
