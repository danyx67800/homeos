package storage

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Manager struct {
	mountRoot string
	defaultFS string
	log       *slog.Logger

	plain Runner // unprivileged: lsblk
	priv  Runner // sudo: smartctl, mkfs, mount, ...

	mu          sync.RWMutex
	healthCache map[string]*Health
	healthAt    map[string]time.Time
	healthTTL   time.Duration

	// One privileged mutation at a time. Two concurrent format calls against
	// the same disk, or a mount racing an unmount, corrupt state.
	opMu sync.Mutex
}

func NewManager(mountRoot, defaultFS string, smartTTL time.Duration, log *slog.Logger) *Manager {
	if mountRoot == "" {
		mountRoot = "/mnt/storage"
	}
	if defaultFS == "" {
		defaultFS = "ext4"
	}
	if smartTTL <= 0 {
		smartTTL = 30 * time.Minute
	}
	return &Manager{
		mountRoot:   mountRoot,
		defaultFS:   defaultFS,
		log:         log,
		plain:       ExecRunner{Timeout: 15 * time.Second},
		priv:        ExecRunner{Sudo: true, Timeout: 10 * time.Minute},
		healthCache: map[string]*Health{},
		healthAt:    map[string]time.Time{},
		healthTTL:   smartTTL,
	}
}

// WithRunners swaps the executors, for tests.
func (m *Manager) WithRunners(plain, priv Runner) *Manager {
	m.plain, m.priv = plain, priv
	return m
}

// List enumerates block devices. SMART is attached from cache only: reading it
// here would spin up every sleeping disk on each dashboard refresh.
func (m *Manager) List(ctx context.Context) ([]Device, error) {
	raw, err := m.plain.Run(ctx, "lsblk",
		"-J", "-b", "-o",
		"NAME,KNAME,PATH,TYPE,SIZE,MODEL,SERIAL,VENDOR,ROTA,RM,TRAN,FSTYPE,LABEL,UUID,MOUNTPOINT,FSUSED,FSAVAIL")
	if err != nil {
		return nil, fmt.Errorf("enumerate block devices: %w", err)
	}
	devs, err := parseLsblk(raw)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	for i := range devs {
		if h, ok := m.healthCache[devs[i].Path]; ok {
			devs[i].Health = h
		}
	}
	m.mu.RUnlock()
	return devs, nil
}

// Health reads SMART for one device, honouring the cache TTL. force bypasses it
// for an explicit "check now" from the UI.
func (m *Manager) Health(ctx context.Context, devPath string, force bool) (*Health, error) {
	if err := ValidateDevice(devPath); err != nil {
		return nil, err
	}

	if !force {
		m.mu.RLock()
		h, ok := m.healthCache[devPath]
		at := m.healthAt[devPath]
		m.mu.RUnlock()
		if ok && time.Since(at) < m.healthTTL {
			return h, nil
		}
	}

	// -n standby: never wake a sleeping drive just to read its temperature.
	// smartctl then exits 2 with no JSON, which we report as "asleep" rather
	// than as an error.
	raw, err := m.priv.Run(ctx, "smartctl", "-j", "-a", "-n", "standby", devPath)
	if smartctlFatal(err, raw) {
		h := &Health{Supported: false, Error: errString(err)}
		m.cacheHealth(devPath, h)
		return h, nil
	}

	h, perr := parseSMART(raw)
	if perr != nil {
		return &Health{Supported: false, Error: perr.Error()}, nil
	}
	m.cacheHealth(devPath, h)
	return h, nil
}

func (m *Manager) cacheHealth(devPath string, h *Health) {
	m.mu.Lock()
	m.healthCache[devPath] = h
	m.healthAt[devPath] = time.Now()
	m.mu.Unlock()
}

// RefreshAllHealth is the background SMART sweep. It walks disks serially: a
// parallel sweep would spin up every drive in the box at once, which is both a
// power spike and needless wear.
func (m *Manager) RefreshAllHealth(ctx context.Context) {
	devs, err := m.List(ctx)
	if err != nil {
		m.log.Warn("smart sweep: cannot list devices", "error", err)
		return
	}
	for _, d := range devs {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := m.Health(ctx, d.Path, true); err != nil {
			m.log.Debug("smart sweep", "device", d.Path, "error", err)
		}
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
