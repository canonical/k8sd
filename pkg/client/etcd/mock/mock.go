// Package mock provides an in-memory etcd membership for tests.
package mock

import (
	"context"
	"slices"
	"sync"

	"github.com/canonical/k8sd/pkg/client/etcd"
	pb "go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Cluster is an in-memory etcd membership. It implements the membership
// calls of clientv3.Cluster and the transactions of clientv3.KV used as
// barriers; other methods panic.
type Cluster struct {
	clientv3.Cluster
	clientv3.KV

	mu sync.Mutex
	// Members is the current membership.
	Members []*pb.Member
	// Late is appended to Members by the MemberList call that follows
	// LateAfterLists earlier MemberList calls. It models a member add that
	// commits after its caller got an error.
	Late           []*pb.Member
	LateAfterLists int
	// AddErr, if set, is returned by MemberAddAsLearner without adding.
	AddErr error
	// RemoveErrs are returned, in order, by the first MemberRemove calls,
	// without removing.
	RemoveErrs []error
	// RemovedIDs records the IDs passed to MemberRemove that were removed.
	RemovedIDs []uint64
	// Barriers counts committed transactions.
	Barriers int

	lists  int
	nextID uint64
}

// NewClient returns an etcd client backed by c.
func NewClient(c *Cluster) *etcd.Client {
	client := clientv3.NewCtxClient(context.Background())
	client.Cluster = c
	client.KV = c
	return &etcd.Client{Client: client}
}

func (c *Cluster) MemberList(_ context.Context, _ ...clientv3.OpOption) (*clientv3.MemberListResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lists++
	if c.lists > c.LateAfterLists && len(c.Late) > 0 {
		c.Members = append(c.Members, c.Late...)
		c.Late = nil
	}
	return &clientv3.MemberListResponse{Members: slices.Clone(c.Members)}, nil
}

func (c *Cluster) MemberAddAsLearner(_ context.Context, peerURLs []string) (*clientv3.MemberAddResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.AddErr != nil {
		return nil, c.AddErr
	}
	for _, m := range c.Members {
		for _, u := range peerURLs {
			if slices.Contains(m.PeerURLs, u) {
				return nil, rpctypes.ErrPeerURLExist
			}
		}
	}
	c.nextID++
	m := &pb.Member{ID: 0x1000 + c.nextID, PeerURLs: peerURLs, IsLearner: true}
	c.Members = append(c.Members, m)
	return &clientv3.MemberAddResponse{Member: m, Members: slices.Clone(c.Members)}, nil
}

func (c *Cluster) MemberRemove(_ context.Context, id uint64) (*clientv3.MemberRemoveResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.RemoveErrs) > 0 {
		err := c.RemoveErrs[0]
		c.RemoveErrs = c.RemoveErrs[1:]
		return nil, err
	}
	i := slices.IndexFunc(c.Members, func(m *pb.Member) bool { return m.ID == id })
	if i < 0 {
		return nil, rpctypes.ErrMemberNotFound
	}
	c.Members = slices.Delete(c.Members, i, i+1)
	c.RemovedIDs = append(c.RemovedIDs, id)
	return &clientv3.MemberRemoveResponse{Members: slices.Clone(c.Members)}, nil
}

func (c *Cluster) Txn(_ context.Context) clientv3.Txn {
	return &txn{c: c}
}

// txn is a transaction whose Commit counts a barrier and changes nothing.
type txn struct{ c *Cluster }

func (t *txn) If(...clientv3.Cmp) clientv3.Txn  { return t }
func (t *txn) Then(...clientv3.Op) clientv3.Txn { return t }
func (t *txn) Else(...clientv3.Op) clientv3.Txn { return t }

func (t *txn) Commit() (*clientv3.TxnResponse, error) {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()

	t.c.Barriers++
	return &clientv3.TxnResponse{}, nil
}

// Member returns the member with the given peer URL, or nil.
func (c *Cluster) Member(peerURL string) *pb.Member {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, m := range c.Members {
		if slices.Contains(m.PeerURLs, peerURL) {
			return m
		}
	}
	return nil
}
