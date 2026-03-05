package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	apiv2 "github.com/canonical/k8s-snap-api/v2/api"
	"github.com/canonical/k8sd/pkg/k8sd/api/impl"
	"github.com/canonical/k8sd/pkg/snap"
	snaputil "github.com/canonical/k8sd/pkg/snap/util"
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

func (e *Endpoints) getNodeStatus(s mctypes.State, r *http.Request) mctypes.Response {
	snap := e.provider.Snap()

	status, err := impl.GetLocalNodeStatus(r.Context(), s, snap)
	if err != nil {
		return mctypes.InternalError(err)
	}

	taints, err := getNodeTaints(snap)
	if err != nil {
		return mctypes.InternalError(fmt.Errorf("failed to get node taints: %w", err))
	}

	return mctypes.SyncResponse(true, &apiv2.NodeStatusResponse{
		NodeStatus: status,
		Taints:     taints,
	})
}

// getNodeTaints retrieves the taints of the local node.
func getNodeTaints(snap snap.Snap) ([]string, error) {
	taintsStr, err := snaputil.GetServiceArgument(snap, "kubelet", "--register-with-taints")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get kubelet taints: %w", err)
	}

	return strings.Split(taintsStr, ","), nil
}
