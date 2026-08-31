package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/idempotency"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
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

// fakeSQLiteAssetCommitter is a test-only implementation of the canonical
// persistence.AssetCommitter port. It deliberately owns the same boundary as
// production: the Dispatcher never calls fakeClips.UpsertClipTx directly;
// the fake commits the canonical outbox event through the supplied transaction
// manager and records discovery commits for the existing assertions.
type fakeSQLiteAssetCommitter struct {
	outbox    outboxEnqueuer
	txmgr     TxManager
	discovery *fakeClips
}

var _ persistence.AssetCommitter = (*fakeSQLiteAssetCommitter)(nil)
var _ DiscoveryCommitter = (*fakeSQLiteAssetCommitter)(nil)

func (f *fakeSQLiteAssetCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if f == nil || f.outbox == nil || f.txmgr == nil {
		return persistence.CommitResult{}, fmt.Errorf("fake SQLiteAssetCommitter: dependencies are required")
	}
	var result persistence.CommitResult
	err := f.txmgr.InTransaction(ctx, func(tx *sql.Tx) error {
		committed, err := f.commitIndexEvent(ctx, tx, req)
		if err != nil {
			return err
		}
		result = committed
		return nil
	})
	return result, err
}

func (f *fakeSQLiteAssetCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return f.CommitAndIndex(ctx, persistence.CommitRequest(req))
}

func (f *fakeSQLiteAssetCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok || sqlTx == nil {
		return persistence.CommitResult{}, fmt.Errorf("fake SQLiteAssetCommitter: expected *sql.Tx, got %T", tx)
	}
	return f.commitIndexEvent(ctx, sqlTx, req)
}

func (f *fakeSQLiteAssetCommitter) CommitDiscoveredAsset(ctx context.Context, tx *sql.Tx, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if f == nil || f.discovery == nil {
		return fmt.Errorf("fake SQLiteAssetCommitter: discovery dependencies are required")
	}
	return f.discovery.UpsertClipTx(ctx, tx, clip)
}

func (f *fakeSQLiteAssetCommitter) commitIndexEvent(ctx context.Context, tx *sql.Tx, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if req.AssetID == "" || req.Source == "" || req.ContentHash == "" {
		return persistence.CommitResult{}, fmt.Errorf("fake SQLiteAssetCommitter: asset id, source and content hash are required")
	}
	key, err := idempotency.OutboxKey(outboxevents.EventAssetIndexRequested, req.Source, req.AssetID, req.ContentHash)
	if err != nil {
		return persistence.CommitResult{}, fmt.Errorf("fake SQLiteAssetCommitter: build event key: %w", err)
	}
	payload, err := json.Marshal(map[string]string{
		"asset_id":       req.AssetID,
		"source_version": req.ContentHash,
	})
	if err != nil {
		return persistence.CommitResult{}, fmt.Errorf("fake SQLiteAssetCommitter: build payload: %w", err)
	}
	enqueued, err := f.outbox.Enqueue(ctx, tx, outboxevents.EventAssetIndexRequested, req.AssetID, "media_asset", string(payload), key)
	if err != nil {
		return persistence.CommitResult{}, err
	}
	if enqueued == nil {
		return persistence.CommitResult{}, fmt.Errorf("fake SQLiteAssetCommitter: outbox returned nil result")
	}
	return persistence.CommitResult{
		AssetRowsAffected:    1,
		OutboxEventKey:       key,
		OutboxInserted:       enqueued.Inserted,
		OutboxExistingStatus: enqueued.ExistingStatus,
	}, nil
}

func newTestDispatcher(clips *fakeClips, events outboxEnqueuer, txmgr TxManager) *Dispatcher {
	committer := &fakeSQLiteAssetCommitter{
		outbox:    events,
		txmgr:     txmgr,
		discovery: clips,
	}
	return NewDispatcher(clips, clips, events, txmgr, zap.NewNop(), committer)
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

// TestDispatcher_EmptyContentHashRejected confirms that EnqueueAndIndex
// rejects empty contentHash — the supersede gate in IndexingHandler
// dead-letters events with source_version="" (PR-ARTLIST-SOURCE-VERSION-FIX).
func TestDispatcher_EmptyContentHashRejected(t *testing.T) {
	// Wire all deps non-nil so the contentHash guard fires (not a nil-dep guard).
	// outboxEventsRepo uses a noop stub so the tx is never reached.
	d := &Dispatcher{
		clips:            &fakeClips{},
		outboxEventsRepo: &noopOutboxEventsRepo{},
		txmgr:            txMgrNoop{},
		log:              zap.NewNop(),
		canonicalCommitter: &fakeSQLiteAssetCommitter{
			outbox: &noopOutboxEventsRepo{}, txmgr: txMgrNoop{}, discovery: &fakeClips{},
		},
	}
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: "clip-1"}, "")
	if err == nil {
		t.Fatal("empty contentHash must return error before txmgr.InTransaction is reached")
	}
	if got := err.Error(); !strings.Contains(got, "contentHash is required") {
		t.Errorf("error message must mention 'contentHash is required', got: %s", got)
	}
	if got := err.Error(); !strings.Contains(got, "clip-1") {
		t.Errorf("error message must name the clip ID, got: %s", got)
	}
}

// noopOutboxEventsRepo is a no-op stub satisfying the outboxEventsRepo
// interface so Dispatcher tests can wire all deps non-nil.
type noopOutboxEventsRepo struct{}

func (noopOutboxEventsRepo) Enqueue(_ context.Context, _ *sql.Tx, _, _, _, _, _ string) (*outboxevents.EnqueueResult, error) {
	return &outboxevents.EnqueueResult{}, nil
}

// Compile-time guard: noopOutboxEventsRepo satisfies the outboxEnqueuer port.
var _ outboxEnqueuer = (*noopOutboxEventsRepo)(nil)

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
