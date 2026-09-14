// Package texttracks — materializer_media_ports_test.go: regression suite
// for the post-translation index seam (POSTGRES-MEDIA-CUTOVER follow-up,
// September 2026).
//
// Pins the two facts the media-index gap turned on:
//
//  1. When the PostgreSQL IndexRequester port is wired, the materializer
//     requests its reindex through THAT port and never touches the
//     operational SQLite outbox (which owns no media handler in any mode,
//     so an event written there can only dead-letter).
//  2. The multilingual search_text rebuild runs BEFORE the reindex request,
//     because the PostgreSQL index worker embeds search_text verbatim —
//     reindexing an un-rebuilt text would leave the fresh translations
//     invisible to semantic search.
//  3. The rebuild runs even when the run created NO translation, and the
//     reindex follows only when something actually changed. Without this,
//     the operator backfill over the pre-existing catalog (where every
//     language row is already READY) could never repair the search_text
//     that the July-2026 bug left without its translations, and the
//     affected assets would stay invisible to multilingual search no
//     matter how often the backfill was re-run.
package texttracks

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type fakeIndexRequester struct {
	calls []string
	order *[]string
	err   error
}

func (f *fakeIndexRequester) RequestIndex(_ context.Context, assetID string) error {
	if f.order != nil {
		*f.order = append(*f.order, "reindex")
	}
	f.calls = append(f.calls, assetID)
	return f.err
}

type fakeSearchTextRebuilder struct {
	calls   []string
	order   *[]string
	changed bool
	err     error
}

func (f *fakeSearchTextRebuilder) Rebuild(_ context.Context, assetID string) (bool, error) {
	if f.order != nil {
		*f.order = append(*f.order, "rebuild")
	}
	f.calls = append(f.calls, assetID)
	return f.changed, f.err
}

// TestMaterialize_UsesPostgresReindexPortAndRebuildsSearchText is the
// canonical regression for the wiring bug: translations were durably written
// but the reindex went to the SQLite outbox and the search_text was never
// recomposed, so the translations never entered the E5 embedding.
func TestMaterialize_UsesPostgresReindexPortAndRebuildsSearchText(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	const srcText = "hello world"
	srcHash := ComputeSourceTextHash(srcText)
	seedSourceTrack(repo, "asset-1", "en", detail.TextTrackTranscript, "src-v1", srcText)

	order := []string{}
	rebuilder := &fakeSearchTextRebuilder{changed: true, order: &order}
	requester := &fakeIndexRequester{order: &order}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	m.SetSearchTextRebuilder(rebuilder)
	m.SetIndexRequester(requester)

	rep, err := m.Materialize(ctx, "asset-1", "en", srcHash, detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(rep.CreatedLanguages) != 1 || rep.CreatedLanguages[0] != "it" {
		t.Fatalf("expected created=[it], got %v", rep.CreatedLanguages)
	}

	if want := []string{"rebuild", "reindex"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("search_text rebuild MUST precede the reindex request: call order = %v, want %v", order, want)
	}
	if len(rebuilder.calls) != 1 || rebuilder.calls[0] != "asset-1" {
		t.Fatalf("expected exactly 1 rebuild for asset-1, got %v", rebuilder.calls)
	}
	if len(requester.calls) != 1 || requester.calls[0] != "asset-1" {
		t.Fatalf("expected exactly 1 reindex request for asset-1, got %v", requester.calls)
	}
	if got := atomic.LoadInt32(&ob.enqueueCalls); got != 0 {
		t.Fatalf("the operational SQLite outbox MUST NOT be used when the PostgreSQL reindex port is wired; enqueue calls = %d", got)
	}
}

// seedFullyTranslatedAsset reproduces the state the operator backfill finds
// across the pre-existing catalog: the source track AND every target track
// already READY with the exact translation key the materializer probes.
// Nothing is left to translate, so a create-only repair trigger would never
// fire. Returns the source text hash the materializer needs.
func seedFullyTranslatedAsset(repo *fakeTextTrackRepo, srcText string) string {
	return seedFullyTranslatedAssetFor(repo, "asset-1", srcText)
}

// seedFullyTranslatedAssetFor is the same fixture for an explicit asset id,
// so handler-level tests can use their own clip ids.
func seedFullyTranslatedAssetFor(repo *fakeTextTrackRepo, assetID, srcText string) string {
	srcHash := ComputeSourceTextHash(srcText)
	seedSourceTrack(repo, assetID, "en", detail.TextTrackTranscript, "src-v1", srcText)
	repo.tracks[key(assetID, "it", detail.TextTrackTranscript)] = &detail.TextTrack{
		ID:                 200,
		AssetID:            assetID,
		LanguageCode:       "it",
		TextKind:           detail.TextTrackTranscript,
		TextContent:        "[it] hello world",
		SourceType:         detail.TextSourceTranslation,
		SourceLanguageCode: "en",
		ModelVersion:       "model-v1",
		PromptVersion:      "prompt-v1",
		TextHash:           ComputeSourceTextHash("[it] hello world"),
		TranslationKey:     detail.TranslationKey(srcHash, "it", "", "model-v1", "prompt-v1"),
		IsCurrent:          true,
		Status:             detail.TextTrackReady,
	}
	return srcHash
}

// TestMaterialize_RepairsSearchTextForAlreadyTranslatedAsset is the
// regression for the repair half. Every language row already exists, so the
// run creates nothing; the rebuild must still run (that is the only way the
// pre-existing catalog can be repaired) and, because the rebuild reports a
// changed document, the reindex must be requested — on the PostgreSQL port,
// never the operational SQLite outbox.
func TestMaterialize_RepairsSearchTextForAlreadyTranslatedAsset(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	const srcText = "hello world"
	srcHash := seedFullyTranslatedAsset(repo, srcText)

	order := []string{}
	rebuilder := &fakeSearchTextRebuilder{changed: true, order: &order}
	requester := &fakeIndexRequester{order: &order}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	m.SetSearchTextRebuilder(rebuilder)
	m.SetIndexRequester(requester)

	rep, err := m.Materialize(ctx, "asset-1", "en", srcHash, detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(rep.CreatedLanguages) != 0 || len(rep.RetranslatedLanguages) != 0 {
		t.Fatalf("fixture must create nothing; got created=%v retranslated=%v", rep.CreatedLanguages, rep.RetranslatedLanguages)
	}
	if want := []string{"rebuild", "reindex"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("the repair MUST rebuild before it reindexes: call order = %v, want %v", order, want)
	}
	if !rep.IndexInputRebuilt {
		t.Fatal("report must record that the index input was rebuilt")
	}
	if !rep.ReindexRequested {
		t.Fatal("a rebuild that changed the document must request a reindex")
	}
	if got := atomic.LoadInt32(&ob.enqueueCalls); got != 0 {
		t.Fatalf("the repair MUST NOT fall back to the operational SQLite outbox; enqueue calls = %d", got)
	}
}

// TestMaterialize_NoOpRerunDoesNotReindexUnchangedDocument pins the
// idempotence half under the corrected contract: re-running over an asset
// whose translations are all READY and whose search_text is already correct
// rebuilds (cheap, and the only detector of "already correct") but requests
// NO reindex, so a repeated backfill never re-embeds an unchanged asset.
func TestMaterialize_NoOpRerunDoesNotReindexUnchangedDocument(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	const srcText = "hello world"
	srcHash := seedFullyTranslatedAsset(repo, srcText)

	rebuilder := &fakeSearchTextRebuilder{changed: false}
	requester := &fakeIndexRequester{}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	m.SetSearchTextRebuilder(rebuilder)
	m.SetIndexRequester(requester)

	rep, err := m.Materialize(ctx, "asset-1", "en", srcHash, detail.TextTrackTranscript, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(rebuilder.calls) != 1 {
		t.Fatalf("the rebuild must run so that 'unchanged' is observed rather than assumed; got %v", rebuilder.calls)
	}
	if len(requester.calls) != 0 {
		t.Fatalf("an unchanged document must not be reindexed; got %v", requester.calls)
	}
	if !rep.IndexInputRebuilt {
		t.Fatal("report must record that the index input was rebuilt")
	}
	if rep.ReindexRequested {
		t.Fatal("no reindex may be requested when nothing changed")
	}
	if rep.ReindexSkippedReason == "" {
		t.Fatal("an operator must be able to tell 'nothing to repair' apart from a silent no-op")
	}
	if got := atomic.LoadInt32(&ob.enqueueCalls); got != 0 {
		t.Fatalf("no change → no outbox emission; got %d", got)
	}
}

// TestMaterialize_RebuildFailureAbortsBeforeReindex pins the fail-closed
// ordering: a rebuild failure must surface as a Materialize error and must
// NOT be followed by a reindex request (which would embed the stale text).
func TestMaterialize_RebuildFailureAbortsBeforeReindex(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	const srcText = "hello world"
	srcHash := ComputeSourceTextHash(srcText)
	seedSourceTrack(repo, "asset-1", "en", detail.TextTrackTranscript, "src-v1", srcText)

	rebuildErr := errors.New("boom")
	rebuilder := &fakeSearchTextRebuilder{err: rebuildErr}
	requester := &fakeIndexRequester{}

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	m.SetSearchTextRebuilder(rebuilder)
	m.SetIndexRequester(requester)

	_, err := m.Materialize(ctx, "asset-1", "en", srcHash, detail.TextTrackTranscript, nil)
	if err == nil {
		t.Fatal("expected a Materialize error when the search_text rebuild fails")
	}
	if !errors.Is(err, rebuildErr) {
		t.Fatalf("error chain must wrap the rebuild error; got %v", err)
	}
	if len(requester.calls) != 0 {
		t.Fatalf("a failed rebuild MUST NOT be followed by a reindex request; got %v", requester.calls)
	}
	// Fail-closed means it fails CLOSED: a rebuild error must not fall back to
	// the operational SQLite outbox, which owns no media handler in any mode
	// and would dead-letter the event while looking like a successful emit.
	if got := atomic.LoadInt32(&ob.enqueueCalls); got != 0 {
		t.Fatalf("a failed rebuild MUST NOT fall back to the operational SQLite outbox; enqueue calls = %d", got)
	}
}

// TestMaterialize_LegacyOutboxFallbackWithoutReindexPort pins the
// back-compatible branch: without a PostgreSQL reindex port the materializer
// keeps emitting through the injected outbox (the behaviour every
// pre-existing test depends on).
func TestMaterialize_LegacyOutboxFallbackWithoutReindexPort(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	const srcText = "hello world"
	srcHash := ComputeSourceTextHash(srcText)
	seedSourceTrack(repo, "asset-1", "en", detail.TextTrackTranscript, "src-v1", srcText)

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")

	if _, err := m.Materialize(ctx, "asset-1", "en", srcHash, detail.TextTrackTranscript, nil); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if got := atomic.LoadInt32(&ob.enqueueCalls); got != 1 {
		t.Fatalf("legacy fallback must emit exactly one outbox event; got %d", got)
	}
}

// TestMaterializerSetters_NilSafe pins the godlike/07 fail-closed posture:
// wiring the ports on a nil materializer is an observable no-op rather than a
// panic (composition paths may hold a nil bundle in disabled mode).
func TestMaterializerSetters_NilSafe(t *testing.T) {
	var m *Materializer
	m.SetIndexRequester(&fakeIndexRequester{})
	m.SetSearchTextRebuilder(&fakeSearchTextRebuilder{})
}
