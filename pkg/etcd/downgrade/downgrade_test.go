package downgrade

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type fakeClient struct {
	downgradeErrs map[clientv3.DowngradeAction]error
	// storageVersions is returned in order by successive Status calls; the last
	// value is repeated once exhausted.
	storageVersions []string
	statusCalls     int
	downgradeCalls  []clientv3.DowngradeAction
}

func (f *fakeClient) Downgrade(_ context.Context, action clientv3.DowngradeAction, _ string) (*clientv3.DowngradeResponse, error) {
	f.downgradeCalls = append(f.downgradeCalls, action)
	if err := f.downgradeErrs[action]; err != nil {
		return nil, err
	}
	return &clientv3.DowngradeResponse{}, nil
}

func (f *fakeClient) Status(_ context.Context, _ string) (*clientv3.StatusResponse, error) {
	i := f.statusCalls
	if i >= len(f.storageVersions) {
		i = len(f.storageVersions) - 1
	}
	f.statusCalls++
	return &clientv3.StatusResponse{StorageVersion: f.storageVersions[i]}, nil
}

func (f *fakeClient) Endpoints() []string { return []string{"https://127.0.0.1:2379"} }
func (f *fakeClient) Close() error        { return nil }

func TestPrepareDowngrade(t *testing.T) {
	target := Version{Major: 3, Minor: 6}

	t.Run("happy path", func(t *testing.T) {
		c := &fakeClient{storageVersions: []string{"3.7.0", "3.6.0"}}
		if err := PrepareDowngrade(context.Background(), logr.Discard(), c, target, time.Minute); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(c.downgradeCalls) != 2 ||
			c.downgradeCalls[0] != clientv3.DowngradeValidate ||
			c.downgradeCalls[1] != clientv3.DowngradeEnable {
			t.Fatalf("expected validate then enable, got %v", c.downgradeCalls)
		}
	})

	t.Run("idempotent when downgrade already in progress", func(t *testing.T) {
		c := &fakeClient{
			downgradeErrs: map[clientv3.DowngradeAction]error{
				clientv3.DowngradeValidate: rpctypes.ErrDowngradeInProcess,
				clientv3.DowngradeEnable:   rpctypes.ErrDowngradeInProcess,
				clientv3.DowngradeCancel:   nil,
			},
			storageVersions: []string{"3.6.0"},
		}
		if err := PrepareDowngrade(context.Background(), logr.Discard(), c, target, time.Minute); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("validate failure aborts", func(t *testing.T) {
		c := &fakeClient{
			downgradeErrs: map[clientv3.DowngradeAction]error{
				clientv3.DowngradeValidate: errors.New("cluster unhealthy"),
				clientv3.DowngradeEnable:   nil,
				clientv3.DowngradeCancel:   nil,
			},
		}
		if err := PrepareDowngrade(context.Background(), logr.Discard(), c, target, time.Minute); err == nil {
			t.Fatal("expected error")
		}
		if len(c.downgradeCalls) != 1 {
			t.Fatalf("enable must not be called after failed validate, got %v", c.downgradeCalls)
		}
	})

	t.Run("waits for storage migration", func(t *testing.T) {
		c := &fakeClient{storageVersions: []string{"3.7.0", "3.7.0", "3.7.0", "3.6.0"}}
		if err := PrepareDowngrade(context.Background(), logr.Discard(), c, target, time.Minute); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if c.statusCalls < 4 {
			t.Fatalf("expected polling until migration, got %d status calls", c.statusCalls)
		}
	})

	t.Run("times out if storage never migrates", func(t *testing.T) {
		c := &fakeClient{storageVersions: []string{"3.7.0"}}
		if err := PrepareDowngrade(context.Background(), logr.Discard(), c, target, 10*time.Second); err == nil {
			t.Fatal("expected timeout error")
		}
	})
}

func TestCancelDowngrade(t *testing.T) {
	t.Run("no inflight downgrade is a no-op", func(t *testing.T) {
		c := &fakeClient{downgradeErrs: map[clientv3.DowngradeAction]error{
			clientv3.DowngradeValidate: nil,
			clientv3.DowngradeEnable:   nil,
			clientv3.DowngradeCancel:   rpctypes.ErrNoInflightDowngrade,
		}}
		if err := CancelDowngrade(context.Background(), logr.Discard(), c); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("cancel failure surfaces", func(t *testing.T) {
		c := &fakeClient{downgradeErrs: map[clientv3.DowngradeAction]error{
			clientv3.DowngradeValidate: nil,
			clientv3.DowngradeEnable:   nil,
			clientv3.DowngradeCancel:   errors.New("boom"),
		}}
		if err := CancelDowngrade(context.Background(), logr.Discard(), c); err == nil {
			t.Fatal("expected error")
		}
	})
}
