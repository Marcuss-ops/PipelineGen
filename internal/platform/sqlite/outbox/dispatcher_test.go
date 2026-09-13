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

// fakeDiscoveryRecorder records the self-owned discovery commits the
// dispatcher issues, so tests can assert the discovery path never opens a
// dispatcher transaction.
type fakeDiscoveryRecorder struct {
	mu        sync.Mutex
	upserts   []*asset.Asset
	orderLog  []string
	upsertErr error
}

func (f *fakeDiscoveryRecorder) CommitDiscoveredAssetAndIndex(_ context.Context, clip *asset.Asset, _ asset.LifecycleState, _ asset.IndexState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, clip)
	f.orderLog = append(f.orderLog, "discovery:"+clip.ID)
	if f.upsertErr != nil {
		return f.upsertErr
	}
	return nil
}

// fakeSQLiteAssetCommitter is a test-only implementation of the canonical
// persistence.AssetCommitter port plus the self-owned discovery port. It
// deliberately owns the same boundary as production: the Dispatcher never
// writes media itself; the fake commits the canonical outbox event through
// the supplied transaction manager (CommitAndIndex) and records discovery
// commits for the existing assertions.
//
// MEDIA-SSOT (September 2026): the fake no longer implements a tx-bound
// discovery method — that entry point was removed from the contract.
type fakeSQLiteAssetCommitter struct {
	outbox    outboxEnqueuer
	txmgr     TxManager
	discovery *fakeDiscoveryRecorder
}

var (
	_ persistence.AssetCommitter       = (*fakeSQLiteAssetCommitter)(nil)
	_ DiscoveryCommitAndIndexCommitter = (*fakeSQLiteAssetCommitter)(nil)
)

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

func (f *fakeSQLiteAssetCommitter) CommitDiscoveredAssetAndIndex(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if f == nil || f.discovery == nil {
		return fmt.Errorf("fake SQLiteAssetCommitter: discovery dependencies are required")
	}
	return f.discovery.CommitDiscoveredAssetAndIndex(ctx, clip, lifecycle, idx)
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

// txMgrNoop is a TxManager that does nothing. Tests that should fail-fast
// before reaching the transaction (nil-safety, empty-clip-id) wire this in.
// DB() returns nil because Dispatcher never invokes it on the hot path.
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

// TestDispatcher_MissingCommitterRejected confirms the canonical-committer
// guard runs before any other work.
func TestDispatcher_MissingCommitterRejected(t *testing.T) {
	d := NewDispatcher(&noopOutboxEventsRepo{}, txMgrNoop{}, zap.NewNop(), nil)
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: "x"}, "hash")
	if err == nil {
		t.Fatal("nil canonical AssetCommitter must return error before any field access")
	}
	if !strings.Contains(err.Error(), "canonical AssetCommitter is required") {
		t.Errorf("error must name the missing canonical AssetCommitter, got: %s", err.Error())
	}
}

// TestDispatcher_MissingClipIDRejected confirms the empty-ID guard runs
// before any commit is attempted.
func TestDispatcher_MissingClipIDRejected(t *testing.T) {
	d := NewDispatcher(&noopOutboxEventsRepo{}, txMgrNoop{}, zap.NewNop(), &fakeSQLiteAssetCommitter{outbox: &noopOutboxEventsRepo{}, txmgr: txMgrNoop{}, discovery: &fakeDiscoveryRecorder{}})
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: ""}, "hash")
	if err == nil {
		t.Fatal("empty clip ID must return error before any commit is reached")
	}
}

// TestDispatcher_EmptyContentHashRejected confirms that EnqueueAndIndex
// rejects empty contentHash — the supersede gate dead-letters events with
// source_version="" (PR-ARTLIST-SOURCE-VERSION-FIX).
func TestDispatcher_EmptyContentHashRejected(t *testing.T) {
	// Wire all deps non-nil so the contentHash guard fires (not a nil-dep guard).
	d := NewDispatcher(
		&noopOutboxEventsRepo{},
		txMgrNoop{},
		zap.NewNop(),
		&fakeSQLiteAssetCommitter{outbox: &noopOutboxEventsRepo{}, txmgr: txMgrNoop{}, discovery: &fakeDiscoveryRecorder{}},
	)
	err := d.EnqueueAndIndex(context.Background(), &asset.Asset{ID: "clip-1"}, "")
	if err == nil {
		t.Fatal("empty contentHash must return error before any commit is reached")
	}
	if got := err.Error(); !strings.Contains(got, "contentHash is required") {
		t.Errorf("error message must mention 'contentHash is required', got: %s", got)
	}
	if got := err.Error(); !strings.Contains(got, "clip-1") {
		t.Errorf("error message must name the clip ID, got: %s", got)
	}
}

// noopOutboxEventsRepo is a no-op stub satisfying the outboxEnqueuer
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

// Compile-time guard: txMgrNoop must satisfy the TxManager interface used
// by Dispatcher and the outbox worker.
var _ TxManager = txMgrNoop{}
