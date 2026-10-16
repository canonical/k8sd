package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Schema creates the journal tables. Register it as a microcluster schema
// extension to back the orchestrator with dqlite.
//
// The constraints enforce spec invariants independently of the Go code:
//   - node_lifecycle_lease has at most one row (one lease holder);
//   - the partial unique index admits at most one transitional row
//     (SingleTransition).
const Schema = `
CREATE TABLE IF NOT EXISTS node_lifecycle (
  name     TEXT PRIMARY KEY NOT NULL,
  peer_url TEXT NOT NULL,
  phase    TEXT NOT NULL CHECK (phase IN ('Joining', 'Active', 'RollingBack', 'Removing'))
);
CREATE UNIQUE INDEX IF NOT EXISTS node_lifecycle_single_transition
  ON node_lifecycle (phase IN ('Joining', 'RollingBack', 'Removing'))
  WHERE phase IN ('Joining', 'RollingBack', 'Removing');
CREATE TABLE IF NOT EXISTS node_lifecycle_lease (
  id     INTEGER PRIMARY KEY NOT NULL CHECK (id = 1),
  holder TEXT NOT NULL
);
`

// TxFunc runs f in a single serialisable transaction, e.g.
// mctypes.State.Database().Transaction.
type TxFunc func(ctx context.Context, f func(context.Context, *sql.Tx) error) error

// SQLJournal implements Journal on dqlite/SQLite.
type SQLJournal struct {
	tx TxFunc
}

// NewSQLJournal returns a Journal using tx. Schema must already be applied.
func NewSQLJournal(tx TxFunc) *SQLJournal { return &SQLJournal{tx: tx} }

// Load implements Journal.
func (j *SQLJournal) Load(ctx context.Context) (JournalState, error) {
	js := JournalState{Records: map[string]Record{}}
	err := j.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		lease, err := selectLease(ctx, tx)
		if err != nil {
			return err
		}
		js.Lease = lease

		rows, err := tx.QueryContext(ctx, "SELECT name, peer_url, phase FROM node_lifecycle")
		if err != nil {
			return fmt.Errorf("failed to query journal: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var r Record
			if err := rows.Scan(&r.Name, &r.PeerURL, &r.Phase); err != nil {
				return fmt.Errorf("failed to scan journal row: %w", err)
			}
			if !r.Phase.Valid() {
				return fmt.Errorf("journal row %s has invalid phase %q", r.Name, r.Phase)
			}
			js.Records[r.Name] = r
		}
		return rows.Err()
	})
	return js, err
}

// CompareAndSwap implements Journal.
func (j *SQLJournal) CompareAndSwap(ctx context.Context, t Transition) error {
	return j.tx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		lease, err := selectLease(ctx, tx)
		if err != nil {
			return err
		}
		if lease != t.FromLease {
			return fmt.Errorf("%w: lease is %q, expected %q", ErrConflict, lease, t.FromLease)
		}

		phase := PhaseAbsent
		peerURL := t.PeerURL
		var storedPeerURL string
		switch err := tx.QueryRowContext(ctx, "SELECT phase, peer_url FROM node_lifecycle WHERE name = ?", t.Node).Scan(&phase, &storedPeerURL); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("failed to query journal row %s: %w", t.Node, err)
		default:
			peerURL = storedPeerURL
		}
		if phase != t.FromPhase {
			return fmt.Errorf("%w: node %s is %s, expected %s", ErrConflict, t.Node, phase, t.FromPhase)
		}

		if t.ToPhase == PhaseAbsent {
			_, err = tx.ExecContext(ctx, "DELETE FROM node_lifecycle WHERE name = ?", t.Node)
		} else {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO node_lifecycle (name, peer_url, phase) VALUES (?, ?, ?)
				 ON CONFLICT (name) DO UPDATE SET phase = excluded.phase`,
				t.Node, peerURL, string(t.ToPhase))
		}
		if err != nil {
			return fmt.Errorf("failed to write journal row %s: %w", t.Node, err)
		}

		if t.ToLease == "" {
			_, err = tx.ExecContext(ctx, "DELETE FROM node_lifecycle_lease WHERE id = 1")
		} else {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO node_lifecycle_lease (id, holder) VALUES (1, ?)
				 ON CONFLICT (id) DO UPDATE SET holder = excluded.holder`, t.ToLease)
		}
		if err != nil {
			return fmt.Errorf("failed to write journal lease: %w", err)
		}
		return nil
	})
}

func selectLease(ctx context.Context, tx *sql.Tx) (string, error) {
	var holder string
	switch err := tx.QueryRowContext(ctx, "SELECT holder FROM node_lifecycle_lease WHERE id = 1").Scan(&holder); {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("failed to query journal lease: %w", err)
	}
	return holder, nil
}
