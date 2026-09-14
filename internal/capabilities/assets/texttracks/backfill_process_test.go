// Package texttracks — backfill_process_test.go: the Priority-1 rule of the
// transcript acquisition chain (POSTGRES-MEDIA-CUTOVER follow-up, September
// 2026).
//
// The chain is payload → DB → YouTube manual → YouTube auto → Whisper, and
// `AcquireService` already has direct coverage for priorities 2-5
// (acquire_test.go). What had NO coverage at all is the level above it:
// ProcessAsset decides whether the acquisition chain runs in the first place.
// Two rules live there, and both are load-bearing:
//
//  1. A READY transcript WITH timed cues satisfies the clip. Re-running a
//     backfill — or re-running it over the whole catalog, which is the whole
//     point of the repair pass — must NOT pay for a Whisper transcription of
//     a clip that already has its text. Priority 1 is the DB, not the
//     network.
//  2. A READY transcript WITHOUT cues does NOT satisfy the clip, because a
//     subtitle artifact cannot be built from untimed text. The chain runs on
//     purpose there. This is the case that made "re-running is free" false
//     in a subtle way, so it is pinned explicitly rather than left implicit.
package texttracks

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// newProcessAssetService wires the canonical BackfillService around the
// request's Materializer plus a real AcquireService whose two network legs
// count their calls.
func newProcessAssetService(
	t *testing.T,
	repo *fakeTextTrackRepo,
	materializer *Materializer,
	subs *stubSubtitles,
	whisp *stubWhisper,
) *BackfillService {
	t.Helper()
	acquirer, err := NewAcquireService(subs, whisp, zap.NewNop())
	if err != nil {
		t.Fatalf("NewAcquireService: %v", err)
	}
	artRepo := &subtitleArtifactRepoRecorder{}
	return &BackfillService{
		clips:           &clipsListerStub{item: &asset.Asset{ID: "asset-1", Source: asset.Source("youtube")}},
		repo:            repo,
		cues:            &cueWriterStub{},
		subArtRepo:      artRepo,
		subMaterializer: NewSubtitleArtifactMaterializer(artRepo, t.TempDir(), &subtitlePublisherRecorder{}),
		materializer:    materializer,
		acquirer:        acquirer,
		log:             zap.NewNop(),
	}
}

func processAssetOptions() BackfillOptions {
	return BackfillOptions{
		Source:          "youtube",
		SourceLanguage:  "en",
		TargetLanguages: []string{"en", "it"},
		TextKind:        detail.TextTrackTranscript,
	}
}

func processAssetItem() *asset.Asset {
	return &asset.Asset{ID: "asset-1", Source: asset.Source("youtube"), Filename: "asset-1.mp4", Duration: 30 * time.Second}
}

// TestProcessAsset_ReadyTranscriptWithCuesDoesNotReacquire is rule 1: the DB
// (Priority 1) already satisfies the clip, so neither YouTube subtitles nor
// Whisper may be invoked. Without this, a repair pass over the whole catalog
// would transcribe every already-complete clip.
func TestProcessAsset_ReadyTranscriptWithCuesDoesNotReacquire(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ob := &fakeOutbox{}

	const srcText = "the clip already has its transcript"
	// The full repair-pass shape: source AND every target language already
	// READY with the matching translation key.
	srcHash := seedFullyTranslatedAssetFor(repo, "asset-1", srcText)
	// Timed cues are what make the READY transcript genuinely usable.
	repo.cues[key("asset-1", "en", detail.TextTrackTranscript)] = []detail.TimedCue{
		{StartMs: 0, EndMs: 1200, Text: "the clip already has"},
	}
	repo.cues[key("asset-1", "it", detail.TextTrackTranscript)] = []detail.TimedCue{
		{StartMs: 0, EndMs: 1200, Text: "il clip ha gi\u00e0"},
	}

	m := newTestMaterializer(t, repo, &fakeTranslator{}, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	// Production wires the PostgreSQL index seam; wire it here too so the
	// repair is observable at this level (otherwise the ports are nil and the
	// repair booleans stay false, which would make the assertion vacuous).
	m.SetSearchTextRebuilder(&fakeSearchTextRebuilder{changed: true})
	m.SetIndexRequester(&fakeIndexRequester{})
	subs := &stubSubtitles{}
	whisp := &stubWhisper{}
	svc := newProcessAssetService(t, repo, m, subs, whisp)

	res, err := svc.ProcessAsset(ctx, processAssetItem(), processAssetOptions())
	if err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	if res.Err != "" {
		t.Fatalf("a READY source with cues must process cleanly; got err=%q", res.Err)
	}
	if !res.SourceReady {
		t.Fatal("the source must be reported READY straight from the database")
	}
	if res.SourceAcquired {
		t.Fatal("nothing may be acquired when the database already has the transcript")
	}
	if whisp.calls != 0 {
		t.Fatalf("Whisper calls = %d, want 0 — Priority 1 is the DB, not the network (this is what keeps a repair re-run cheap)", whisp.calls)
	}
	if subs.calls != 0 {
		t.Fatalf("YouTube subtitle calls = %d, want 0 when the database already satisfies the clip", subs.calls)
	}
	if srcHash == "" {
		t.Fatal("fixture must resolve a source hash")
	}
	if len(res.CreatedLangs) != 0 {
		t.Fatalf("the Italian translation was already READY, so nothing may be created; got %v", res.CreatedLangs)
	}
	// The repair half still runs, which is the point of re-processing: the
	// index input is recomposed even though nothing was translated.
	if !res.IndexRepaired {
		t.Fatal("a re-run over an already-translated clip must still repair the index input")
	}
}

// TestProcessAsset_ReadyTranscriptWithoutCuesReacquires pins rule 2: text
// without timing is not subtitle readiness, so the chain runs on purpose.
func TestProcessAsset_ReadyTranscriptWithoutCuesReacquires(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ob := &fakeOutbox{}

	const srcText = "text without timing"
	seedSourceTrack(repo, "asset-1", "en", detail.TextTrackTranscript, "src-v1", srcText)
	// No cues seeded: the READY row is text-only.

	m := newTestMaterializer(t, repo, &fakeTranslator{}, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	subs := &stubSubtitles{}
	whisp := &stubWhisper{}
	svc := newProcessAssetService(t, repo, m, subs, whisp)

	res, err := svc.ProcessAsset(ctx, processAssetItem(), processAssetOptions())
	if err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	if subs.calls+whisp.calls == 0 {
		t.Fatal("a READY transcript WITHOUT timed cues must trigger acquisition — untimed text cannot produce a subtitle artifact")
	}
	// The re-acquisition is FAIL-SOFT on purpose: a failed attempt must not
	// discard a text-only transcript that is still the best source the clip
	// has. The run continues and the translations are still produced.
	if res.Err != "" {
		t.Fatalf("a failed timed re-acquisition must not fail the clip; got err=%q", res.Err)
	}
	if !res.SourceReady {
		t.Fatal("the text-only transcript remains the source when re-acquisition fails")
	}
	if len(res.CreatedLangs) == 0 {
		t.Fatal("the clip must still be translated from the text-only source rather than being dropped")
	}
}

// TestProcessAsset_NoSourceUsesTheChainAndReportsFailure pins the third shape:
// no track at all, and a failing chain, must be reported as no-source rather
// than silently succeeding.
func TestProcessAsset_NoSourceUsesTheChainAndReportsFailure(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	ob := &fakeOutbox{}

	m := newTestMaterializer(t, repo, &fakeTranslator{}, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")
	subs := &stubSubtitles{}
	whisp := &stubWhisper{}
	svc := newProcessAssetService(t, repo, m, subs, whisp)

	res, err := svc.ProcessAsset(ctx, processAssetItem(), processAssetOptions())
	if err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	if res.Err != "no_source_track" {
		t.Fatalf("res.Err = %q, want no_source_track when the chain cannot produce a source", res.Err)
	}
	if subs.calls+whisp.calls == 0 {
		t.Fatal("the acquisition chain must be attempted before giving up")
	}
}
