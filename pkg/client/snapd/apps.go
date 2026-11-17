package snapd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// StartOptions configures a service start operation.
type StartOptions struct {
	Enable bool
}

// StopOptions configures a service stop operation.
type StopOptions struct {
	Disable bool
}

// RestartOptions configures a service restart operation.
type RestartOptions struct {
	Reload bool
}

type appInstruction struct {
	Action  string   `json:"action"`
	Names   []string `json:"names"`
	Enable  bool     `json:"enable,omitempty"`
	Disable bool     `json:"disable,omitempty"`
	Reload  bool     `json:"reload,omitempty"`
}

// StartServices initiates starting the given snap services and returns the snapd change ID.
func (c *Client) StartServices(ctx context.Context, names []string, opts StartOptions) (string, error) {
	return c.postAppInstruction(ctx, appInstruction{
		Action: "start",
		Names:  names,
		Enable: opts.Enable,
	})
}

// StopServices initiates stopping the given snap services and returns the snapd change ID.
func (c *Client) StopServices(ctx context.Context, names []string, opts StopOptions) (string, error) {
	return c.postAppInstruction(ctx, appInstruction{
		Action:  "stop",
		Names:   names,
		Disable: opts.Disable,
	})
}

// RestartServices initiates restarting the given snap services and returns the snapd change ID.
func (c *Client) RestartServices(ctx context.Context, names []string, opts RestartOptions) (string, error) {
	return c.postAppInstruction(ctx, appInstruction{
		Action: "restart",
		Names:  names,
		Reload: opts.Reload,
	})
}

func (c *Client) postAppInstruction(ctx context.Context, inst appInstruction) (string, error) {
	if len(inst.Names) == 0 {
		return "", fmt.Errorf("service names must not be empty")
	}

	payload, err := json.Marshal(inst)
	if err != nil {
		return "", fmt.Errorf("failed to marshal app instruction: %w", err)
	}

	return c.doAsync(ctx, http.MethodPost, "/v2/apps", "application/json", bytes.NewReader(payload))
}
