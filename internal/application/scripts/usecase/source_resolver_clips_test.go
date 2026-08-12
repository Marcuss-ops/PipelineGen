package usecase

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

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
