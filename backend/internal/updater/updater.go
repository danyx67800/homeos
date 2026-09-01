package updater

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// maxArtifact bounds what will be written to disk from a URL. An appliance with
// a small root filesystem must not be filled by a malicious or misconfigured
// channel before the checksum ever gets a chance to fail.
const maxArtifact = 512 << 20 // 512 MiB

type State string

const (
	StateIdle        State = "idle"
	StateChecking    State = "checking"
	StateAvailable   State = "available"
	StateDownloading State = "downloading"
	StateVerifying   State = "verifying"
	StateStaged      State = "staged"
	StateApplying    State = "applying"
	StateFailed      State = "failed"
	StateUpToDate    State = "up_to_date"
)

// Status is what the dashboard renders.
type Status struct {
	State          State     `json:"state"`
	CurrentVersion string    `json:"current_version"`
	Available      *Release  `json:"available,omitempty"`
	Progress       int       `json:"progress"`
	Message        string    `json:"message,omitempty"`
	Error          string    `json:"error,omitempty"`
	LastCheckedAt  time.Time `json:"last_checked_at,omitempty"`
	StagedVersion  string    `json:"staged_version,omitempty"`
}

type Config struct {
	ChannelURL  string // https://…/stable.json
	Arch        string // GOARCH
	Version     string // the running version
	ReleasesDir string // /usr/lib/homeos/releases
	PublicKey   ed25519.PublicKey
	// ApplyCommand is the privileged helper that swaps the symlink and
	// restarts the service. Run detached: the process being replaced cannot
	// supervise its own replacement.
	ApplyCommand []string
}

type Updater struct {
	cfg    Config
	log    *slog.Logger
	client *http.Client

	mu     sync.RWMutex
	status Status

	onChange func(Status)
}

func New(cfg Config, log *slog.Logger, onChange func(Status)) *Updater {
	return &Updater{
		cfg: cfg,
		log: log,
		client: &http.Client{
			Timeout: 30 * time.Minute, // a slow ADSL line downloading 25 MB
		},
		status:   Status{State: StateIdle, CurrentVersion: cfg.Version},
		onChange: onChange,
	}
}

func (u *Updater) Status() Status {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.status
}

func (u *Updater) set(mutate func(*Status)) {
	u.mu.Lock()
	mutate(&u.status)
	snapshot := u.status
	u.mu.Unlock()
	if u.onChange != nil {
		u.onChange(snapshot)
	}
}

// Check fetches the channel and reports the newest applicable release.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	if u.cfg.ChannelURL == "" {
		return nil, errors.New("no update channel configured")
	}
	u.set(func(s *Status) { s.State, s.Error, s.Progress = StateChecking, "", 0 })

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.cfg.ChannelURL, nil)
	if err != nil {
		return nil, u.fail(err)
	}
	req.Header.Set("User-Agent", "homeos-core/"+u.cfg.Version)

	res, err := u.client.Do(req)
	if err != nil {
		return nil, u.fail(fmt.Errorf("fetch update channel: %w", err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, u.fail(fmt.Errorf("update channel returned %s", res.Status))
	}

	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, u.fail(fmt.Errorf("read update channel: %w", err))
	}
	ch, err := ParseChannel(raw)
	if err != nil {
		return nil, u.fail(err)
	}

	rel, err := u.pick(ch)
	now := time.Now()
	if err != nil {
		if errors.Is(err, errUpToDate) {
			u.set(func(s *Status) {
				s.State, s.Available, s.LastCheckedAt, s.Error = StateUpToDate, nil, now, ""
			})
			return nil, nil
		}
		return nil, u.fail(err)
	}

	u.set(func(s *Status) {
		s.State, s.Available, s.LastCheckedAt, s.Error = StateAvailable, rel, now, ""
		s.Message = "version " + rel.Version + " is available"
	})
	return rel, nil
}

var errUpToDate = errors.New("already up to date")

// pick returns the newest release that is both newer than the running build and
// reachable from it.
func (u *Updater) pick(ch *Channel) (*Release, error) {
	cur, err := ParseVersion(u.cfg.Version)
	if err != nil {
		return nil, fmt.Errorf("cannot parse the running version %q: %w", u.cfg.Version, err)
	}

	var best *Release
	var bestV Version
	for i := range ch.Releases {
		r := ch.Releases[i]
		v, err := ParseVersion(r.Version)
		if err != nil || !cur.LessThan(v) {
			continue
		}
		if _, err := r.ArtifactFor(u.cfg.Arch); err != nil {
			continue // published, but not for this machine
		}
		if best == nil || bestV.LessThan(v) {
			best, bestV = &ch.Releases[i], v
		}
	}
	if best == nil {
		return nil, errUpToDate
	}

	// A release may declare the oldest version it can be applied on top of.
	// Refusing here is far better than applying it and discovering the
	// migration it assumed never ran.
	if best.MinVersion != "" {
		min, err := ParseVersion(best.MinVersion)
		if err == nil && cur.LessThan(min) {
			return nil, fmt.Errorf("%w: %s requires at least %s, this box runs %s",
				ErrTooOld, best.Version, best.MinVersion, u.cfg.Version)
		}
	}
	return best, nil
}

func (u *Updater) fail(err error) error {
	u.set(func(s *Status) { s.State, s.Error = StateFailed, err.Error() })
	u.log.Error("update", "error", err)
	return err
}
