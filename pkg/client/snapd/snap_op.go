package snapd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// RefreshOptions configures a store-backed snap refresh operation.
type RefreshOptions struct {
	Channel  string `json:"channel,omitempty"`
	Revision string `json:"revision,omitempty"`
	Amend    bool   `json:"amend,omitempty"`
}

type snapActionRequest struct {
	Action string `json:"action"`
	RefreshOptions
}

// InstallPathOptions configures sideloading a local .snap archive.
type InstallPathOptions struct {
	Name      string
	Classic   bool
	Dangerous bool
}

// Refresh initiates refreshing an installed snap and returns the snapd change ID.
func (c *Client) Refresh(ctx context.Context, snapName string, opts RefreshOptions) (string, error) {
	if snapName == "" {
		return "", fmt.Errorf("snap name must not be empty")
	}

	payload, err := json.Marshal(snapActionRequest{
		Action:         "refresh",
		RefreshOptions: opts,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal refresh request: %w", err)
	}

	path := fmt.Sprintf("/v2/snaps/%s", url.PathEscape(snapName))
	return c.doAsync(ctx, http.MethodPost, path, "application/json", bytes.NewReader(payload))
}

// InstallPath sideloads a local .snap file via multipart upload and returns the snapd change ID.
func (c *Client) InstallPath(ctx context.Context, snapPath string, opts InstallPathOptions) (string, error) {
	if snapPath == "" {
		return "", fmt.Errorf("snap path must not be empty")
	}

	f, err := os.Open(snapPath)
	if err != nil {
		return "", fmt.Errorf("cannot open %q: %w", snapPath, err)
	}

	pr, pw := io.Pipe()
	defer pr.Close()

	mw := multipart.NewWriter(pw)
	go func() {
		defer f.Close()

		writeErr := func() error {
			if err := mw.WriteField("action", "install"); err != nil {
				return err
			}
			if opts.Name != "" {
				if err := mw.WriteField("name", opts.Name); err != nil {
					return err
				}
			}
			if err := mw.WriteField("snap-path", snapPath); err != nil {
				return err
			}
			if opts.Classic {
				if err := mw.WriteField("classic", "true"); err != nil {
					return err
				}
			}
			if opts.Dangerous {
				if err := mw.WriteField("dangerous", "true"); err != nil {
					return err
				}
			}

			fw, err := mw.CreateFormFile("snap", filepath.Base(snapPath))
			if err != nil {
				return err
			}
			if _, err := io.Copy(fw, f); err != nil {
				return err
			}
			return mw.Close()
		}()
		if writeErr != nil {
			_ = pw.CloseWithError(writeErr)
			return
		}
		_ = pw.Close()
	}()

	return c.doAsync(ctx, http.MethodPost, "/v2/snaps", mw.FormDataContentType(), pr)
}
