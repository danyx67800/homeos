// Package dockerx wraps the Docker Engine API with the operations HomeOS needs
// and the guard rails it wants.
//
// The guard rail that matters: every mutating call checks that the target
// carries the homeos.managed label before touching it. The daemon has full
// Docker access, so without that check a bug or a crafted request could stop a
// container the user runs by hand, or one belonging to another tool entirely.
package dockerx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

var (
	ErrNotManaged = errors.New("container is not managed by HomeOS")
	ErrNotFound   = errors.New("container not found")
)

const (
	LabelManaged = "homeos.managed"
	LabelApp     = "homeos.app"
	LabelEnable  = "homeos.enable"
	LabelTitle   = "homeos.title"
	LabelRole    = "homeos.role"
	LabelIcon    = "homeos.icon"
	LabelPort    = "homeos.port"
	LabelRoute   = "homeos.route"
)

type Client struct {
	api  *client.Client
	log  *slog.Logger
	edge string
	// unavailable is non-nil when the daemon could not be reached at start-up.
	unavailable error
}

func New(socket, edgeNetwork string, log *slog.Logger) (*Client, error) {
	host := socket
	if !strings.Contains(host, "://") {
		host = "unix://" + host
	}
	api, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to Docker at %s: %w", socket, err)
	}
	return &Client{api: api, log: log, edge: edgeNetwork}, nil
}

func (c *Client) Close() error {
	if c.api == nil {
		return nil
	}
	return c.api.Close()
}

// Ping reports whether the daemon is reachable, for the health endpoint.
func (c *Client) Ping(ctx context.Context) error {
	if !c.Available() {
		return c.unavailableErr()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.api.Ping(ctx)
	return err
}

func (c *Client) Version(ctx context.Context) (string, error) {
	if !c.Available() {
		return "", c.unavailableErr()
	}
	v, err := c.api.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// Container is the flattened view the API serves.
type Container struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	App      string            `json:"app,omitempty"`
	Title    string            `json:"title,omitempty"`
	Image    string            `json:"image"`
	State    string            `json:"state"`  // running | exited | ...
	Status   string            `json:"status"` // "Up 3 hours"
	Health   string            `json:"health,omitempty"`
	Created  time.Time         `json:"created"`
	Managed  bool              `json:"managed"`
	Role     string            `json:"role,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Networks []string          `json:"networks,omitempty"`
	Ports    []string          `json:"ports,omitempty"`
}

// List returns containers, newest first. managedOnly hides anything the user
// started by hand, which is the default view in the dashboard.
func (c *Client) List(ctx context.Context, managedOnly bool) ([]Container, error) {
	if !c.Available() {
		return nil, c.unavailableErr()
	}
	opts := container.ListOptions{All: true}
	if managedOnly {
		f := filters.NewArgs()
		f.Add("label", LabelManaged+"=true")
		opts.Filters = f
	}
	raw, err := c.api.ContainerList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		out = append(out, flatten(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func flatten(r types.Container) Container {
	name := ""
	if len(r.Names) > 0 {
		name = strings.TrimPrefix(r.Names[0], "/")
	}
	var nets []string
	if r.NetworkSettings != nil {
		for n := range r.NetworkSettings.Networks {
			nets = append(nets, n)
		}
		sort.Strings(nets)
	}
	var ports []string
	for _, p := range r.Ports {
		if p.PublicPort != 0 {
			ports = append(ports, fmt.Sprintf("%d:%d/%s", p.PublicPort, p.PrivatePort, p.Type))
		}
	}
	sort.Strings(ports)

	return Container{
		ID:       r.ID,
		Name:     name,
		App:      r.Labels[LabelApp],
		Title:    r.Labels[LabelTitle],
		Image:    r.Image,
		State:    r.State,
		Status:   r.Status,
		Health:   healthFromStatus(r.Status),
		Created:  time.Unix(r.Created, 0),
		Managed:  r.Labels[LabelManaged] == "true",
		Role:     r.Labels[LabelRole],
		Labels:   r.Labels,
		Networks: nets,
		Ports:    ports,
	}
}

// healthFromStatus extracts the health word Docker appends to the status
// string, e.g. "Up 2 hours (healthy)". The list endpoint does not expose the
// structured health state, and inspecting every container to get it would turn
// one API call into N.
func healthFromStatus(status string) string {
	for _, h := range []string{"healthy", "unhealthy", "starting"} {
		if strings.Contains(status, "("+h+")") {
			return h
		}
	}
	return ""
}

var _ = io.Discard
var _ = network.EndpointSettings{}
