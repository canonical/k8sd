package etcd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// ErrNotLearner is returned when the etcd member matched by peer URL is a voting member.
var ErrNotLearner = errors.New("etcd member is not a learner")

// barrierKey is only named by the barrier transaction; it is never written.
const barrierKey = "/k8sd/membership-barrier"

// learnerSweepInterval is the delay between sweeps in SweepLearnerByPeerURL.
const learnerSweepInterval = 250 * time.Millisecond

// learnerSweepAttemptTimeout bounds each sweep in SweepLearnerByPeerURL. etcd
// silently drops a membership change proposed while another one is pending,
// and its caller then waits until its context expires; bounding each sweep
// lets the next one retry.
const learnerSweepAttemptTimeout = 5 * time.Second

// AddLearnerResult is the outcome of AddLearner.
type AddLearnerResult struct {
	// Member is the learner for the requested peer URL.
	Member *pb.Member
	// Members is the cluster membership including Member.
	Members []*pb.Member
	// Adopted is true when Member already existed and was not added by this call.
	Adopted bool
}

// AddLearner adds a learner member with the given peer URL.
//
// If etcd rejects the add because a member with this peer URL already exists
// (ErrPeerURLExist, or ErrMemberExist when the earlier add happened within the
// same second), and that member is a learner whose etcd has never started
// (empty name), it is adopted: it is exactly what this call would have created.
// That happens when an earlier add returned an error but committed anyway.
// Any other existing member is left alone and the rejection is returned.
func (c *Client) AddLearner(ctx context.Context, peerURL string) (*AddLearnerResult, error) {
	resp, err := c.MemberAddAsLearner(ctx, []string{peerURL})
	if err == nil {
		return &AddLearnerResult{Member: resp.Member, Members: resp.Members}, nil
	}
	if !errors.Is(err, rpctypes.ErrPeerURLExist) && !errors.Is(err, rpctypes.ErrMemberExist) {
		return nil, err
	}

	members, listErr := c.MemberList(ctx)
	if listErr != nil {
		return nil, errors.Join(err, fmt.Errorf("failed to list etcd members: %w", listErr))
	}
	m := memberByPeerURL(members.Members, peerURL)
	switch {
	case m == nil:
		return nil, err
	case !m.IsLearner:
		return nil, fmt.Errorf("peer URL %s belongs to voting etcd member %x: %w", peerURL, m.ID, err)
	case m.Name != "":
		// A learner that has started before has raft state that a fresh
		// data directory cannot resume; it must be removed and re-added.
		return nil, fmt.Errorf("peer URL %s belongs to etcd learner %x (%q) that has already started: %w", peerURL, m.ID, m.Name, err)
	}
	return &AddLearnerResult{Member: m, Members: members.Members, Adopted: true}, nil
}

// IsMemberAddRejected reports whether err from a member add is a definitive
// rejection, meaning the member was not and will not be added: either etcd
// refused before proposing it, or the change committed and was rejected when
// applied. Any other error (timeouts, cancellation, leader changes, transport
// errors) leaves the outcome unknown; the add may still commit later.
func IsMemberAddRejected(err error) bool {
	for _, rejection := range []error{
		rpctypes.ErrUnhealthy,
		rpctypes.ErrMemberNotEnoughStarted,
		rpctypes.ErrTooManyLearners,
		rpctypes.ErrPeerURLExist,
		rpctypes.ErrMemberExist,
		rpctypes.ErrMemberNotFound,
	} {
		if errors.Is(err, rejection) {
			return true
		}
	}
	return false
}

// RemoveLearnerByPeerURL removes the learner member with the given peer URL.
// It finds members by peer URL because a learner whose etcd never started has
// no name. It returns ErrNotFound if no member has that peer URL, and
// ErrNotLearner, without removing anything, if that member is a voter.
func (c *Client) RemoveLearnerByPeerURL(ctx context.Context, peerURL string) error {
	resp, err := c.MemberList(ctx)
	if err != nil {
		return fmt.Errorf("failed to list etcd members: %w", err)
	}
	m := memberByPeerURL(resp.Members, peerURL)
	if m == nil {
		return fmt.Errorf("%w: peer URL %s", ErrNotFound, peerURL)
	}
	if !m.IsLearner {
		return fmt.Errorf("%w: member %x (%q) with peer URL %s", ErrNotLearner, m.ID, m.Name, peerURL)
	}
	if _, err := c.MemberRemove(ctx, m.ID); err != nil && !errors.Is(err, rpctypes.ErrMemberNotFound) {
		return fmt.Errorf("failed to remove etcd learner %x: %w", m.ID, err)
	}
	return nil
}

// SweepLearnerByPeerURL removes the learner with the given peer URL, including
// one added by a MemberAdd that reported an error but commits afterwards.
//
// Each sweep first commits a barrier through raft, so every proposal already in
// the leader's log has been applied before members are listed. A proposal that
// has not reached the leader's log yet is not covered by the barrier, so with a
// non-zero settle the sweep repeats until settle has elapsed. A failed sweep is
// retried until one succeeds or ctx is done.
//
// Voting members are never removed: finding one returns ErrNotLearner.
func (c *Client) SweepLearnerByPeerURL(ctx context.Context, peerURL string, settle time.Duration) error {
	deadline := time.Now().Add(settle)
	for {
		err := c.sweepLearnerByPeerURL(ctx, peerURL)
		if errors.Is(err, ErrNotLearner) {
			return err
		}

		wait := learnerSweepInterval
		if err == nil {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return nil
			}
			wait = min(wait, remaining)
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return err
			}
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (c *Client) sweepLearnerByPeerURL(ctx context.Context, peerURL string) error {
	ctx, cancel := context.WithTimeout(ctx, learnerSweepAttemptTimeout)
	defer cancel()

	if err := c.Barrier(ctx); err != nil {
		return err
	}
	if err := c.RemoveLearnerByPeerURL(ctx, peerURL); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// Barrier commits a no-op write through raft. Raft applies entries in log
// order, so once it returns, every proposal that was in the leader's log
// before it (such as a MemberAdd whose caller gave up) has been applied, and a
// following linearizable read observes it.
//
// The transaction's condition never holds and it has no else branch, so it
// changes no keys. The delete only makes etcd treat it as a write instead of
// a read; unlike a put, it is accepted even when the space quota is exceeded.
func (c *Client) Barrier(ctx context.Context) error {
	_, err := c.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(barrierKey), "<", 0)).
		Then(clientv3.OpDelete(barrierKey)).
		Commit()
	if err != nil {
		return fmt.Errorf("failed to commit etcd barrier: %w", err)
	}
	return nil
}

func memberByPeerURL(members []*pb.Member, peerURL string) *pb.Member {
	for _, m := range members {
		if slices.Contains(m.PeerURLs, peerURL) {
			return m
		}
	}
	return nil
}
