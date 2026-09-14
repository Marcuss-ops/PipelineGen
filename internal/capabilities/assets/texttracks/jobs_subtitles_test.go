// Package texttracks — jobs_subtitles_test.go: the handoff test for the
// `asset.text.materialize` fast path (POSTGRES-MEDIA-CUTOVER follow-up,
// September 2026).
//
// jobs_subtitles.go adds the subtitle-artifact delivery to the fast path.
// backfill_subtitles_test.go already pins the delivery step in isolation, so
// what is left to prove here is the WIRING: that HandleJob actually reaches
// it. That is the exact thing that was missing — the fast path ran the
// translator and stopped — and a test of the step alone would keep passing
// if someone deleted the call.
//
// The test therefore drives the REAL handler with the REAL materializer and
// the REAL BackfillService (no method is stubbed out on the code under
// test): only the leaf ports (assets lister, cue writer, Drive publisher,
// artifact registry) are test doubles.
package texttracks

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// clipsListerStub answers the canonical asset lookup the handler performs
// before the delivery step.
type clipsListerStub struct {
	item *asset.Asset
	err  error
}

func (c *clipsListerStub) List(_ context.Context, _ asset.Filter) ([]*asset.Asset, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.item == nil {
		return nil, nil
	}
	return []*asset.Asset{c.item}, nil
}

// cueWriterStub satisfies the required constructor port. The fast path never
// writes cues (only the acquisition branch does), so any call is a bug.
type cueWriterStub struct {
	calls int
}

func (c *cueWriterStub) ReplaceTranscriptCues(context.Context, string, map[string][]detail.TimedCue) error {
	c.calls++
	return nil
}

func TestMaterializeJobHandler_FastPathDeliversPerLanguageSubtitles(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	const clipID = "yt_clip_fastpath_1"
	const srcText = "hello world from the committed clip"

	srcHash := seedFullyTranslatedAssetFor(repo, clipID, srcText)

	// Timed cues make the tracks subtitle-ready. Text without cues must NOT
	// produce a subtitle; that distinction is the delivery step's job, so
	// both languages are given cues here.
	repo.cues[key(clipID, "en", detail.TextTrackTranscript)] = []detail.TimedCue{
		{StartMs: 0, EndMs: 1200, Text: "hello world"},
	}
	repo.cues[key(clipID, "it", detail.TextTrackTranscript)] = []detail.TimedCue{
		{StartMs: 0, EndMs: 1200, Text: "ciao mondo"},
	}

	// The materializer is real; nothing is left to translate, which is the
	// shape of every clip committed before the translation stage learned to
	// deliver its subtitles.
	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en", "it"}, "model-v1", "prompt-v1")

	artRepo := &subtitleArtifactRepoRecorder{}
	pub := &subtitlePublisherRecorder{}
	cues := &cueWriterStub{}
	backfill := &BackfillService{
		clips: &clipsListerStub{item: &asset.Asset{
			ID:       clipID,
			Source:   asset.Source("youtube"),
			Filename: clipID + ".mp4",
			Duration: 30 * time.Second,
		}},
		repo:            repo,
		cues:            cues,
		subArtRepo:      artRepo,
		subMaterializer: NewSubtitleArtifactMaterializer(artRepo, t.TempDir(), pub),
		driveFolderID:   "drive-folder-live",
		log:             zap.NewNop(),
	}

	handler := NewMaterializeJobHandler(m, zap.NewNop()).WithBackfill(backfill)

	// A committed source hash is what selects the fast path.
	payload, err := json.Marshal(MaterializeJobPayload{
		AssetID:        clipID,
		SourceLanguage: "en",
		SourceTextHash: srcHash,
		TextKinds:      []string{string(detail.TextTrackTranscript)},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	result, err := handler.HandleJob(ctx, &job.Job{ID: "job-fastpath-1", Type: asset.TypeTextMaterialize, Payload: payload}, nil)
	if err != nil {
		t.Fatalf("HandleJob: %v", err)
	}

	// The regression: this key was absent when the fast path stopped after
	// translation, so a freshly extracted clip reached Drive with no
	// subtitle files at all.
	raw, ok := result["subtitles"]
	if !ok || raw == nil {
		t.Fatal("the materialize fast path MUST report subtitle delivery — without it a clip committed with a source hash gets no subtitle artifacts")
	}
	delivery, ok := raw.(*SubtitleDeliveryReport)
	if !ok {
		t.Fatalf("subtitles result must be a *SubtitleDeliveryReport, got %T", raw)
	}
	if delivery.Skipped {
		t.Fatalf("a youtube clip must not be skipped; reason=%q", delivery.SkipReason)
	}
	if delivery.Delivered != 2 {
		t.Fatalf("delivered = %d, want 2 (the source and the already-translated language)", delivery.Delivered)
	}
	if len(delivery.Failed) != 0 {
		t.Fatalf("unexpected per-language failures: %v", delivery.Failed)
	}
	if len(pub.requests) != 2 {
		t.Fatalf("Drive publishes = %d, want 2 — every READY language must reach Drive", len(pub.requests))
	}
	if cues.calls != 0 {
		t.Fatalf("the fast path must not write cues (that is the acquisition branch's job); calls = %d", cues.calls)
	}
}

// TestMaterializeJobHandler_NoTranscriptKindSkipsSubtitleDelivery pins the
// scope: a payload that materializes only non-transcript kinds must not
// attempt subtitle delivery (subtitles describe a transcript's timing).
func TestMaterializeJobHandler_NoTranscriptKindSkipsSubtitleDelivery(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	tr := &fakeTranslator{}
	ob := &fakeOutbox{}

	const clipID = "yt_clip_desc_only"
	const srcText = "a description only payload"
	// A description source that is already READY, so the materialization
	// itself succeeds and the only question left is whether the subtitle
	// delivery is attempted for a non-transcript kind.
	srcHash := ComputeSourceTextHash(srcText)
	seedSourceTrack(repo, clipID, "en", detail.TextTrackDescription, "src-v1", srcText)

	m := newTestMaterializer(t, repo, tr, ob, "en", []string{"en"}, "model-v1", "prompt-v1")
	pub := &subtitlePublisherRecorder{}
	artRepo := &subtitleArtifactRepoRecorder{}
	backfill := &BackfillService{
		clips:           &clipsListerStub{item: &asset.Asset{ID: clipID, Source: asset.Source("youtube")}},
		repo:            repo,
		cues:            &cueWriterStub{},
		subArtRepo:      artRepo,
		subMaterializer: NewSubtitleArtifactMaterializer(artRepo, t.TempDir(), pub),
		log:             zap.NewNop(),
	}
	handler := NewMaterializeJobHandler(m, zap.NewNop()).WithBackfill(backfill)

	payload, err := json.Marshal(MaterializeJobPayload{
		AssetID:        clipID,
		SourceLanguage: "en",
		SourceTextHash: srcHash,
		TextKinds:      []string{string(detail.TextTrackDescription)},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	result, err := handler.HandleJob(ctx, &job.Job{ID: "job-desc-1", Payload: payload}, nil)
	if err != nil {
		t.Fatalf("HandleJob: %v", err)
	}
	if _, present := result["subtitles"]; present {
		t.Fatal("a non-transcript payload must not trigger subtitle delivery")
	}
	if len(pub.requests) != 0 {
		t.Fatalf("no subtitle may be published; publishes = %d", len(pub.requests))
	}
}
