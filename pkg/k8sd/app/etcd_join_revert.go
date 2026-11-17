package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	etcdclient "github.com/canonical/k8sd/pkg/client/etcd"
	"github.com/canonical/k8sd/pkg/log"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
)

const (
	// DefaultEtcdJoinRevertRemoveTimeout bounds how long RevertEtcdJoin keeps
	// retrying to remove a joiner that may already be an etcd voter.
	//
	// With strict reconfiguration (the etcd default) the serving member rejects
	// removing a voter with ErrUnhealthy until it has been connected to enough
	// voters for HealthInterval (5s). A joiner that was promoted a moment ago is
	// not counted yet, so its removal is rejected even when every member is
	// healthy. 30s covers several health intervals plus a leader election.
	DefaultEtcdJoinRevertRemoveTimeout = 30 * time.Second
	// etcdJoinRevertTimeout bounds the whole etcd join revert: the removal
	// retries plus stopping the local etcd service afterwards.
	etcdJoinRevertTimeout = DefaultEtcdJoinRevertRemoveTimeout + 30*time.Second

	defaultEtcdJoinRevertRetryInitial = 500 * time.Millisecond
	defaultEtcdJoinRevertRetryMax     = 4 * time.Second
	// etcdJoinRevertAttemptTimeout bounds a single MemberRemove call, so a
	// request stuck on a lost leader does not consume the whole retry budget.
	etcdJoinRevertAttemptTimeout = 5 * time.Second
)

// EtcdMemberRemover removes the joining member from the etcd cluster.
type EtcdMemberRemover interface {
	RemoveMember(ctx context.Context) error
}

// EtcdMemberRemoverFunc adapts a function to EtcdMemberRemover.
type EtcdMemberRemoverFunc func(ctx context.Context) error

func (f EtcdMemberRemoverFunc) RemoveMember(ctx context.Context) error { return f(ctx) }

// EtcdStopper stops the local etcd service.
type EtcdStopper interface {
	StopEtcd(ctx context.Context) error
}

// EtcdStopperFunc adapts a function to EtcdStopper.
type EtcdStopperFunc func(ctx context.Context) error

func (f EtcdStopperFunc) StopEtcd(ctx context.Context) error { return f(ctx) }

// DirRemover deletes the local etcd data directory.
type DirRemover interface {
	RemoveDir() error
}

// DirRemoverFunc adapts a function to DirRemover.
type DirRemoverFunc func() error

func (f DirRemoverFunc) RemoveDir() error { return f() }

// Clock is the time source used between removal attempts.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// EtcdJoinRevert describes the etcd state to undo after a failed control plane join.
type EtcdJoinRevert struct {
	// MemberID is the etcd member ID of the joiner, used in logs and in the
	// manual remediation hint.
	MemberID uint64
	// MayBeVoter is true once a promotion of the joiner was attempted, because
	// MemberPromote can commit even when it returns an error.
	MayBeVoter bool
	// EtcdStarted is true once the local etcd service was started.
	EtcdStarted bool

	// RemoveVoter removes a joiner that may be a voter. It is retried until it
	// succeeds, returns "member not found", or RemoveTimeout elapses. A nil
	// RemoveVoter means the cluster cannot be reached.
	RemoveVoter EtcdMemberRemover
	// RemoveLearner removes a joiner that was never promoted. It is called once,
	// after the local etcd was stopped. A nil RemoveLearner skips the removal.
	RemoveLearner EtcdMemberRemover
	StopEtcd      EtcdStopper
	RemoveDir     DirRemover

	Log log.Logger

	// Optional; zero values select the defaults.
	Clock         Clock
	RemoveTimeout time.Duration
	RetryInitial  time.Duration
	RetryMax      time.Duration
}

// RevertEtcdJoin undoes the etcd part of a failed control plane join.
//
// A learner does not count toward quorum, so its local etcd is stopped first,
// then the learner is removed from the cluster and the data directory deleted.
//
// A member that may be a voter is removed from the cluster FIRST while its
// local etcd keeps running. Stopping a voter that is still part of the cluster
// lowers the number of live voters and loses quorum when going from 1 to 2
// members, or when another voter is down. Local etcd is therefore only stopped,
// and its data only deleted, after the removal succeeded. If it never succeeds,
// etcd is left running with its data intact and an error with the manual
// remediation is logged.
func RevertEtcdJoin(ctx context.Context, r EtcdJoinRevert) {
	if r.MayBeVoter {
		revertEtcdVoterJoin(ctx, r)
		return
	}

	if r.EtcdStarted {
		if err := r.StopEtcd.StopEtcd(ctx); err != nil {
			r.Log.Error(err, "Failed to stop etcd during join revert")
		}
	}
	if r.RemoveLearner == nil {
		r.Log.Info("Skipping etcd member removal: no client endpoints available")
	} else if err := r.RemoveLearner.RemoveMember(ctx); err != nil {
		r.Log.Error(err, "Failed to remove etcd member")
	}
	if err := r.RemoveDir.RemoveDir(); err != nil {
		r.Log.Error(err, "Failed to cleanup etcd state directory")
	}
}

func revertEtcdVoterJoin(ctx context.Context, r EtcdJoinRevert) {
	if err := removeEtcdMemberWithRetry(ctx, r); err != nil {
		r.Log.Error(err, "Failed to remove etcd member that may be a voter; leaving local etcd running and its data in place to preserve quorum. "+
			"Remove the member manually once the etcd cluster is healthy, then stop etcd and delete the etcd data directory on this node",
			"remediation", fmt.Sprintf("etcdctl member remove %x", r.MemberID))
		return
	}

	if r.EtcdStarted {
		if err := r.StopEtcd.StopEtcd(ctx); err != nil {
			r.Log.Error(err, "Failed to stop etcd during join revert")
		}
	}
	if err := r.RemoveDir.RemoveDir(); err != nil {
		r.Log.Error(err, "Failed to cleanup etcd state directory")
	}
}

// removeEtcdMemberWithRetry retries r.RemoveVoter with exponential backoff
// until it succeeds, the member is reported as not found (already removed),
// r.RemoveTimeout elapses or ctx is done.
func removeEtcdMemberWithRetry(ctx context.Context, r EtcdJoinRevert) error {
	if r.RemoveVoter == nil {
		return errors.New("no etcd client endpoints available")
	}

	clock := r.Clock
	if clock == nil {
		clock = realClock{}
	}
	timeout := r.RemoveTimeout
	if timeout == 0 {
		timeout = DefaultEtcdJoinRevertRemoveTimeout
	}
	delay := r.RetryInitial
	if delay == 0 {
		delay = defaultEtcdJoinRevertRetryInitial
	}
	maxDelay := r.RetryMax
	if maxDelay == 0 {
		maxDelay = defaultEtcdJoinRevertRetryMax
	}

	deadline := clock.Now().Add(timeout)
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, etcdJoinRevertAttemptTimeout)
		err := r.RemoveVoter.RemoveMember(attemptCtx)
		cancel()
		switch {
		case err == nil:
			r.Log.Info("Removed etcd member", "attempt", attempt)
			return nil
		case errors.Is(err, rpctypes.ErrMemberNotFound) || errors.Is(err, etcdclient.ErrNotFound):
			r.Log.Info("Etcd member not found, treating it as already removed", "attempt", attempt)
			return nil
		}

		if remaining := deadline.Sub(clock.Now()); remaining <= 0 {
			return fmt.Errorf("gave up after %d attempts in %s: %w", attempt, timeout, err)
		} else if delay > remaining {
			delay = remaining
		}
		r.Log.Info("Etcd member removal failed, retrying", "attempt", attempt, "retryIn", delay, "error", err.Error())

		select {
		case <-ctx.Done():
			return fmt.Errorf("gave up after %d attempts: %w (last error: %w)", attempt, ctx.Err(), err)
		case <-clock.After(delay):
		}
		delay = min(delay*2, maxDelay)
	}
}
