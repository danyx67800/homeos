package dockerx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Compose drives `docker compose` for one app stack.
//
// The CLI plugin rather than a Go Compose library: it is what the operator has
// installed, what the generated file is validated against, and what they will
// reach for when debugging by hand. Reimplementing its semantics in-process
// would mean HomeOS and the operator seeing different behaviour from the same
// file.
type Compose struct {
	dir     string // the app's directory, holding docker-compose.yml
	project string
	timeout time.Duration
}

func NewCompose(dir, project string) *Compose {
	return &Compose{dir: dir, project: project, timeout: 15 * time.Minute}
}

func (c *Compose) run(ctx context.Context, onLine func(string), args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	full := append([]string{"compose", "--project-name", c.project,
		"--file", filepath.Join(c.dir, "docker-compose.yml")}, args...)

	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = c.dir
	cmd.Env = append(os.Environ(), "COMPOSE_PROGRESS=plain", "DOCKER_CLI_HINTS=false")

	var out bytes.Buffer
	if onLine != nil {
		cmd.Stdout = lineWriter{onLine, &out}
		cmd.Stderr = lineWriter{onLine, &out}
	} else {
		cmd.Stdout = &out
		cmd.Stderr = &out
	}

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("docker compose %s timed out after %s", args[0], c.timeout)
		}
		msg := out.String()
		if len(msg) > 2000 {
			msg = "..." + msg[len(msg)-2000:]
		}
		return fmt.Errorf("docker compose %s: %w\n%s", args[0], err, msg)
	}
	return nil
}

// Up pulls images and starts the stack. onLine receives progress so the
// dashboard can show a real install log rather than a spinner.
func (c *Compose) Up(ctx context.Context, onLine func(string)) error {
	if err := c.run(ctx, onLine, "pull", "--quiet"); err != nil {
		// A pull failure is usually a missing arm64 tag or a rate limit; both
		// are worth reporting verbatim rather than as "install failed".
		return err
	}
	return c.run(ctx, onLine, "up", "--detach", "--remove-orphans", "--wait")
}

// Down stops the stack. Volumes are preserved unless the caller asks otherwise,
// so "uninstall but keep my data" is the default shape.
func (c *Compose) Down(ctx context.Context, removeVolumes bool, onLine func(string)) error {
	args := []string{"down", "--remove-orphans"}
	if removeVolumes {
		args = append(args, "--volumes")
	}
	return c.run(ctx, onLine, args...)
}

func (c *Compose) Stop(ctx context.Context) error  { return c.run(ctx, nil, "stop") }
func (c *Compose) Start(ctx context.Context) error { return c.run(ctx, nil, "start") }

// Config validates the generated file without touching the daemon. Run before
// every Up so a bad manifest fails with a parse error instead of a half-created
// stack.
func (c *Compose) Config(ctx context.Context) error {
	return c.run(ctx, nil, "config", "--quiet")
}

// lineWriter splits writes into lines for the progress callback while keeping
// the full text for error reporting.
type lineWriter struct {
	emit func(string)
	buf  *bytes.Buffer
}

func (w lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		if s := string(bytes.TrimSpace(line)); s != "" {
			w.emit(s)
		}
	}
	return len(p), nil
}
