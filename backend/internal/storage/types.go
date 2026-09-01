package storage

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Device is a whole disk. Partitions hang off it.
type Device struct {
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	Model      string      `json:"model,omitempty"`
	Serial     string      `json:"serial,omitempty"`
	Vendor     string      `json:"vendor,omitempty"`
	SizeBytes  uint64      `json:"size_bytes"`
	Rotational bool        `json:"rotational"`
	Removable  bool        `json:"removable"`
	Transport  string      `json:"transport,omitempty"` // sata | nvme | usb | ...
	Type       string      `json:"type"`                // disk | rom | loop
	Partitions []Partition `json:"partitions"`
	Health     *Health     `json:"health,omitempty"`
	// InUse is true when the disk or any partition is mounted, so the UI can
	// refuse a destructive action without a round trip.
	InUse bool `json:"in_use"`
}

type Partition struct {
	Path        string  `json:"path"`
	Name        string  `json:"name"`
	SizeBytes   uint64  `json:"size_bytes"`
	Fstype      string  `json:"fstype,omitempty"`
	Label       string  `json:"label,omitempty"`
	UUID        string  `json:"uuid,omitempty"`
	Mountpoint  string  `json:"mountpoint,omitempty"`
	UsedBytes   uint64  `json:"used_bytes,omitempty"`
	FreeBytes   uint64  `json:"free_bytes,omitempty"`
	UsedPercent float64 `json:"used_percent,omitempty"`
}

// Health is the subset of SMART worth putting on a dashboard. The full
// attribute table is kept for the detail view.
type Health struct {
	Supported      bool             `json:"supported"`
	Passed         bool             `json:"passed"`
	TemperatureC   int              `json:"temperature_celsius,omitempty"`
	PowerOnHours   uint64           `json:"power_on_hours,omitempty"`
	PowerCycles    uint64           `json:"power_cycle_count,omitempty"`
	PercentageUsed int              `json:"percentage_used,omitempty"` // NVMe wear
	Attributes     []SMARTAttribute `json:"attributes,omitempty"`
	// Warnings names the specific reasons a drive looks unhealthy, so the UI
	// can say "5 reallocated sectors" instead of a bare red dot.
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type SMARTAttribute struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Value     int    `json:"value"`
	Worst     int    `json:"worst"`
	Threshold int    `json:"threshold"`
	Raw       string `json:"raw"`
	Failing   bool   `json:"failing"`
}

// flexUint64 accepts a JSON number or a decimal string. util-linux changed
// lsblk's JSON types between releases, and HomeOS supports both Debian 12
// (2.38) and Ubuntu 24.04 (2.39+), so the parser must tolerate either.
type flexUint64 uint64

func (f *flexUint64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		// Sizes such as "465.8G" appear when lsblk is called without -b. We
		// always pass -b, so this is a malformed rather than expected value.
		return nil
	}
	*f = flexUint64(v)
	return nil
}

// flexBool accepts true/false, "1"/"0" and 1/0.
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	switch s {
	case "true", "1":
		*f = true
	default:
		*f = false
	}
	return nil
}

// flexString tolerates a JSON null where a string is expected.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return nil
	}
	*f = flexString(strings.TrimSpace(s))
	return nil
}
