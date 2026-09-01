package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes an external command. It exists so the operations below can be
// tested for the exact argv they build without needing root or real disks --
// which is the assertion that actually matters, since these run under sudo.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands for real. Privileged ones go through `sudo -n`
// against the allowlist install.sh writes to /etc/sudoers.d/homeos; -n makes a
// missing rule fail immediately instead of blocking on a password prompt that
// nothing can answer.
type ExecRunner struct {
	Sudo    bool
	Timeout time.Duration
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := args
	if r.Sudo {
		argv = append([]string{"-n", name}, args...)
		name = "sudo"
	}

	// Resolve first, so "not installed" is reported as such rather than as a
	// generic exec failure buried in the message.
	if _, err := exec.LookPath(name); err != nil {
		return nil, classifyExec(name, err)
	}

	cmd := exec.CommandContext(ctx, name, argv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Never inherit the caller's environment into a privileged command.
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.Bytes(), fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return stdout.Bytes(), nil
}

// smartctl exits non-zero to report drive conditions, not just tool failures.
// Bits 0-2 mean the command or device is genuinely broken; bits 3-7 are health
// findings that come with perfectly good JSON on stdout, so those must be
// treated as success or every failing drive would look like a tool error.
func smartctlFatal(err error, stdout []byte) bool {
	if err == nil {
		return false
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		// Timeout, or the binary is missing.
		return true
	}
	code := ee.ExitCode()
	if code&0b111 != 0 {
		return true
	}
	// Health bits set: usable output is expected on stdout.
	return len(bytes.TrimSpace(stdout)) == 0
}

// ErrToolMissing marks a command that could not be executed at all, as opposed
// to one that ran and reported a failure.
//
// The distinction matters to the caller: a missing lsblk or smartctl means the
// storage subsystem is unavailable on this system (a container-based install,
// or a stripped image), which is a 503 the dashboard can explain. A command
// that ran and failed is a real error about a real disk.
var ErrToolMissing = errors.New("required tool is not installed")

func classifyExec(name string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) || strings.Contains(err.Error(), "executable file not found") {
		return fmt.Errorf("%w: %s", ErrToolMissing, name)
	}
	return err
}
