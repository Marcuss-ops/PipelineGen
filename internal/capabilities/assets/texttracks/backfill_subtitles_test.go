// Package texttracks — backfill_subtitles_test.go: pins the canonical
// per-language subtitle-artifact delivery step (POSTGRES-MEDIA-CUTOVER
// follow-up, September 2026).
//
// This step now has two callers — the operator backfill and the
// `asset.text.materialize` job fast path that the direct YouTube fan-out
// uses — so the contract under test is the contract both depend on: every
// READY language gets its own artifact, and an inapplicable asset says so
// instead of silently doing nothing.
package texttracks

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// subtitleTrackRepoStub implements exactly the two reads the delivery step
// performs. The embedded interface is deliberately left nil so any new
// dependency the step grows panics loudly in tests instead of passing on a
// zero value.
type subtitleTrackRepoStub struct {
	detail.TextTrackRepository
	ready map[string][]detail.TimedCue
}

func (r *subtitleTrackRepoStub) ListReadyLanguages(_ context.Context, _ string, _ detail.TextTrackKind) ([]string, error) {
	langs := make([]string, 0, len(r.ready))
	for lang, cues := range r.ready {
		if len(cues) > 0 {
			langs = append(langs, lang)
		}
	}
	return langs, nil
}

func (r *subtitleTrackRepoStub) FindReady(_ context.Context, _, lang string, _ detail.TextTrackKind) (*detail.TextTrack, []detail.TimedCue, error) {
	cues, ok := r.ready[lang]
	if !ok {
		return nil, nil, nil
	}
	return &detail.TextTrack{
		ID:           7,
		LanguageCode: lang,
		TextKind:     detail.TextTrackTranscript,
		Status:       detail.TextTrackReady,
		IsCurrent:    true,
	}, cues, nil
}

func newSubtitleDeliveryService(t *testing.T, ready map[string][]detail.TimedCue) (*BackfillService, *subtitlePublisherRecorder) {
	t.Helper()
	artRepo := &subtitleArtifactRepoRecorder{}
	pub := &subtitlePublisherRecorder{}
	return &BackfillService{
		repo:            &subtitleTrackRepoStub{ready: ready},
		subArtRepo:      artRepo,
		subMaterializer: NewSubtitleArtifactMaterializer(artRepo, t.TempDir(), pub),
		driveFolderID:   "folder-default",
		log:             zap.NewNop(),
	}, pub
}

func youtubeClip(id string) *asset.Asset {
	return &asset.Asset{
		ID:       id,
		Source:   asset.Source("youtube"),
		Filename: id + ".mp4",
		Duration: 2 * time.Second,
	}
}

// TestMaterializeSubtitleArtifacts_DeliversEveryReadyLanguage is the core
// regression: a YouTube clip with its configured translations must end up
// with one uploaded subtitle artifact per READY language, not just the
// original. This is the guarantee the direct YouTube path lacked.
func TestMaterializeSubtitleArtifacts_DeliversEveryReadyLanguage(t *testing.T) {
	ready := map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1000, Text: "hello"}},
		"it": {{StartMs: 0, EndMs: 1000, Text: "ciao"}},
		"de": {{StartMs: 0, EndMs: 1000, Text: "hallo"}},
	}
	svc, pub := newSubtitleDeliveryService(t, ready)

	rep, err := svc.MaterializeSubtitleArtifacts(
		context.Background(), youtubeClip("clip-1"), "en", []string{"it", "de"}, detail.TextTrackTranscript,
	)
	if err != nil {
		t.Fatalf("MaterializeSubtitleArtifacts: %v", err)
	}
	if rep.Skipped {
		t.Fatalf("a youtube clip must not be skipped; reason=%q", rep.SkipReason)
	}
	if rep.Delivered != 3 {
		t.Fatalf("delivered = %d, want 3 (one artifact per READY language)", rep.Delivered)
	}
	if len(pub.requests) != 3 {
		t.Fatalf("Drive publishes = %d, want 3 (every language must reach Drive, not only the original)", len(pub.requests))
	}
	if len(rep.Failed) != 0 {
		t.Fatalf("unexpected per-language failures: %v", rep.Failed)
	}
	// The languages are reported so an operator can see the delivered set.
	if len(rep.Languages) != 3 {
		t.Fatalf("languages = %v, want the 3 READY languages", rep.Languages)
	}
}

// TestMaterializeSubtitleArtifacts_IsIdempotent pins that a second delivery
// over identical content reuses the recorded artifact instead of uploading a
// duplicate — the fan-out may run again for the same clip, and Drive must
// not accumulate copies.
func TestMaterializeSubtitleArtifacts_IsIdempotent(t *testing.T) {
	ready := map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1000, Text: "hello"}},
	}
	svc, pub := newSubtitleDeliveryService(t, ready)
	ctx := context.Background()

	if _, err := svc.MaterializeSubtitleArtifacts(ctx, youtubeClip("clip-1"), "en", nil, detail.TextTrackTranscript); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if len(pub.requests) != 1 {
		t.Fatalf("first delivery publishes = %d, want 1", len(pub.requests))
	}
	if _, err := svc.MaterializeSubtitleArtifacts(ctx, youtubeClip("clip-1"), "en", nil, detail.TextTrackTranscript); err != nil {
		t.Fatalf("second delivery: %v", err)
	}
	if len(pub.requests) != 1 {
		t.Fatalf("an identical re-delivery MUST reuse the artifact; publishes = %d, want 1", len(pub.requests))
	}
}

// TestMaterializeSubtitleArtifacts_SkipReasonsAreExplicit pins the
// fail-honest posture: every inapplicable case is named, so "no subtitles
// expected" is never confused with "delivery silently did nothing".
func TestMaterializeSubtitleArtifacts_SkipReasonsAreExplicit(t *testing.T) {
	ready := map[string][]detail.TimedCue{
		"en": {{StartMs: 0, EndMs: 1000, Text: "hello"}},
	}
	svc, pub := newSubtitleDeliveryService(t, ready)
	ctx := context.Background()

	cases := []struct {
		name   string
		asset  *asset.Asset
		kind   detail.TextTrackKind
		reason string
	}{
		{name: "no asset", asset: nil, kind: detail.TextTrackTranscript, reason: "no_asset"},
		{name: "empty id", asset: &asset.Asset{Source: asset.Source("youtube")}, kind: detail.TextTrackTranscript, reason: "no_asset"},
		{
			name:   "source does not take subtitles",
			asset:  &asset.Asset{ID: "clip-1", Source: asset.Source("artlist")},
			kind:   detail.TextTrackTranscript,
			reason: "source_does_not_take_subtitles",
		},
		{
			name:   "kind is not transcript",
			asset:  youtubeClip("clip-1"),
			kind:   detail.TextTrackDescription,
			reason: "text_kind_is_not_transcript",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := svc.MaterializeSubtitleArtifacts(ctx, tc.asset, "en", nil, tc.kind)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !rep.Skipped || rep.SkipReason != tc.reason {
				t.Fatalf("skipped=%v reason=%q, want skipped=true reason=%q", rep.Skipped, rep.SkipReason, tc.reason)
			}
		})
	}
	if len(pub.requests) != 0 {
		t.Fatalf("no skip case may publish; publishes = %d", len(pub.requests))
	}
}

// TestMaterializeSubtitleArtifacts_NilMaterializerIsSkippedNotPanicked pins
// the degraded-composition contract: a service built without a subtitle
// materializer reports the skip instead of panicking on boot paths that
// legitimately omit it.
func TestMaterializeSubtitleArtifacts_NilMaterializerIsSkippedNotPanicked(t *testing.T) {
	svc := &BackfillService{repo: &subtitleTrackRepoStub{}, log: zap.NewNop()}
	rep, err := svc.MaterializeSubtitleArtifacts(context.Background(), youtubeClip("clip-1"), "en", nil, detail.TextTrackTranscript)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.Skipped || rep.SkipReason != "no_subtitle_materializer" {
		t.Fatalf("skipped=%v reason=%q, want the explicit no_subtitle_materializer skip", rep.Skipped, rep.SkipReason)
	}
}

// TestContainsTextKind pins the payload predicate the job handler uses to
// decide whether subtitle delivery applies to a materialize job.
func TestContainsTextKind(t *testing.T) {
	if !containsTextKind([]string{"description", "transcript"}, detail.TextTrackTranscript) {
		t.Fatal("transcript present in a multi-kind payload must be detected")
	}
	if containsTextKind([]string{"description"}, detail.TextTrackTranscript) {
		t.Fatal("a payload without transcript must not trigger subtitle delivery")
	}
	if containsTextKind(nil, detail.TextTrackTranscript) {
		t.Fatal("an empty payload must not trigger subtitle delivery")
	}
}
