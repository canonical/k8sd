package snapd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	defaultSocketPath   = "/run/snapd.socket"
	defaultPollInterval = 250 * time.Millisecond
)

// Client communicates with the local snapd REST API over its Unix socket.
type Client struct {
	client       *http.Client
	pollInterval time.Duration
}

// NewClient creates a snapd client connected to the default snapd Unix socket.
func NewClient() (*Client, error) {
	return NewClientWithSocket(defaultSocketPath)
}

// NewClientWithSocket creates a snapd client connected to the given Unix socket path.
func NewClientWithSocket(socketPath string) (*Client, error) {
	if socketPath == "" {
		socketPath = defaultSocketPath
	}

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("http.DefaultTransport is not a *http.Transport")
	}

	unixTransport := defaultTransport.Clone()
	defaultDialContext := unixTransport.DialContext

	unixTransport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return defaultDialContext(ctx, "unix", socketPath)
	}

	return &Client{
		client:       &http.Client{Transport: unixTransport},
		pollInterval: defaultPollInterval,
	}, nil
}

// SetPollInterval overrides the polling interval used by WaitChange.
func (c *Client) SetPollInterval(d time.Duration) {
	if d > 0 {
		c.pollInterval = d
	}
}

type apiError struct {
	Message string `json:"message"`
	Kind    string `json:"kind"`
}

type apiEnvelope struct {
	Type       string          `json:"type"`
	StatusCode int             `json:"status-code"`
	Change     string          `json:"change"`
	Result     json.RawMessage `json:"result"`
}

func formatAPIError(statusCode int, result json.RawMessage) error {
	var apiErr apiError
	if len(result) > 0 && json.Unmarshal(result, &apiErr) == nil && apiErr.Message != "" {
		if apiErr.Kind != "" {
			return fmt.Errorf("snapd error (%s): %s", apiErr.Kind, apiErr.Message)
		}
		return fmt.Errorf("snapd error: %s", apiErr.Message)
	}
	return fmt.Errorf("snapd HTTP %d: %s", statusCode, http.StatusText(statusCode))
}

func (c *Client) doAsync(ctx context.Context, method, path, contentType string, body io.Reader) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, body)
	if err != nil {
		return "", fmt.Errorf("failed to build snapd request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("snapd request %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("client: could not read response body: %w", err)
	}

	var env apiEnvelope
	if err := json.Unmarshal(resBody, &env); err != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			return "", fmt.Errorf("snapd HTTP %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
		return "", fmt.Errorf("client: could not unmarshal response body: %w", err)
	}

	if env.Type == "error" || resp.StatusCode >= http.StatusBadRequest {
		return "", formatAPIError(resp.StatusCode, env.Result)
	}
	if resp.StatusCode != http.StatusAccepted || env.Type != "async" {
		return "", fmt.Errorf("expected async 202 response for %s %s, got HTTP %d (type %q)", method, path, resp.StatusCode, env.Type)
	}
	if env.Change == "" {
		return "", fmt.Errorf("snapd async response for %s %s missing change ID", method, path)
	}

	return env.Change, nil
}
