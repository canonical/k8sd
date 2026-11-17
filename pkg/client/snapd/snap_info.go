package snapd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type SnapInfoResult struct {
	InstallDate time.Time `json:"install-date"`
	Revision    string    `json:"revision"`
	Version     string    `json:"version"`
}

type SnapInfoResponse struct {
	StatusCode int            `json:"status-code"`
	Result     SnapInfoResult `json:"result"`
}

// GetSnapInfo retrieves information about a snap from the snapd API.
func (c *Client) GetSnapInfo(snap string) (*SnapInfoResponse, error) {
	return c.GetSnapInfoWithContext(context.Background(), snap)
}

// GetSnapInfoWithContext retrieves information about a snap from the snapd API using ctx.
func (c *Client) GetSnapInfoWithContext(ctx context.Context, snap string) (*SnapInfoResponse, error) {
	if snap == "" {
		return nil, fmt.Errorf("snap name must not be empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost/v2/snaps/%s", url.PathEscape(snap)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build snapd snap info request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapd snap info: %w", err)
	}
	defer resp.Body.Close()

	resBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("client: could not read response body: %w", err)
	}

	var snapInfoResponse SnapInfoResponse
	if err := json.Unmarshal(resBody, &snapInfoResponse); err != nil {
		return nil, fmt.Errorf("client: could not unmarshal response body: %w", err)
	}

	return &snapInfoResponse, nil
}

// HasInstallDate checks if the InstallDate field is set in the SnapInfoResult.
// It returns true if the InstallDate is not zero, indicating that the snap is installed.
func (s SnapInfoResponse) HasInstallDate() bool {
	return !s.Result.InstallDate.IsZero()
}
