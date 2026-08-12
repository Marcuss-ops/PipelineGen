package usecase

import (
	"context"
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
