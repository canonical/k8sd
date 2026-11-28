package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/canonical/k8sd/pkg/k8sd/types"
	"github.com/canonical/microcluster/v3/microcluster/db"
)

var clusterConfigsStmts = map[string]int{
	"insert-v1alpha2": MustPrepareStatement("cluster-configs", "insert-v1alpha2.sql"),
	"select-v1alpha2": MustPrepareStatement("cluster-configs", "select-v1alpha2.sql"),
}

// SetClusterConfig updates the cluster configuration with any non-empty values that are set.
// SetClusterConfig will attempt to merge the existing and new configs, and return an error if any protected fields have changed.
// SetClusterConfig will return the merged cluster configuration on success.
func SetClusterConfig(ctx context.Context, tx *sql.Tx, new types.ClusterConfig) (types.ClusterConfig, error) {
	old, err := GetClusterConfig(ctx, tx)
	if err != nil {
		return types.ClusterConfig{}, fmt.Errorf("failed to fetch existing cluster config: %w", err)
	}
	config, err := types.MergeClusterConfig(old, new)
	if err != nil {
		return types.ClusterConfig{}, fmt.Errorf("failed to merge new cluster configuration options: %w", err)
	}

	if err := insertClusterConfig(ctx, tx, config); err != nil {
		return types.ClusterConfig{}, err
	}
	return config, nil
}

// SetClusterDNS records the allocated CoreDNS ClusterIP unless the stored IP changed from expected.
// It reports whether the stored IP equals ip afterwards.
func SetClusterDNS(ctx context.Context, tx *sql.Tx, expected string, ip string) (bool, error) {
	config, err := GetClusterConfig(ctx, tx)
	if err != nil {
		return false, fmt.Errorf("failed to fetch existing cluster config: %w", err)
	}
	current := config.Kubelet.GetClusterDNS()
	if current == ip {
		return true, nil
	}
	if current != expected {
		return false, nil
	}

	config.Kubelet.ClusterDNS = &ip
	if err := insertClusterConfig(ctx, tx, config); err != nil {
		return false, err
	}
	return true, nil
}

func insertClusterConfig(ctx context.Context, tx *sql.Tx, config types.ClusterConfig) error {
	b, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to encode cluster config: %w", err)
	}
	insertTxStmt, err := db.Stmt(tx, clusterConfigsStmts["insert-v1alpha2"])
	if err != nil {
		return fmt.Errorf("failed to prepare insert statement: %w", err)
	}
	if _, err := insertTxStmt.ExecContext(ctx, string(b)); err != nil {
		return fmt.Errorf("failed to insert v1alpha2 config: %w", err)
	}
	return nil
}

// GetClusterConfig retrieves the cluster configuration from the database.
func GetClusterConfig(ctx context.Context, tx *sql.Tx) (types.ClusterConfig, error) {
	txStmt, err := db.Stmt(tx, clusterConfigsStmts["select-v1alpha2"])
	if err != nil {
		return types.ClusterConfig{}, fmt.Errorf("failed to prepare statement: %w", err)
	}

	var s string
	if err := txStmt.QueryRowContext(ctx).Scan(&s); err != nil {
		if err == sql.ErrNoRows {
			return types.ClusterConfig{}, nil
		}
		return types.ClusterConfig{}, fmt.Errorf("failed to retrieve v1alpha2 config: %w", err)
	}

	var clusterConfig types.ClusterConfig
	if err := json.Unmarshal([]byte(s), &clusterConfig); err != nil {
		return types.ClusterConfig{}, fmt.Errorf("failed to parse v1alpha2 config: %w", err)
	}

	return clusterConfig, nil
}
