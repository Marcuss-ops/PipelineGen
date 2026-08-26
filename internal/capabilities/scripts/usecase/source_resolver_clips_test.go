package usecase

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

func TestClipsResolver_AdvancedSegmentsUseOnlyCanonicalIDs(t *testing.T) {
	recorder := &recordClipBuilder{
		fakeClipBuilder: fakeClipBuilder{ev: makePackForIDs([]string{"intro", "segment-a", "segment-b"})},
	}
	resolver := &ClipsSourceResolver{clipBuilder: recorder, log: zap.NewNop()}

	source := scriptpkg.SourceSpec{
		Type:             scriptpkg.SourceClips,
		IntroClipIDs:     []string{"intro"},
		OrderingStrategy: "chronological",
	}
	resolution := makeTestResCtx()
	resolution.Segments = []scriptpkg.ScriptSegment{
		{ID: "intro", Kind: "intro", Topic: "Opening", ClipIDs: []string{"segment-a", "segment-b"}},
		{ID: "narration", Kind: "scene", Topic: "Narration", ClipIDs: []string{}},
	}

	resolved, err := resolver.Resolve(context.Background(), source, resolution)
	if err != nil {
		t.Fatalf("advanced clips payload rejected: %v", err)
	}
	if got, want := recorder.lastIDs, []string{"intro", "segment-a", "segment-b"}; !equalClipIDs(got, want) {
		t.Fatalf("resolver fetched %v, want %v", got, want)
	}
	if recorder.lastOpts == nil || len(recorder.lastOpts.Segments) != 2 {
		t.Fatalf("resolver did not pass canonical segments: %+v", recorder.lastOpts)
	}
	if recorder.lastOpts.OrderingStrategy != "" {
		t.Fatalf("explicit clips resolver allowed ordering strategy %q to override payload order", recorder.lastOpts.OrderingStrategy)
	}
	if got := recorder.lastOpts.Segments[0].ClipIDs; !equalClipIDs(got, []string{"intro", "segment-a", "segment-b"}) {
		t.Fatalf("first segment ownership = %v, want intro + declared IDs", got)
	}
	if got := recorder.lastOpts.Segments[1].ClipIDs; len(got) != 0 {
		t.Fatalf("explicit empty segment received clips: %v", got)
	}
	if resolved.ClipEvidence == nil || len(resolved.ClipEvidence.SegmentEvidence) != 2 {
		t.Fatalf("per-segment evidence was not attached: %+v", resolved.ClipEvidence)
	}
	if len(resolved.Segments) != 2 || !equalClipIDs(resolved.Segments[0].ClipIDs, []string{"intro", "segment-a", "segment-b"}) || resolved.Segments[1].ClipIDs == nil || len(resolved.Segments[1].ClipIDs) != 0 {
		t.Fatalf("resolved source did not expose the same canonical segment ownership: %+v", resolved.Segments)
	}
	if got := resolved.ClipEvidence.SegmentEvidence[0].ClipIDs; !equalClipIDs(got, []string{"intro", "segment-a", "segment-b"}) {
		t.Fatalf("segment evidence ownership = %v", got)
	}
}

func TestClipsResolver_LegacyRootAndIntroFallback(t *testing.T) {
	recorder := &recordClipBuilder{
		fakeClipBuilder: fakeClipBuilder{ev: makePackForIDs([]string{"intro", "a", "b", "c"})},
	}
	resolver := &ClipsSourceResolver{clipBuilder: recorder, log: zap.NewNop()}

	source := scriptpkg.SourceSpec{
		Type:         scriptpkg.SourceClips,
		ClipIDs:      []string{"a", "b", "c"},
		IntroClipIDs: []string{"intro"},
	}
	resolution := makeTestResCtx()
	resolution.Segments = []scriptpkg.ScriptSegment{
		{ID: "one", Topic: "One"},
		{ID: "two", Topic: "Two"},
	}

	if _, err := resolver.Resolve(context.Background(), source, resolution); err != nil {
		t.Fatalf("legacy clips payload rejected: %v", err)
	}
	if got, want := recorder.lastIDs, []string{"intro", "a", "b", "c"}; !equalClipIDs(got, want) {
		t.Fatalf("legacy resolver fetched %v, want %v", got, want)
	}
	if got := recorder.lastOpts.Segments[0].ClipIDs; !equalClipIDs(got, []string{"intro", "a"}) {
		t.Fatalf("legacy first segment ownership = %v", got)
	}
	if got := recorder.lastOpts.Segments[1].ClipIDs; !equalClipIDs(got, []string{"b", "c"}) {
		t.Fatalf("legacy final segment ownership = %v", got)
	}
}

func equalClipIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type hydrationSubtitleRepository struct {
	artifacts []detail.SubtitleArtifact
}

func (r *hydrationSubtitleRepository) Upsert(_ context.Context, _ *detail.SubtitleArtifact) error {
	return nil
}

func (r *hydrationSubtitleRepository) FindCurrent(_ context.Context, _ string, _ string, _ detail.SubtitleFormat) (*detail.SubtitleArtifact, error) {
	return nil, nil
}

func (r *hydrationSubtitleRepository) ListByAsset(_ context.Context, assetID string) ([]detail.SubtitleArtifact, error) {
	out := make([]detail.SubtitleArtifact, 0, len(r.artifacts))
	for _, artifact := range r.artifacts {
		if artifact.AssetID == assetID {
			out = append(out, artifact)
		}
	}
	return out, nil
}

func TestClipSourceBuilder_HydratesAssetTimingAndSubtitleWithoutSearch(t *testing.T) {
	resolver := newFakeClipResolver()
	clip := makeTestClip("clip-a", "Clip A", 2*time.Second)
	clip.SetMetadataInt("start_ms", 1250)
	clip.SetMetadataInt("end_ms", 4750)
	resolver.AddClip(clip)

	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{
		tracks: map[string]*detail.TextTrack{"clip-a:en": makeTrack("clip-a", "en", "hydrated transcript")},
	})
	builder.ConfigureSubtitleArtifactRepository(&hydrationSubtitleRepository{artifacts: []detail.SubtitleArtifact{{
		AssetID: "clip-a", Format: detail.SubtitleFormatASS, Status: detail.SubtitleStatusReady, IsCurrent: true,
		DriveURL: "https://drive/subtitle-a", DriveFileID: "subtitle-a",
	}}})

	evidence, _, _, err := builder.BuildClipContext(context.Background(), []string{"clip-a"}, &ClipGenerationOptions{Language: "en", RequireDriveLink: true})
	if err != nil {
		t.Fatalf("BuildClipContext failed: %v", err)
	}
	detail, ok := evidence.ClipDetails["clip-a"]
	if !ok {
		t.Fatalf("hydrated clip detail missing: %+v", evidence.ClipDetails)
	}
	if detail.StartMs != 1250 || detail.EndMs != 4750 {
		t.Fatalf("timing was not hydrated from the asset: %+v", detail)
	}
	if detail.DriveLink != clip.DriveLink() {
		t.Fatalf("DriveLink = %q, want %q", detail.DriveLink, clip.DriveLink())
	}
	if detail.SubtitleLink != "https://drive/subtitle-a" || detail.SubtitleFileID != "subtitle-a" {
		t.Fatalf("subtitle link/file ID were not hydrated: %+v", detail)
	}

	resolver.mu.RLock()
	mediaCalls := append([]string(nil), resolver.mediaCalls...)
	driveCalls := append([]string(nil), resolver.driveCalls...)
	resolver.mu.RUnlock()
	if !equalClipIDs(mediaCalls, []string{"clip-a"}) || len(driveCalls) != 0 {
		t.Fatalf("hydration performed unexpected lookups: media=%v drive=%v", mediaCalls, driveCalls)
	}
}

func TestClipSourceBuilder_StrictTranscriptPolicyRejectsMissingTranscript(t *testing.T) {
	resolver := newFakeClipResolver()
	resolver.AddClip(makeTestClip("clip-strict", "Strict", time.Second))
	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{tracks: map[string]*detail.TextTrack{}})

	_, _, _, err := builder.BuildClipContext(context.Background(), []string{"clip-strict"}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: scriptpkg.TranscriptPolicyStrict,
	})
	if err == nil {
		t.Fatal("strict transcript policy must reject a clip without a READY transcript")
	}
}

func TestClipSourceBuilder_AllowsSummaryWithoutTranscript(t *testing.T) {
	resolver := newFakeClipResolver()
	clip := makeTestClip("clip-summary", "Summary clip", time.Second)
	clip.SetMetadataString("summary", "A boxer explains how footwork changes the pace of a fight.")
	resolver.AddClip(clip)
	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{tracks: map[string]*detail.TextTrack{}})

	evidence, _, _, err := builder.BuildClipContext(context.Background(), []string{"clip-summary"}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: scriptpkg.TranscriptPolicyStrict, AllowMetadataFallback: true,
	})
	if err != nil {
		t.Fatalf("summary evidence should authorize a transcript-free clip: %v", err)
	}
	if evidence == nil || evidence.ClipDetails["clip-summary"].Description == "" {
		t.Fatalf("summary evidence was not preserved: %+v", evidence)
	}
	if evidence.ClipDetails["clip-summary"].Transcript != "" {
		t.Fatal("summary fallback must not fabricate transcript content")
	}
}

func TestClipSourceBuilder_UnknownTranscriptPolicyRejectsMissingTranscript(t *testing.T) {
	resolver := newFakeClipResolver()
	resolver.AddClip(makeTestClip("clip-unknown-policy", "Unknown policy", time.Second))
	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{tracks: map[string]*detail.TextTrack{}})

	_, _, _, err := builder.BuildClipContext(context.Background(), []string{"clip-unknown-policy"}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: "unsupported", SourceText: "EXPLICIT_SOURCE_TEXT",
	})
	if err == nil {
		t.Fatal("unknown transcript policy must not authorize source_text fallback")
	}
}

func TestClipSourceBuilder_StrictPolicyAllowsFallbackOnlyForExplicitSourceText(t *testing.T) {
	resolver := newFakeClipResolver()
	resolver.AddClip(makeTestClip("clip-fallback", "Fallback", time.Second))
	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{tracks: map[string]*detail.TextTrack{}})

	evidence, _, _, err := builder.BuildClipContext(context.Background(), []string{"clip-fallback"}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: scriptpkg.TranscriptPolicyStrict, SourceText: "EXPLICIT_GLOBAL_SOURCE_TEXT",
	})
	if err != nil {
		t.Fatalf("explicit global source_text should authorize strict fallback: %v", err)
	}
	if evidence == nil || evidence.ClipDetails["clip-fallback"].Transcript != "" {
		t.Fatalf("fallback must retain clip metadata without fabricating transcript evidence: %+v", evidence)
	}

	_, _, _, err = builder.BuildClipContext(context.Background(), []string{"clip-fallback"}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: scriptpkg.TranscriptPolicyStrict,
		Segments: []scriptpkg.ScriptSegment{{ID: "scene", ClipIDs: []string{"clip-fallback"}, SourceText: "EXPLICIT_SEGMENT_SOURCE_TEXT"}},
	})
	if err != nil {
		t.Fatalf("explicit segment source_text should authorize strict fallback: %v", err)
	}

	_, _, _, err = builder.BuildClipContext(context.Background(), []string{"clip-fallback"}, &ClipGenerationOptions{
		Language: "en", TranscriptPolicy: scriptpkg.TranscriptPolicyStrict,
		Segments: []scriptpkg.ScriptSegment{{ID: "scene", ClipIDs: []string{"other-clip"}, SourceText: "SOURCE_FOR_ANOTHER_CLIP"}},
	})
	if err == nil {
		t.Fatal("source_text belonging to another clip must not authorize transcript fallback")
	}
}

func TestClipsResolver_GlobalSourceTextSurvivesTranscriptFallback(t *testing.T) {
	recorder := &recordClipBuilder{
		fakeClipBuilder: fakeClipBuilder{ev: makePackForIDs([]string{"clip-source-text"})},
	}
	resolver := &ClipsSourceResolver{clipBuilder: recorder, log: zap.NewNop()}

	resolved, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type:             scriptpkg.SourceClips,
		ClipIDs:          []string{"clip-source-text"},
		SourceText:       "EXPLICIT_GLOBAL_SOURCE_TEXT",
		TranscriptPolicy: scriptpkg.TranscriptPolicyStrict,
	}, makeTestResCtx())
	if err != nil {
		t.Fatalf("explicit global source_text should survive clip resolution: %v", err)
	}
	if resolved.SourceText != "EXPLICIT_GLOBAL_SOURCE_TEXT" {
		t.Fatalf("resolved source text = %q, want explicit global source text", resolved.SourceText)
	}
}

func TestClipsResolver_RejectsSilentEmptyClipEvidence(t *testing.T) {
	recorder := &recordClipBuilder{fakeClipBuilder: fakeClipBuilder{ev: nil}}
	resolver := &ClipsSourceResolver{clipBuilder: recorder, log: zap.NewNop()}
	_, err := resolver.Resolve(context.Background(), scriptpkg.SourceSpec{
		Type: scriptpkg.SourceClips, ClipIDs: []string{"declared-clip"}, TranscriptPolicy: scriptpkg.TranscriptPolicyStrict,
	}, makeTestResCtx())
	if err == nil || !strings.Contains(err.Error(), "empty clip evidence") {
		t.Fatalf("silent empty evidence must fail explicitly, got %v", err)
	}
}

func TestClipsResolver_HydratesExplicitSegmentsWithoutSearch(t *testing.T) {
	evidence := &scriptpkg.ClipEvidence{
		AcceptedClipIDs: []string{"intro-clip", "clip-b", "clip-a"},
		ClipDetails: map[string]scriptpkg.ClipDetail{
			"intro-clip": {
				Name: "Intro", StartMs: 10, EndMs: 610,
				DriveLink: "https://drive/intro", SubtitleLink: "https://drive/sub-intro", SubtitleFileID: "sub-intro",
			},
			"clip-a": {
				Name: "A", StartMs: 200, EndMs: 1200,
				DriveLink: "https://drive/a", SubtitleLink: "https://drive/sub-a", SubtitleFileID: "sub-a",
			},
			"clip-b": {
				Name: "B", StartMs: 20, EndMs: 520,
				DriveLink: "https://drive/b", SubtitleLink: "https://drive/sub-b", SubtitleFileID: "sub-b",
			},
		},
	}
	recorder := &recordClipBuilder{fakeClipBuilder: fakeClipBuilder{ev: evidence}}
	resolver := &ClipsSourceResolver{clipBuilder: recorder, log: zap.NewNop()}

	source := scriptpkg.SourceSpec{
		Type:         scriptpkg.SourceClips,
		ClipIDs:      []string{"legacy-root-must-be-ignored"},
		IntroClipIDs: []string{"intro-clip"},
	}
	resolution := makeTestResCtx()
	resolution.Segments = []scriptpkg.ScriptSegment{
		{ID: "scene-1", Kind: "scene", Topic: "First", ClipIDs: []string{"clip-b", "clip-a"}},
		{ID: "intro", Kind: "intro", Topic: "Opening", ClipIDs: []string{}},
		{ID: "scene-3", Kind: "scene", Topic: "No visuals", ClipIDs: []string{}},
		{ID: "scene-4", Kind: "scene", Topic: "Narration", ClipIDs: nil},
	}

	resolved, err := resolver.Resolve(context.Background(), source, resolution)
	if err != nil {
		t.Fatalf("explicit hydration failed: %v", err)
	}
	wantRequested := []string{"intro-clip", "clip-b", "clip-a"}
	if !equalClipIDs(recorder.lastIDs, wantRequested) {
		t.Fatalf("hydration requested %v, want only declared ordered IDs %v", recorder.lastIDs, wantRequested)
	}
	if recorder.lastOpts == nil || recorder.lastOpts.OrderingStrategy != "" {
		t.Fatalf("explicit hydration must disable resolver reordering: opts=%+v", recorder.lastOpts)
	}
	if resolved.ClipEvidence == nil || len(resolved.ClipEvidence.SegmentEvidence) != 4 {
		t.Fatalf("segment evidence missing: %+v", resolved.ClipEvidence)
	}

	segments := resolved.ClipEvidence.SegmentEvidence
	if !equalClipIDs(segments[0].ClipIDs, []string{"clip-b", "clip-a"}) {
		t.Fatalf("scene-1 membership/order = %v", segments[0].ClipIDs)
	}
	if len(segments[1].ClipIDs) != 1 || segments[1].ClipIDs[0] != "intro-clip" {
		t.Fatalf("intro membership/order = %v", segments[1].ClipIDs)
	}
	if segments[2].ClipIDs == nil || len(segments[2].ClipIDs) != 0 {
		t.Fatalf("explicit zero-clip segment was not preserved: %v", segments[2].ClipIDs)
	}
	if segments[3].ClipIDs != nil {
		t.Fatalf("nil narration-only segment unexpectedly received clips: %v", segments[3].ClipIDs)
	}

	for _, check := range []struct {
		segment int
		clipID  string
		start   int64
		end     int64
		drive   string
		sub     string
		fileID  string
	}{
		{0, "clip-b", 20, 520, "https://drive/b", "https://drive/sub-b", "sub-b"},
		{0, "clip-a", 200, 1200, "https://drive/a", "https://drive/sub-a", "sub-a"},
		{1, "intro-clip", 10, 610, "https://drive/intro", "https://drive/sub-intro", "sub-intro"},
	} {
		detail, ok := segments[check.segment].Clips[check.clipID]
		if !ok {
			t.Fatalf("segment[%d] missing hydrated detail for %q", check.segment, check.clipID)
		}
		if detail.StartMs != check.start || detail.EndMs != check.end || detail.DriveLink != check.drive || detail.SubtitleLink != check.sub || detail.SubtitleFileID != check.fileID {
			t.Errorf("detail[%q] = %+v, want timing/link/subtitle preservation", check.clipID, detail)
		}
	}

	encoded, err := json.Marshal(segments)
	if err != nil {
		t.Fatalf("marshal segment evidence: %v", err)
	}
	encodedText := string(encoded)
	if !strings.Contains(encodedText, `"clip_ids":[]`) {
		t.Fatalf("explicit empty clip_ids was omitted from serialized hydration: %s", encodedText)
	}
	if !strings.Contains(encodedText, `"clip_ids":null`) {
		t.Fatalf("nil narration clip_ids did not remain distinguishable from explicit empty: %s", encodedText)
	}
}
