package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type fakeMemberLister struct {
	members []*etcdserverpb.Member
	err     error
}

func (f *fakeMemberLister) MemberList(context.Context, ...clientv3.OpOption) (*clientv3.MemberListResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &clientv3.MemberListResponse{Members: f.members}, nil
}

func TestEtcdVoterWithPeers(t *testing.T) {
	self := &etcdserverpb.Member{ID: 0xc, Name: "node-c"}
	selfLearner := &etcdserverpb.Member{ID: 0xc, Name: "node-c", IsLearner: true}
	peer := &etcdserverpb.Member{ID: 0xa, Name: "node-a"}
	peerLearner := &etcdserverpb.Member{ID: 0xb, Name: "node-b", IsLearner: true}
	unreachable := &fakeMemberLister{err: errors.New("context deadline exceeded")}

	for _, tc := range []struct {
		name         string
		cluster      etcdMemberLister
		local        etcdMemberLister
		expectVoter  bool
		expectErr    bool
		expectMember uint64
	}{
		{name: "VoterWithPeers", cluster: &fakeMemberLister{members: []*etcdserverpb.Member{peer, self}}, expectVoter: true, expectMember: 0xc},
		{name: "AlreadyRemoved", cluster: &fakeMemberLister{members: []*etcdserverpb.Member{peer}}},
		{name: "Learner", cluster: &fakeMemberLister{members: []*etcdserverpb.Member{peer, selfLearner}}},
		{name: "OnlyVoter", local: &fakeMemberLister{members: []*etcdserverpb.Member{self, peerLearner}}},
		{
			name:    "ClusterIsAuthoritative",
			cluster: &fakeMemberLister{members: []*etcdserverpb.Member{peer}},
			local:   &fakeMemberLister{members: []*etcdserverpb.Member{peer, self}},
		},
		{
			name:        "ClusterUnreachableLocalVoter",
			cluster:     unreachable,
			local:       &fakeMemberLister{members: []*etcdserverpb.Member{peer, self}},
			expectVoter: true, expectMember: 0xc,
		},
		{name: "NothingReachable", cluster: unreachable, local: unreachable, expectErr: true},
		{name: "NoEndpoints", expectErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			id, isVoter, err := etcdVoterWithPeers(context.Background(), "node-c", tc.cluster, tc.local)

			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			g.Expect(isVoter).To(Equal(tc.expectVoter))
			if tc.expectVoter {
				g.Expect(id).To(Equal(tc.expectMember))
			}
		})
	}
}

func TestRemoveAllExcept(t *testing.T) {
	g := NewWithT(t)
	dir := t.TempDir()
	for _, name := range []string{"etcd", "kubelet", "kube-apiserver"} {
		g.Expect(os.WriteFile(filepath.Join(dir, name), []byte("--flag=1"), 0o600)).To(Succeed())
	}

	g.Expect(removeAllExcept(dir, "etcd")).To(Succeed())

	entries, err := os.ReadDir(dir)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(entries).To(HaveLen(1))
	g.Expect(entries[0].Name()).To(Equal("etcd"))

	g.Expect(removeAllExcept(filepath.Join(dir, "missing"), "etcd")).To(Succeed())
}
