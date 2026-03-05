package api

import (
	mctypes "github.com/canonical/microcluster/v3/microcluster/types"
)

const (
	// StatusNodeUnavailable is the Http status code that the API returns if the node isn't in the cluster.
	StatusNodeUnavailable = 520
	// StatusNodeInUse is the Http status code that the API returns if the node is already in the cluster.
	StatusNodeInUse = 521
)

func NodeUnavailable(err error) mctypes.Response {
	return mctypes.ErrorResponse(StatusNodeUnavailable, err.Error())
}

func NodeInUse(err error) mctypes.Response {
	return mctypes.ErrorResponse(StatusNodeInUse, err.Error())
}
