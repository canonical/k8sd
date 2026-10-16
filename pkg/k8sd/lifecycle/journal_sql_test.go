package lifecycle_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/canonical/k8sd/pkg/k8sd/lifecycle"
	_ "github.com/mattn/go-sqlite3"
	. "github.com/onsi/gomega"
)

// dqlite is SQLite underneath; these tests pin the SQL semantics (CHECK,
// partial unique index, ON CONFLICT) that the journal relies on.
func newSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_busy_timeout=10000", filepath.Join(t.TempDir(), "journal.db"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(lifecycle.Schema); err != nil {
		t.Fatalf("failed to apply schema: %v", err)
	}
	return db
}

func sqlTx(db *sql.DB) lifecycle.TxFunc {
	return func(ctx context.Context, f func(context.Context, *sql.Tx) error) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := f(ctx, tx); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}
}

// TestJournalContract runs the same CAS vectors against the simulator journal
// and the SQL journal so the simulator cannot drift from production semantics.
func TestJournalContract(t *testing.T) {
	for name, newJournal := range map[string]func(t *testing.T) lifecycle.Journal{
		"sim": func(*testing.T) lifecycle.Journal { return simJournal{newSim("n1")} },
		"sql": func(t *testing.T) lifecycle.Journal { return lifecycle.NewSQLJournal(sqlTx(newSQLiteDB(t))) },
	} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			ctx := testCtx(t)
			j := newJournal(t)
			cas := func(node string, from, to lifecycle.Phase, fromLease, toLease string) error {
				return j.CompareAndSwap(ctx, lifecycle.Transition{Node: node, PeerURL: peerURL(node), FromPhase: from, ToPhase: to, FromLease: fromLease, ToLease: toLease})
			}

			js, err := j.Load(ctx)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(js.Lease).To(BeEmpty())
			g.Expect(js.Records).NotTo(HaveKey("a"))

			g.Expect(cas("a", absent, joining, "", "a")).To(Succeed())
			js, err = j.Load(ctx)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(js.Lease).To(Equal("a"))
			g.Expect(js.Records).To(HaveKeyWithValue("a", lifecycle.Record{Name: "a", PeerURL: peerURL("a"), Phase: joining}))

			g.Expect(cas("b", absent, joining, "", "b")).To(MatchError(lifecycle.ErrConflict), "lease is held")
			g.Expect(cas("a", joining, lifecycle.PhaseActive, "", "")).To(MatchError(lifecycle.ErrConflict), "stale lease")
			g.Expect(cas("a", rollingBack, absent, "a", "")).To(MatchError(lifecycle.ErrConflict), "stale phase")

			g.Expect(cas("a", joining, lifecycle.PhaseActive, "a", "")).To(Succeed())
			g.Expect(cas("a", lifecycle.PhaseActive, removing, "", "a")).To(Succeed())
			g.Expect(cas("a", removing, absent, "a", "")).To(Succeed())

			js, err = j.Load(ctx)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(js.Lease).To(BeEmpty())
			g.Expect(js.Records).NotTo(HaveKey("a"))
		})
	}
}

func TestSQLJournalSchemaEnforcesInvariants(t *testing.T) {
	g := NewWithT(t)
	db := newSQLiteDB(t)
	insert := func(name, phase string) error {
		_, err := db.Exec("INSERT INTO node_lifecycle (name, peer_url, phase) VALUES (?, ?, ?)", name, peerURL(name), phase)
		return err
	}

	g.Expect(insert("a", "Active")).To(Succeed())
	g.Expect(insert("b", "Active")).To(Succeed())
	g.Expect(insert("c", "Joining")).To(Succeed())
	g.Expect(insert("d", "Removing")).To(MatchError(ContainSubstring("UNIQUE")), "SingleTransition")
	g.Expect(insert("e", "Bogus")).To(MatchError(ContainSubstring("CHECK")))
	_, err := db.Exec("INSERT INTO node_lifecycle_lease (id, holder) VALUES (2, 'a')")
	g.Expect(err).To(MatchError(ContainSubstring("CHECK")), "single lease row")
}

func TestSQLJournalConcurrentBeginIsExclusive(t *testing.T) {
	g := NewWithT(t)
	ctx := testCtx(t)
	j := lifecycle.NewSQLJournal(sqlTx(newSQLiteDB(t)))

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node := fmt.Sprintf("n%d", i)
			errs[i] = j.CompareAndSwap(ctx, lifecycle.Transition{Node: node, PeerURL: peerURL(node), FromPhase: absent, ToPhase: joining, ToLease: node})
		}()
	}
	wg.Wait()

	won := 0
	for _, err := range errs {
		if err == nil {
			won++
			continue
		}
		g.Expect(err).To(MatchError(lifecycle.ErrConflict))
	}
	g.Expect(won).To(Equal(1))
}
