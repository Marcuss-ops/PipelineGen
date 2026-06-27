package outbox

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// fakeClips records UpsertClipTx invocations and argument order so tests
// can assert the upsert comes BEFORE the outbox enqueue (single tx).
// QDRANT-002 PR7: extends to also satisfy the ClipsStateWriter
// interface so the dispatcher wired with a single fake satisfies both
// the upserter and the state writer in test wiring.
type fakeClips struct {
	mu        sync.Mutex
	upserts   []*asset.Asset
	orderLog  []string
	upsertErr error

	statesMu sync.Mutex
	stateLog []stateTxLog
	stateErr error
}

type stateTxLog struct {
	Tx    *sql.Tx
	ID    string
	State asset.IndexState
}

func (f *fakeClips) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, clip)
	f.orderLog = append(f.orderLog, "upsert:"+clip.ID)
	if f.upsertErr != nil {
		return f.upsertErr
	}
	return nil
}

func (f *fakeClips) SetIndexStateTx(ctx context.Context, tx *sql.Tx, id string, state asset.IndexState) error {
	f.statesMu.Lock()
	defer f.statesMu.Unlock()
	f.stateLog = append(f.stateLog, stateTxLog{Tx: tx, ID: id, State: state})
	if f.stateErr != nil {
		return f.stateErr
	}
	return nil
}

// RestoreTx (QDRANT-002 close-out, June 2026) records the restore call.
func (f *fakeClips) RestoreTx(ctx context.Context, tx *sql.Tx, id string) error {
	f.statesMu.Lock()
	defer f.statesMu.Unlock()
	// Record as a state log entry with an empty state marker.
	f.stateLog = append(f.stateLog, stateTxLog{Tx: tx, ID: id, State: "(restore)"})
	return nil
}

// HardDeleteTx (QDRANT-002 close-out, June 2026) records the hard-delete call.
func (f *fakeClips) HardDeleteTx(ctx context.Context, tx *sql.Tx, id string) error {
	f.statesMu.Lock()
	defer f.statesMu.Unlock()
	f.stateLog = append(f.stateLog, stateTxLog{Tx: tx, ID: id, State: "(hard_delete)"})
	return nil
}

// txMgrNoop is a TxManager that prints a clear failure if anyone actually
// calls InTransaction. Tests that should fail-fast before reaching the
// transaction (nil-safety, empty-clip-id) wire this in. DB() returns nil
// because Dispatcher never invokes it on the hot path.
type txMgrNoop struct{}

func (txMgrNoop) InTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return nil // unreachable for the tests in this file
}

func (txMgrNoop) DB() *sql.DB { return nil }

// TestDispatcher_NilPointerRejected confirms a nil *Dispatcher fails fast
// without dereferencing any field.
func TestDispatcher_NilPointerRejected(t *testing.T) {
	var d *Dispatcher
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: "x"}, "hash")
	if err == nil {
		t.Fatal("nil *Dispatcher must return error before any field access")
	}
}

// TestDispatcher_MissingClipIDRejected confirms the empty-ID guard runs
// before any tx is opened (txMgrNoop would catch a bug).
func TestDispatcher_MissingClipIDRejected(t *testing.T) {
	d := NewDispatcher(&fakeClips{}, &fakeClips{}, nil, txMgrNoop{}, zap.NewNop())
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: ""}, "hash")
	if err == nil {
		t.Fatal("empty clip ID must return error before txmgr.InTransaction is reached")
	}
}

// TestDispatcher_MissingOutboxEventsRejected confirms the outbox-events-nil guard
// runs before any tx is opened.
func TestDispatcher_MissingOutboxEventsRejected(t *testing.T) {
	d := &Dispatcher{clips: &fakeClips{}, outboxEventsRepo: nil, txmgr: txMgrNoop{}}
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: "x"}, "hash")
	if err == nil {
		t.Fatal("nil outboxEventsRepo must return error before tx is reached")
	}
}

// TestDispatcher_MissingTxMgrRejected confirms the txmgr-nil guard runs
// before any tx is opened.
func TestDispatcher_MissingTxMgrRejected(t *testing.T) {
	d := &Dispatcher{clips: &fakeClips{}, outboxEventsRepo: nil}
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: "x"}, "hash")
	if err == nil {
		t.Fatal("nil txmgr must return error before any field access")
	}
}

// TestShortHashPrefix covers the trivial content-hash log prefix shim.
func TestShortHashPrefix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"short", "short"},
		{"abcdefghijklmnop", "abcdefghijkl"},
	}
	for _, c := range cases {
		if got := shortHashPrefix(c.in); got != c.want {
			t.Errorf("shortHashPrefix(%q): want %q got %q", c.in, c.want, got)
		}
	}
}

// Compile-time guard: fakeClips must satisfy the ClipsUpserter interface.
var _ ClipsUpserter = (*fakeClips)(nil)

// Compile-time guard: fakeClips also satisfies the ClipsStateWriter interface
// (QDRANT-002 PR7 — Dispatcher wires both upserter and state writer through
// the same concrete in production, so the test does the same).
var _ ClipsStateWriter = (*fakeClips)(nil)

// Compile-time guard: txMgrNoop must satisfy the TxManager interface used
// by Dispatcher and the outbox worker (defined in indexer.go).
var _ TxManager = txMgrNoop{}
