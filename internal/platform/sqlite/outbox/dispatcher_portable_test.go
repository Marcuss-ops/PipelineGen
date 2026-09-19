package outbox

import (
	"context"
	"database/sql"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// portableRecordingCommitter records every self-owned commit call and counts
// the tx-bound entry points so the tests can prove the dispatcher never takes
// a cross-engine transaction path. It implements exactly the interfaces the
// PostgreSQL media committer implements.
type portableRecordingCommitter struct {
	commits     []persistence.CommitRequest
	discoveries []portableDiscoveryCall

	txBoundEnqueue   int
	txBoundDiscovery int
}

type portableDiscoveryCall struct {
	Clip      *asset.Asset
	Lifecycle asset.LifecycleState
	Index     asset.IndexState
}

var (
	_ persistence.AssetCommitter       = (*portableRecordingCommitter)(nil)
	_ DiscoveryCommitAndIndexCommitter = (*portableRecordingCommitter)(nil)
)

func (c *portableRecordingCommitter) CommitAndIndex(_ context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	c.commits = append(c.commits, req)
	return persistence.CommitResult{OutboxEventKey: "portable-key"}, nil
}

func (c *portableRecordingCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return c.CommitAndIndex(ctx, persistence.CommitRequest(req))
}

func (c *portableRecordingCommitter) CommitTx(_ context.Context, _ persistence.Transaction, _ persistence.CommitRequest) (persistence.CommitResult, error) {
	c.txBoundEnqueue++
	return persistence.CommitResult{}, nil
}

// CommitDiscoveredAsset is the DEMOLISHED tx-bound discovery entry point. It
// survives only on the fake to prove the dispatcher never calls it.
func (c *portableRecordingCommitter) CommitDiscoveredAsset(_ context.Context, _ *sql.Tx, _ *asset.Asset, _ asset.LifecycleState, _ asset.IndexState) error {
	c.txBoundDiscovery++
	return nil
}

func (c *portableRecordingCommitter) CommitDiscoveredAssetAndIndex(_ context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	c.discoveries = append(c.discoveries, portableDiscoveryCall{Clip: clip, Lifecycle: lifecycle, Index: idx})
	return nil
}

// newSelfOwnedDispatcher wires a dispatcher exactly like the production
// composition root does for the PostgreSQL media SSOT: the canonical committer
// plus the SQLite operational TxManager. The dispatcher holds no tx-bound
// media writer — that construct no longer exists.
func newSelfOwnedDispatcher(t *testing.T, committer *portableRecordingCommitter) *Dispatcher {
	t.Helper()
	d := NewDispatcher(nil, nil, zap.NewNop(), committer)
	if d.committer == nil {
		t.Fatal("NewDispatcher must keep the canonical committer")
	}
	return d
}

// TestDispatcher_EnqueueAndIndexUsesCommitterOwnTx pins that EnqueueAndIndex
// never opens a dispatcher transaction and instead delegates to the
// committer's CommitAndIndex with the index event requested.
func TestDispatcher_EnqueueAndIndexUsesCommitterOwnTx(t *testing.T) {
	committer := &portableRecordingCommitter{}
	d := newSelfOwnedDispatcher(t, committer)

	clip := &asset.Asset{
		ID:        "planner:stock-portable:0",
		Source:    asset.Source("stock"),
		Name:      "portable clip",
		MediaType: asset.MediaType("video"),
	}
	const contentHash = "sha256:portable"

	if err := d.EnqueueAndIndex(context.Background(), clip, contentHash); err != nil {
		t.Fatalf("EnqueueAndIndex: %v", err)
	}
	if len(committer.commits) != 1 {
		t.Fatalf("expected 1 CommitAndIndex call, got %d", len(committer.commits))
	}
	if committer.txBoundEnqueue != 0 {
		t.Errorf("the dispatcher must not use the tx-bound CommitTx path; got %d calls", committer.txBoundEnqueue)
	}
	req := committer.commits[0]
	if req.AssetID != clip.ID {
		t.Errorf("commit AssetID = %q, want %q", req.AssetID, clip.ID)
	}
	if req.ContentHash != contentHash {
		t.Errorf("commit ContentHash = %q, want %q", req.ContentHash, contentHash)
	}
	if !req.EmitIndexEvent {
		t.Error("non-folder commit must request the index event")
	}
}

// TestDispatcher_SaveDiscoveredAssetUsesSelfOwnedTx pins that
// SaveDiscoveredAsset delegates to the committer's self-owned transaction
// (CommitDiscoveredAssetAndIndex) and never hands a SQLite *sql.Tx to the
// media committer.
func TestDispatcher_SaveDiscoveredAssetUsesSelfOwnedTx(t *testing.T) {
	committer := &portableRecordingCommitter{}
	d := newSelfOwnedDispatcher(t, committer)

	clip := &asset.Asset{
		ID:     "artlist_portable",
		Source: asset.Source("artlist"),
		Name:   "discovered clip",
	}

	if err := d.SaveDiscoveredAsset(context.Background(), clip, asset.StateStaging, asset.StateDiscovered); err != nil {
		t.Fatalf("SaveDiscoveredAsset: %v", err)
	}
	if len(committer.discoveries) != 1 {
		t.Fatalf("expected 1 CommitDiscoveredAssetAndIndex call, got %d", len(committer.discoveries))
	}
	if committer.txBoundDiscovery != 0 {
		t.Errorf("the dispatcher must not use the tx-bound CommitDiscoveredAsset path; got %d calls", committer.txBoundDiscovery)
	}
	if committer.txBoundEnqueue != 0 {
		t.Errorf("discovery must not emit an index commit; got %d CommitTx calls", committer.txBoundEnqueue)
	}
	call := committer.discoveries[0]
	if call.Lifecycle != asset.StateStaging {
		t.Errorf("discovery lifecycle = %q, want %q", call.Lifecycle, asset.StateStaging)
	}
	if call.Index != asset.StateDiscovered {
		t.Errorf("discovery index state = %q, want %q", call.Index, asset.StateDiscovered)
	}
	if got := call.Clip.Metadata["job_key"]; got == "" {
		t.Error("SaveDiscoveredAsset must stamp metadata_json.job_key before the commit")
	}
}

// TestDispatcher_DiscoveryRequiresSelfOwnedCommitter pins the fail-closed
// behavior: a committer that does NOT implement the self-owned discovery port
// must produce an error rather than silently falling back to a cross-engine
// transaction.
func TestDispatcher_DiscoveryRequiresSelfOwnedCommitter(t *testing.T) {
	d := NewDispatcher(nil, nil, zap.NewNop(), txBoundOnlyDiscoveryCommitter{})
	err := d.SaveDiscoveredAsset(context.Background(), &asset.Asset{ID: "x", Source: asset.Source("artlist")}, asset.StateStaging, asset.StateDiscovered)
	if err == nil {
		t.Fatal("discovery without a self-owned-tx committer must fail closed")
	}
}

// txBoundOnlyDiscoveryCommitter implements persistence.AssetCommitter and the
// DEMOLISHED tx-bound discovery method, but deliberately NOT
// DiscoveryCommitAndIndexCommitter.
type txBoundOnlyDiscoveryCommitter struct{}

func (txBoundOnlyDiscoveryCommitter) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (txBoundOnlyDiscoveryCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return txBoundOnlyDiscoveryCommitter{}.CommitAndIndex(ctx, persistence.CommitRequest(req))
}

func (txBoundOnlyDiscoveryCommitter) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (txBoundOnlyDiscoveryCommitter) CommitDiscoveredAsset(context.Context, *sql.Tx, *asset.Asset, asset.LifecycleState, asset.IndexState) error {
	return nil
}

// ── P0 stock-acquisition certification (September 2026) ──────────────────

// TestBuildPortableCommitRequest_HonoursDeclaredTaxonomy pins the
// post-publication half of the stock-clip convergence contract.
//
// A stock clip is committed twice — once here through EnqueueAndIndex (which
// carries the Drive identity) and once by the stock job finalizer's single-TX
// spine write. When this side declared no taxonomy, the media upsert's
// insert-wins taxonomy semantics let the spine write restamp semantic_role
// from the provider default, and the producer-declared stock family was lost.
// A producer that declares asset_kind / semantic_role must have them resolved
// into the commit request; a producer that declares nothing must be unchanged.
func TestBuildPortableCommitRequest_HonoursDeclaredTaxonomy(t *testing.T) {
	declaring := &asset.Asset{
		ID:        "planner:6638386361363531:0",
		Source:    asset.Source("youtube"),
		Name:      "clip_001.mp4",
		MediaType: asset.MediaType("video"),
		Metadata: map[string]any{
			"asset_kind":    "stock_video",
			"semantic_role": "stock",
		},
	}

	req := buildPortableCommitRequest(declaring, "sha256-abc", true)

	if req.Source != "youtube" {
		t.Errorf("Source = %q, want the acquisition provider %q", req.Source, "youtube")
	}
	if req.Taxonomy.IsZero() {
		t.Fatal("declared taxonomy was discarded")
	}
	if req.Taxonomy.AssetKind != capregistry.AssetStockVideo {
		t.Errorf("Taxonomy.AssetKind = %q, want %q", req.Taxonomy.AssetKind, capregistry.AssetStockVideo)
	}
	if req.Taxonomy.SemanticRole != "stock" {
		t.Errorf("Taxonomy.SemanticRole = %q, want %q (not the provider default)", req.Taxonomy.SemanticRole, "stock")
	}
	if req.Taxonomy.SourceType != "youtube" {
		t.Errorf("Taxonomy.SourceType = %q, want %q", req.Taxonomy.SourceType, "youtube")
	}
	if req.IndexPriority != persistence.IndexPriorityHigh {
		t.Errorf("stock IndexPriority = %d, want %d", req.IndexPriority, persistence.IndexPriorityHigh)
	}

	silent := &asset.Asset{
		ID:        "artlist_abc",
		Source:    asset.Source("artlist"),
		Name:      "discovered.mp4",
		MediaType: asset.MediaType("video"),
	}
	if silentReq := buildPortableCommitRequest(silent, "sha256-def", true); !silentReq.Taxonomy.IsZero() {
		t.Errorf("an undeclared taxonomy must stay zero (COALESCE-keep preserves the stored dimensions); got %+v", silentReq.Taxonomy)
	}
	if silentReq := buildPortableCommitRequest(silent, "sha256-def", true); silentReq.IndexPriority != 0 {
		t.Errorf("an undeclared taxonomy must keep normal index priority; got %d", silentReq.IndexPriority)
	}
}
