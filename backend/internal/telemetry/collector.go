package telemetry

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

type Collector struct {
	interval  time.Duration
	hwmonRoot string
	log       *slog.Logger

	mu       sync.RWMutex
	latest   Snapshot
	history  []Snapshot
	histCap  int
	cpuModel string
	cores    int
	threads  int

	// Previous network counters, for per-second rates.
	prevNet  map[string]net.IOCountersStat
	prevTime time.Time
}

func NewCollector(interval time.Duration, historyRetention time.Duration, log *slog.Logger) *Collector {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	capacity := int(historyRetention / interval)
	if capacity < 30 {
		capacity = 30
	}
	if capacity > 5000 {
		capacity = 5000 // ~2.7h at 2s; bounds memory on a long-lived daemon
	}
	c := &Collector{
		interval:  interval,
		hwmonRoot: DefaultHwmonRoot,
		log:       log,
		histCap:   capacity,
		history:   make([]Snapshot, 0, capacity),
		prevNet:   map[string]net.IOCountersStat{},
	}
	c.readStaticCPUInfo()
	return c
}

// readStaticCPUInfo caches values that never change, so the hot loop does not
// re-read /proc/cpuinfo every two seconds.
func (c *Collector) readStaticCPUInfo() {
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		c.cpuModel = strings.TrimSpace(infos[0].ModelName)
	}
	if n, err := cpu.Counts(false); err == nil {
		c.cores = n
	}
	if n, err := cpu.Counts(true); err == nil {
		c.threads = n
	}
}

// Run samples until ctx is cancelled, publishing each snapshot.
func (c *Collector) Run(ctx context.Context, pub func(Snapshot)) {
	// Prime the CPU counters so the first published sample is a real delta
	// rather than an average since boot.
	_, _ = cpu.Percent(0, false)
	c.prevTime = time.Now()
	if counters, err := net.IOCounters(true); err == nil {
		for _, n := range counters {
			c.prevNet[n.Name] = n
		}
	}

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := c.sample()
			c.store(snap)
			if pub != nil {
				pub(snap)
			}
		}
	}
}

func (c *Collector) store(s Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.latest = s
	if len(c.history) == c.histCap {
		copy(c.history, c.history[1:])
		c.history[len(c.history)-1] = s
	} else {
		c.history = append(c.history, s)
	}
}

func (c *Collector) Latest() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

// History returns a copy, newest last. n <= 0 means everything retained.
func (c *Collector) History(n int) []Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if n <= 0 || n > len(c.history) {
		n = len(c.history)
	}
	out := make([]Snapshot, n)
	copy(out, c.history[len(c.history)-n:])
	return out
}

func (c *Collector) sample() Snapshot {
	now := time.Now()
	s := Snapshot{
		Timestamp: now,
		CPU:       CPUStats{ModelName: c.cpuModel, Cores: c.cores, Threads: c.threads},
	}

	// Interval 0 means "since the previous call", which is what the ticker
	// cadence gives us. Passing a duration here would block the loop instead.
	if v, err := cpu.Percent(0, false); err == nil && len(v) > 0 {
		s.CPU.UsagePercent = round1(v[0])
	}
	if v, err := cpu.Percent(0, true); err == nil {
		s.CPU.PerCore = make([]float64, len(v))
		for i, p := range v {
			s.CPU.PerCore[i] = round1(p)
		}
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		s.CPU.MHz = infos[0].Mhz
	}

	if v, err := mem.VirtualMemory(); err == nil {
		s.Memory = MemoryStats{
			TotalBytes:     v.Total,
			UsedBytes:      v.Used,
			FreeBytes:      v.Free,
			AvailableBytes: v.Available,
			CachedBytes:    v.Cached,
			UsedPercent:    round1(v.UsedPercent),
		}
	}
	if v, err := mem.SwapMemory(); err == nil {
		s.Swap = SwapStats{
			TotalBytes:  v.Total,
			UsedBytes:   v.Used,
			UsedPercent: round1(v.UsedPercent),
		}
	}
	if v, err := load.Avg(); err == nil {
		s.Load = LoadStats{Load1: v.Load1, Load5: v.Load5, Load15: v.Load15}
	}
	if v, err := host.Uptime(); err == nil {
		s.UptimeSecs = v
	}

	s.Temperature, s.Fans = readHwmon(c.hwmonRoot)
	// gopsutil reads thermal zones that are not exposed under hwmon on some
	// ARM boards, so it is a fallback rather than the primary source.
	if len(s.Temperature) == 0 {
		s.Temperature = fallbackTemperatures()
	}

	s.Network = c.sampleNetwork(now)
	s.Filesystems = sampleFilesystems()
	return s
}

func fallbackTemperatures() []TempReading {
	sensors, err := host.SensorsTemperatures()
	if err != nil && len(sensors) == 0 {
		return nil
	}
	out := make([]TempReading, 0, len(sensors))
	for _, s := range sensors {
		if s.Temperature <= 0 {
			continue
		}
		t := TempReading{Chip: s.SensorKey, Label: s.SensorKey, Celsius: round1(s.Temperature)}
		t.Category, t.Primary = classifyTemp(s.SensorKey, s.SensorKey)
		out = append(out, t)
	}
	promotePrimary(out)
	return out
}

// sampleNetwork converts cumulative counters into per-second rates. Virtual and
// per-container interfaces are excluded: veth devices come and go with every
// container start and would swamp the dashboard.
func (c *Collector) sampleNetwork(now time.Time) []NetReading {
	counters, err := net.IOCounters(true)
	if err != nil {
		return nil
	}
	elapsed := now.Sub(c.prevTime).Seconds()
	c.prevTime = now

	out := make([]NetReading, 0, len(counters))
	for _, n := range counters {
		if isVirtualIface(n.Name) {
			continue
		}
		r := NetReading{
			Interface:   n.Name,
			BytesSent:   n.BytesSent,
			BytesRecv:   n.BytesRecv,
			PacketsSent: n.PacketsSent,
			PacketsRecv: n.PacketsRecv,
		}
		if prev, ok := c.prevNet[n.Name]; ok && elapsed > 0 {
			// Guard against counter resets on interface bounce, which would
			// otherwise underflow to an enormous uint64 rate.
			if n.BytesSent >= prev.BytesSent {
				r.SentPerSec = uint64(float64(n.BytesSent-prev.BytesSent) / elapsed)
			}
			if n.BytesRecv >= prev.BytesRecv {
				r.RecvPerSec = uint64(float64(n.BytesRecv-prev.BytesRecv) / elapsed)
			}
		}
		c.prevNet[n.Name] = n
		out = append(out, r)
	}
	return out
}

func isVirtualIface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range []string{"veth", "docker", "br-", "homeos0", "virbr", "tun", "tap"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// sampleFilesystems reports real, mounted data filesystems. Pseudo filesystems
// and Docker's per-layer overlay mounts are excluded; there can be hundreds of
// the latter and none of them mean anything to a user.
func sampleFilesystems() []FSUsage {
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil
	}
	out := make([]FSUsage, 0, len(parts))
	for _, p := range parts {
		if !isRealFilesystem(p.Fstype) || isNoiseMount(p.Mountpoint) {
			continue
		}
		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u.Total == 0 {
			continue
		}
		out = append(out, FSUsage{
			Mountpoint:  p.Mountpoint,
			Device:      p.Device,
			Fstype:      p.Fstype,
			TotalBytes:  u.Total,
			UsedBytes:   u.Used,
			FreeBytes:   u.Free,
			UsedPercent: round1(u.UsedPercent),
		})
	}
	return out
}

func isRealFilesystem(fstype string) bool {
	switch fstype {
	case "ext2", "ext3", "ext4", "btrfs", "xfs", "zfs", "f2fs",
		"vfat", "exfat", "ntfs", "ntfs3", "fuseblk":
		return true
	}
	return false
}

func isNoiseMount(mp string) bool {
	for _, p := range []string{
		"/var/lib/docker/", "/snap/", "/run/", "/sys/", "/proc/", "/dev/",
	} {
		if strings.HasPrefix(mp, p) {
			return true
		}
	}
	return false
}
