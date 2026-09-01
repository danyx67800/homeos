// Package telemetry samples the machine and publishes snapshots to the hub.
//
// Two cadences, deliberately: the cheap counters (CPU, memory, load, network)
// are read every couple of seconds, while SMART is polled on the order of tens
// of minutes. Running smartctl against a sleeping drive spins it up, so a fast
// SMART poll would keep every disk awake permanently and wear it out. That is
// why DiskHealth is not part of Snapshot.
package telemetry

import "time"

type Snapshot struct {
	Timestamp   time.Time     `json:"timestamp"`
	UptimeSecs  uint64        `json:"uptime_seconds"`
	CPU         CPUStats      `json:"cpu"`
	Memory      MemoryStats   `json:"memory"`
	Swap        SwapStats     `json:"swap"`
	Load        LoadStats     `json:"load"`
	Temperature []TempReading `json:"temperature"`
	Fans        []FanReading  `json:"fans"`
	Network     []NetReading  `json:"network"`
	Filesystems []FSUsage     `json:"filesystems"`
}

type CPUStats struct {
	// UsagePercent is the aggregate across all cores, 0-100.
	UsagePercent float64   `json:"usage_percent"`
	PerCore      []float64 `json:"per_core"`
	Cores        int       `json:"cores"`
	Threads      int       `json:"threads"`
	ModelName    string    `json:"model_name"`
	MHz          float64   `json:"mhz"`
}

type MemoryStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	CachedBytes    uint64  `json:"cached_bytes"`
	UsedPercent    float64 `json:"used_percent"`
}

type SwapStats struct {
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

type LoadStats struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// TempReading is one hwmon input. Primary marks the sensor the dashboard should
// show as "the" CPU temperature; see classifyTemp.
type TempReading struct {
	Chip     string  `json:"chip"`
	Label    string  `json:"label"`
	Celsius  float64 `json:"celsius"`
	HighC    float64 `json:"high_celsius,omitempty"`
	CritC    float64 `json:"critical_celsius,omitempty"`
	Primary  bool    `json:"primary,omitempty"`
	Category string  `json:"category"` // cpu | drive | board | other
}

type FanReading struct {
	Chip  string `json:"chip"`
	Label string `json:"label"`
	RPM   int    `json:"rpm"`
}

type NetReading struct {
	Interface   string `json:"interface"`
	BytesSent   uint64 `json:"bytes_sent"`
	BytesRecv   uint64 `json:"bytes_recv"`
	SentPerSec  uint64 `json:"sent_bytes_per_sec"`
	RecvPerSec  uint64 `json:"recv_bytes_per_sec"`
	PacketsSent uint64 `json:"packets_sent"`
	PacketsRecv uint64 `json:"packets_recv"`
}

type FSUsage struct {
	Mountpoint  string  `json:"mountpoint"`
	Device      string  `json:"device"`
	Fstype      string  `json:"fstype"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
}
