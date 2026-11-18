package api

import (
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

const (
	// StatusNodeInUse is the Http status code that the API returns if the node is already in the cluster.
	StatusNodeInUse = 521
)

func NodeInUse(err error) mctypes.Response {
	return mctypes.ErrorResponse(StatusNodeInUse, err.Error())
}
