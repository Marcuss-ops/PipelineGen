package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

type youTubeEnqueueCall struct {
	assetID string
	hash    string
}

type youTubeDiscoverCall struct {
	assetID   string
	lifecycle asset.LifecycleState
	index     asset.IndexState
}

// fakeYouTubeDispatcher records which canonical entry point the adapter chose.
// The adapter must never persist media itself, so this double is the only
// observation channel.
type fakeYouTubeDispatcher struct {
	enqueued   []youTubeEnqueueCall
	discovered []youTubeDiscoverCall
	enqueueErr error
}

func newFakeYouTubeDispatcher() *fakeYouTubeDispatcher {
	return &fakeYouTubeDispatcher{}
}

func (d *fakeYouTubeDispatcher) EnqueueAndIndex(_ context.Context, clip *asset.Asset, contentHash string) error {
	if d == nil {
		return errors.New("nil dispatcher")
	}
	d.enqueued = append(d.enqueued, youTubeEnqueueCall{assetID: clip.ID, hash: contentHash})
	return d.enqueueErr
}

func (d *fakeYouTubeDispatcher) SaveDiscoveredAsset(_ context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	if d == nil {
		return errors.New("nil dispatcher")
	}
	d.discovered = append(d.discovered, youTubeDiscoverCall{assetID: clip.ID, lifecycle: lifecycle, index: idx})
	return d.enqueueErr
}

// TestYouTubeAssetWriter_Upsert_WithHashUsesCommitAndIndex pins the routing
// rule for the ordinary enrichment case: a clip that has a content fingerprint
// MUST go through the canonical commit-and-index path so media_assets and the
// media index request commit together on the media SSOT.
func TestYouTubeAssetWriter_Upsert_WithHashUsesCommitAndIndex(t *testing.T) {
	dispatcher := newFakeYouTubeDispatcher()
	writer := &youTubeAssetWriter{dispatcher: dispatcher}

	a := &asset.Asset{ID: "clip-1"}
	a.SetContentHash("sha256-abc")

	if err := writer.Upsert(context.Background(), a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(dispatcher.enqueued) != 1 {
		t.Fatalf("expected exactly one EnqueueAndIndex call, got %d", len(dispatcher.enqueued))
	}
	if got := dispatcher.enqueued[0]; got.assetID != "clip-1" || got.hash != "sha256-abc" {
		t.Fatalf("unexpected enqueue call: %+v", got)
	}
	if len(dispatcher.discovered) != 0 {
		t.Fatalf("the discovery branch must not run when a hash exists: %+v", dispatcher.discovered)
	}
}

// TestYouTubeAssetWriter_Upsert_LegacyHashFallback pins that a clip carrying
// only the compatibility hash still takes the canonical commit-and-index path
// (the commit request field is named contentHash but a legacy fingerprint is a
// valid supersede key).
func TestYouTubeAssetWriter_Upsert_LegacyHashFallback(t *testing.T) {
	dispatcher := newFakeYouTubeDispatcher()
	writer := &youTubeAssetWriter{dispatcher: dispatcher}

	a := &asset.Asset{ID: "clip-2"}
	a.SetLegacyFileMD5("legacy-md5")

	if err := writer.Upsert(context.Background(), a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(dispatcher.enqueued) != 1 || dispatcher.enqueued[0].hash != "legacy-md5" {
		t.Fatalf("expected the legacy hash to be used, got %+v", dispatcher.enqueued)
	}
}

// TestYouTubeAssetWriter_Upsert_WithoutHashUsesDiscoveryPath pins the
// hash-less branch: the asset is not indexable yet, so it is persisted through
// the SAME canonical writer with its current lifecycle/index state and no index
// request — never dropped, and never written on a different engine.
func TestYouTubeAssetWriter_Upsert_WithoutHashUsesDiscoveryPath(t *testing.T) {
	dispatcher := newFakeYouTubeDispatcher()
	writer := &youTubeAssetWriter{dispatcher: dispatcher}

	a := &asset.Asset{ID: "clip-3", LifecycleState: asset.StateActive}
	a.SetMetadataString("index_state", string(asset.StateDiscovered))

	if err := writer.Upsert(context.Background(), a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(dispatcher.discovered) != 1 {
		t.Fatalf("expected exactly one SaveDiscoveredAsset call, got %d", len(dispatcher.discovered))
	}
	got := dispatcher.discovered[0]
	if got.assetID != "clip-3" || got.lifecycle != asset.StateActive || got.index != asset.StateDiscovered {
		t.Fatalf("unexpected discovery call: %+v", got)
	}
	if len(dispatcher.enqueued) != 0 {
		t.Fatalf("the index branch must not run without a hash: %+v", dispatcher.enqueued)
	}
}

// TestYouTubeAssetWriter_Upsert_NormalizesMissingStates pins that invalid or
// missing lifecycle/index state degrades to the canonical defaults instead of
// reaching the writer with an invalid enum (SaveDiscoveredAsset rejects those).
func TestYouTubeAssetWriter_Upsert_NormalizesMissingStates(t *testing.T) {
	dispatcher := newFakeYouTubeDispatcher()
	writer := &youTubeAssetWriter{dispatcher: dispatcher}

	a := &asset.Asset{ID: "clip-4", LifecycleState: "not-a-state"}
	a.SetMetadataString("index_state", "also-not-a-state")

	if err := writer.Upsert(context.Background(), a); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(dispatcher.discovered) != 1 {
		t.Fatalf("expected one discovery call, got %d", len(dispatcher.discovered))
	}
	got := dispatcher.discovered[0]
	if got.lifecycle != asset.StateActive || got.index != asset.StateDiscovered {
		t.Fatalf("states must normalize to the canonical defaults, got %+v", got)
	}
}

// TestYouTubeAssetWriter_Upsert_FailsClosed pins the validation boundary so a
// malformed asset or an unwired writer never reaches the media writer.
func TestYouTubeAssetWriter_Upsert_FailsClosed(t *testing.T) {
	if err := (&youTubeAssetWriter{}).Upsert(context.Background(), &asset.Asset{ID: "x"}); err == nil {
		t.Error("expected an error when the canonical dispatcher is unavailable")
	}

	dispatcher := newFakeYouTubeDispatcher()
	writer := &youTubeAssetWriter{dispatcher: dispatcher}
	for name, a := range map[string]*asset.Asset{"nil": nil, "empty id": {}} {
		if err := writer.Upsert(context.Background(), a); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if len(dispatcher.discovered) != 0 || len(dispatcher.enqueued) != 0 {
		t.Fatalf("no call may reach the dispatcher, got %d/%d", len(dispatcher.enqueued), len(dispatcher.discovered))
	}
}

// TestYouTubeAssetWriter_Upsert_PropagatesWriterErrors pins that a canonical
// writer failure is surfaced (the callers log a warning) rather than swallowed,
// so the enrichment can never look successful when the media row was not
// committed.
func TestYouTubeAssetWriter_Upsert_PropagatesWriterErrors(t *testing.T) {
	dispatcher := newFakeYouTubeDispatcher()
	dispatcher.enqueueErr = errors.New("canonical commit failed")
	writer := &youTubeAssetWriter{dispatcher: dispatcher}

	a := &asset.Asset{ID: "clip-5"}
	a.SetContentHash("sha256-xyz")

	if err := writer.Upsert(context.Background(), a); err == nil {
		t.Fatal("expected the canonical writer error to propagate")
	}
}

// TestNewYouTubeAssetWriter_Selector pins the composition selector: the
// canonical path requires BOTH the canonical dispatcher and the canonical
// committer (they exist exactly when the media PostgreSQL SSOT is open); with
// no media read/write store at all the selector returns nil so the YouTube
// validator fails closed instead of wiring a half-broken pipeline.
func TestNewYouTubeAssetWriter_Selector(t *testing.T) {
	if got := newYouTubeAssetWriter(nil, nil, nil); got != nil {
		t.Fatalf("no bundles => nil, got %T", got)
	}
	if got := newYouTubeAssetWriter(&OutboxBundle{}, nil, nil); got != nil {
		t.Fatalf("no dispatcher/committer/repos => nil, got %T", got)
	}
	if got := newYouTubeAssetWriter(&OutboxBundle{Dispatcher: &outbox.Dispatcher{}}, nil, nil); got != nil {
		t.Fatalf("dispatcher without a committer must not select the canonical path, got %T", got)
	}

	canonical := newYouTubeAssetWriter(&OutboxBundle{Dispatcher: &outbox.Dispatcher{}}, nil, stubAssetCommitter{})
	if _, ok := canonical.(*youTubeAssetWriter); !ok {
		t.Fatalf("dispatcher + committer must select the canonical writer, got %T", canonical)
	}
}

// stubAssetCommitter satisfies persistence.AssetCommitter for the selector test.
type stubAssetCommitter struct{}

func (stubAssetCommitter) CommitTx(context.Context, persistence.Transaction, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (stubAssetCommitter) CommitAndIndex(context.Context, persistence.CommitRequest) (persistence.CommitResult, error) {
	return persistence.CommitResult{}, nil
}

func (stubAssetCommitter) CommitAsset(context.Context, persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return persistence.CommittedAsset{}, nil
}
