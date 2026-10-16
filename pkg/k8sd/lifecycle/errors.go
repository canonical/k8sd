package lifecycle

import (
	"errors"
	"fmt"
)

// ErrBlocked marks a transition whose guard is currently false. Blocked
// transitions are safe to retry; the state did not change.
var ErrBlocked = errors.New("transition blocked")

var (
	ErrNoEtcdQuorum   = fmt.Errorf("%w: etcd has no quorum", ErrBlocked)
	ErrNoDqliteQuorum = fmt.Errorf("%w: dqlite has no quorum", ErrBlocked)
	ErrQuorumAtRisk   = fmt.Errorf("%w: membership change would lose quorum", ErrBlocked)
	ErrNodeDown       = fmt.Errorf("%w: node is down or unreachable", ErrBlocked)
	ErrLearnerBusy    = fmt.Errorf("%w: another etcd learner exists", ErrBlocked)
	ErrAwaitingMember = fmt.Errorf("%w: waiting for microcluster membership", ErrBlocked)
)

var (
	ErrLeaseHeld        = errors.New("another node transition is in progress")
	ErrConflict         = errors.New("journal compare-and-swap conflict")
	ErrInvalidPhase     = errors.New("request is invalid in the node's current phase")
	ErrLastControlPlane = errors.New("refusing to remove the last control-plane node")
	ErrLeaseLost        = errors.New("transition is not owned by the journal lease")
)

type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent marks err as non-retryable. Reconcile returns it immediately,
// and Join rolls back instead of waiting for the context deadline.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err was marked with Permanent.
func IsPermanent(err error) bool {
	var p *permanentError
	return errors.As(err, &p)
}
