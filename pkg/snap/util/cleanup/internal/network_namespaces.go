package internal

import (
	"context"
	"os"
	"strings"
	"syscall"

	"github.com/canonical/k8sd/pkg/log"
	netnsutils "github.com/canonical/k8sd/pkg/utils/netns"
)

// NetworkNamespaces is a set of network namespaces, identified by their nsfs device and inode.
type NetworkNamespaces map[netnsID]struct{}

type netnsID struct{ dev, ino uint64 }

func statNetns(path string) (netnsID, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return netnsID{}, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return netnsID{}, false
	}
	return netnsID{dev: uint64(st.Dev), ino: st.Ino}, true
}

// add records the network namespace that path (e.g. /proc/<pid>/ns/net) refers to.
func (n NetworkNamespaces) add(path string) {
	if id, ok := statNetns(path); ok {
		n[id] = struct{}{}
	}
}

// contains reports whether path refers to a network namespace in the set.
func (n NetworkNamespaces) contains(path string) bool {
	id, ok := statNetns(path)
	if !ok {
		return false
	}
	_, found := n[id]
	return found
}

// RemoveNetworkNamespaces deletes the CNI network namespaces in owned. Namespaces of other
// container runtimes on the host (e.g. Podman, MicroK8s, kind) use the same "cni-" names,
// so the name alone does not prove the namespace belongs to the snap.
func RemoveNetworkNamespaces(ctx context.Context, netnsHelper netnsutils.NetworkNamespaceManager, owned NetworkNamespaces) {
	log := log.FromContext(ctx)

	err := netnsHelper.ForEachNetworkNamespace(ctx, func(ctx context.Context, namespace string) error {
		if strings.HasPrefix(namespace, "cni-") && owned.contains(netnsHelper.Path(namespace)) {
			return netnsHelper.DeleteNetworkNamespace(ctx, namespace)
		}
		return nil
	})
	if err != nil {
		log.Error(err, "failed to iterate and delete network namespaces")
	}
}
