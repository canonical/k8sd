package etcd

import (
	"context"
	"errors"
	"fmt"

	pkiutil "github.com/canonical/k8sd/pkg/utils/pki"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// ErrNotFound is returned when an etcd member is not found.
var ErrNotFound = errors.New("etcd member not found")

type Client struct {
	*clientv3.Client
	pkiDir string
}

func NewClient(pkiDir string, endpoints []string) (*Client, error) {
	certFile := fmt.Sprintf("%s/server.crt", pkiDir)
	keyFile := fmt.Sprintf("%s/server.key", pkiDir)
	caFile := fmt.Sprintf("%s/ca.crt", pkiDir)

	tlsConfig, err := pkiutil.LoadTLSConfigFromPath(certFile, keyFile, caFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS config: %w", err)
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
		TLS:       tlsConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	return &Client{
		Client: client,
		pkiDir: pkiDir,
	}, nil
}

// MoveLeaderIfNeeded transfers etcd leadership away from nodeName if it is currently the etcd leader.
// MoveLeader must be issued to the leader itself, so this creates a targeted client directly to that
// node. Returns nil if the node is not found or it is not the current leader.
func (c *Client) MoveLeaderIfNeeded(ctx context.Context, nodeName string) error {
	return c.moveLeader(ctx, nodeName, NewClient)
}

func (c *Client) moveLeader(ctx context.Context, nodeName string, createClient func(string, []string) (*Client, error)) error {
	memberResp, err := c.MemberList(ctx)
	if err != nil {
		return fmt.Errorf("failed to list etcd members: %w", err)
	}

	var targetID uint64
	var targetClientURLs []string
	for _, m := range memberResp.Members {
		if m.Name == nodeName {
			targetID = m.ID
			targetClientURLs = m.ClientURLs
			break
		}
	}
	if targetID == 0 {
		return nil
	}

	// Query Status from the target node to check if it is the current leader.
	var leaderID uint64
	for _, url := range targetClientURLs {
		statusResp, err := c.Status(ctx, url)
		if err == nil {
			leaderID = statusResp.Leader
			break
		}
	}
	if leaderID == 0 {
		return fmt.Errorf("failed to determine etcd leader via status calls to %q", nodeName)
	}
	if leaderID != targetID {
		return nil
	}

	// Try each non-learner, non-target voter as the transfer target, in case a
	// stale member entry points at a stopped or lagging voter that can't accept
	// the transfer.
	var transfereeIDs []uint64
	for _, m := range memberResp.Members {
		if m.ID != targetID && !m.IsLearner {
			transfereeIDs = append(transfereeIDs, m.ID)
		}
	}
	if len(transfereeIDs) == 0 {
		return fmt.Errorf("no eligible etcd member to transfer leadership to")
	}

	// MoveLeader must be called from the leader; dial it directly.
	leaderClient, err := createClient(c.pkiDir, targetClientURLs)
	if err != nil {
		return fmt.Errorf("failed to create etcd client for leader: %w", err)
	}
	defer leaderClient.Close()

	var lastErr error
	for _, transfereeID := range transfereeIDs {
		if _, err := leaderClient.MoveLeader(ctx, transfereeID); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	return fmt.Errorf("failed to transfer etcd leadership from %q to any of %d eligible voters: %w", nodeName, len(transfereeIDs), lastErr)
}

func (c *Client) RemoveNodeByName(ctx context.Context, name string) error {
	resp, err := c.MemberList(ctx)
	if err != nil {
		return fmt.Errorf("failed to list etcd members: %w", err)
	}

	// Find the member with the matching name
	var memberID uint64
	for _, m := range resp.Members {
		if m.Name == name {
			memberID = m.ID
			break
		}
	}

	if memberID == 0 {
		// Member not found.
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}

	if _, err = c.MemberRemove(ctx, memberID); err != nil {
		return fmt.Errorf("failed to remove etcd member: %w", err)
	}

	return nil
}
