package dockerx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// requireManaged is the gate in front of every mutating call. homeos-core holds
// full Docker access, so without it a bug here could stop a container the user
// runs by hand, or one belonging to a completely unrelated tool.
func (c *Client) requireManaged(ctx context.Context, id string) (string, error) {
	if !c.Available() {
		return "", c.unavailableErr()
	}
	insp, err := c.api.ContainerInspect(ctx, id)
	if err != nil {
		if strings.Contains(err.Error(), "No such container") {
			return "", fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return "", fmt.Errorf("inspect %s: %w", id, err)
	}
	if insp.Config == nil || insp.Config.Labels[LabelManaged] != "true" {
		return "", fmt.Errorf("%w: %s", ErrNotManaged, strings.TrimPrefix(insp.Name, "/"))
	}
	return insp.ID, nil
}

func (c *Client) Start(ctx context.Context, id string) error {
	real, err := c.requireManaged(ctx, id)
	if err != nil {
		return err
	}
	if err := c.api.ContainerStart(ctx, real, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	c.log.Info("container started", "id", short(real))
	return nil
}

// Stop sends SIGTERM and waits before SIGKILL. The default is generous because
// databases and media servers can take a while to flush.
func (c *Client) Stop(ctx context.Context, id string, timeout time.Duration) error {
	real, err := c.requireManaged(ctx, id)
	if err != nil {
		return err
	}
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 30
	}
	if err := c.api.ContainerStop(ctx, real, container.StopOptions{Timeout: &secs}); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	c.log.Info("container stopped", "id", short(real))
	return nil
}

func (c *Client) Restart(ctx context.Context, id string, timeout time.Duration) error {
	real, err := c.requireManaged(ctx, id)
	if err != nil {
		return err
	}
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 30
	}
	if err := c.api.ContainerRestart(ctx, real, container.StopOptions{Timeout: &secs}); err != nil {
		return fmt.Errorf("restart container: %w", err)
	}
	c.log.Info("container restarted", "id", short(real))
	return nil
}

// Remove deletes a container. Volumes are never removed here: an app's data
// outlives its container, and uninstalling data is a separate, explicit act in
// the app store.
func (c *Client) Remove(ctx context.Context, id string, force bool) error {
	real, err := c.requireManaged(ctx, id)
	if err != nil {
		return err
	}
	err = c.api.ContainerRemove(ctx, real, container.RemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
	if err != nil {
		return fmt.Errorf("remove container: %w", err)
	}
	c.log.Info("container removed", "id", short(real))
	return nil
}

// Logs returns the last n lines. Docker multiplexes stdout and stderr on one
// stream when the container has no TTY, so the frames have to be demultiplexed
// or the output arrives with 8-byte binary headers embedded in it.
func (c *Client) Logs(ctx context.Context, id string, tail int) (string, error) {
	real, err := c.requireManaged(ctx, id)
	if err != nil {
		return "", err
	}
	if tail <= 0 || tail > 5000 {
		tail = 200
	}
	rc, err := c.api.ContainerLogs(ctx, real, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
		Timestamps: false,
	})
	if err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	defer rc.Close()

	insp, err := c.api.ContainerInspect(ctx, real)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	if insp.Config != nil && insp.Config.Tty {
		if _, err := io.Copy(&sb, io.LimitReader(rc, 4<<20)); err != nil {
			return sb.String(), nil
		}
	} else {
		if _, err := stdcopy.StdCopy(&sb, &sb, io.LimitReader(rc, 4<<20)); err != nil {
			return sb.String(), nil
		}
	}
	return sb.String(), nil
}

// StreamLogs follows a container's output, calling emit for each line until the
// context is cancelled.
func (c *Client) StreamLogs(ctx context.Context, id string, emit func(string)) error {
	real, err := c.requireManaged(ctx, id)
	if err != nil {
		return err
	}
	rc, err := c.api.ContainerLogs(ctx, real, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true, Tail: "50",
	})
	if err != nil {
		return fmt.Errorf("follow logs: %w", err)
	}
	defer rc.Close()

	insp, _ := c.api.ContainerInspect(ctx, real)
	tty := insp.Config != nil && insp.Config.Tty

	pr, pw := io.Pipe()
	go func() {
		var cerr error
		if tty {
			_, cerr = io.Copy(pw, rc)
		} else {
			_, cerr = stdcopy.StdCopy(pw, pw, rc)
		}
		pw.CloseWithError(cerr)
	}()

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		emit(sc.Text())
	}
	return sc.Err()
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
