package snapd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/canonical/k8sd/pkg/k8sd/types"
)

// GetRefreshStatus fetches the status of a snapd change.
func (c *Client) GetRefreshStatus(changeID string) (*types.RefreshStatus, error) {
	return c.GetRefreshStatusWithContext(context.Background(), changeID)
}

// GetRefreshStatusWithContext fetches the status of a snapd change using ctx.
func (c *Client) GetRefreshStatusWithContext(ctx context.Context, changeID string) (*types.RefreshStatus, error) {
	if changeID == "" {
		return nil, fmt.Errorf("change ID must not be empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost/v2/changes/%s", url.PathEscape(changeID)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build snapd change request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapd change status: %w", err)
	}
	defer resp.Body.Close()

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("client: could not read response body: %w", err)
	}

	var env apiEnvelope
	if err := json.Unmarshal(resBody, &env); err != nil {
		return nil, fmt.Errorf("client: could not unmarshal response body: %w", err)
	}
	if env.Type == "error" || resp.StatusCode >= http.StatusBadRequest {
		return nil, formatAPIError(resp.StatusCode, env.Result)
	}
	if env.Type != "sync" {
		return nil, fmt.Errorf("expected sync response for change %q, got %q", changeID, env.Type)
	}

	var status types.RefreshStatus
	if err := json.Unmarshal(env.Result, &status); err != nil {
		return nil, fmt.Errorf("client: could not unmarshal change result: %w", err)
	}

	return &status, nil
}

// WaitChange polls snapd until the given change completes or ctx is cancelled.
func (c *Client) WaitChange(ctx context.Context, changeID string) error {
	check := func() (bool, error) {
		status, err := c.GetRefreshStatusWithContext(ctx, changeID)
		if err != nil {
			return false, err
		}
		if !status.Ready {
			return false, nil
		}
		if status.Err != "" {
			return true, fmt.Errorf("snapd change %s failed: %s", changeID, status.Err)
		}
		return true, nil
	}

	if done, err := check(); err != nil || done {
		return err
	}

	interval := c.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if done, err := check(); err != nil || done {
				return err
			}
		}
	}
}
