package script

import (
	"encoding/json"
	"testing"
)

func TestCanonicalizeSegmentClipIDs_ExplicitSegmentsWin(t *testing.T) {
	source := SourceSpec{
		Type:         SourceClips,
		ClipIDs:      []string{"legacy-a", "legacy-b"},
		IntroClipIDs: []string{"intro"},
	}
	segments := []ScriptSegment{
		{ID: "intro", Topic: "Opening", ClipIDs: []string{"clip-a", "clip-b"}},
		{ID: "scene-2", Topic: "Second", ClipIDs: []string{}},
	}

	got := CanonicalizeSegmentClipIDs(source, segments)
	if len(got) != 2 || len(got[0].ClipIDs) != 3 || got[0].ClipIDs[0] != "intro" || got[0].ClipIDs[1] != "clip-a" || got[0].ClipIDs[2] != "clip-b" {
		t.Fatalf("explicit segment ownership was not preserved: %+v", got)
	}
	if len(got[1].ClipIDs) != 0 {
		t.Fatalf("legacy root clip_ids leaked into an explicit empty segment: %+v", got[1].ClipIDs)
	}
}

func TestCanonicalizeSegmentClipIDs_ExplicitEmptySegmentsWin(t *testing.T) {
	source := SourceSpec{Type: SourceClips, ClipIDs: []string{"legacy-a", "legacy-b"}}
	segments := []ScriptSegment{{ID: "empty", Topic: "Narration", ClipIDs: []string{}}, {ID: "also-empty", Topic: "More", ClipIDs: []string{}}}
	got := CanonicalizeSegmentClipIDs(source, segments)
	if !HasExplicitSegmentClipIDs(segments) {
		t.Fatal("non-nil empty clip_ids must select advanced ownership")
	}
	for i, segment := range got {
		if len(segment.ClipIDs) != 0 {
			t.Fatalf("legacy root clip_ids leaked into explicit empty segment[%d]: %v", i, segment.ClipIDs)
		}
	}
}

func TestCanonicalizeSegmentClipIDs_LegacyRootCompatibility(t *testing.T) {
	source := SourceSpec{Type: SourceClips, ClipIDs: []string{"a", "b", "c"}, IntroClipIDs: []string{"intro"}}
	segments := []ScriptSegment{{ID: "one", Topic: "One"}, {ID: "two", Topic: "Two"}}

	got := CanonicalizeSegmentClipIDs(source, segments)
	if got[0].ClipIDs[0] != "intro" || got[0].ClipIDs[1] != "a" || got[1].ClipIDs[0] != "b" || got[1].ClipIDs[1] != "c" {
		t.Fatalf("legacy IDs were not assigned in order: %+v", got)
	}
	if source.ClipIDs[0] != "a" || len(source.IntroClipIDs) != 1 {
		t.Fatal("canonicalization mutated the caller source")
	}
}

func TestBuildSegmentClipEvidencePreservesZeroToManyOwnership(t *testing.T) {
	evidence := &ClipEvidence{
		ClipDetails: map[string]ClipDetail{
			"a": {Name: "A", Transcript: "alpha"},
			"b": {Name: "B", Transcript: "bravo"},
		},
	}
	segments := []ScriptSegment{
		{ID: "empty", Topic: "Narration"},
		{ID: "multi", Kind: "scene", Topic: "Scene", ClipIDs: []string{"a", "b"}},
	}

	got := BuildSegmentClipEvidence(segments, evidence)
	if len(got) != 2 || len(got[0].ClipIDs) != 0 || len(got[1].ClipIDs) != 2 {
		t.Fatalf("segment evidence cardinality mismatch: %+v", got)
	}
	if got[1].Clips["a"].Transcript != "alpha" || got[1].Clips["b"].Transcript != "bravo" {
		t.Fatalf("segment evidence lost clip details: %+v", got[1].Clips)
	}
}

func TestNewClipEvidence_DeepCopiesSegmentClipTags(t *testing.T) {
	original := ClipEvidence{
		SegmentEvidence: []SegmentClipEvidence{{
			SegmentID: "scene-1",
			Clips: map[string]ClipDetail{
				"clip-a": {Tags: []string{"original"}},
			},
		}},
	}

	clone := NewClipEvidence(original)
	clonedDetail := clone.SegmentEvidence[0].Clips["clip-a"]
	clonedDetail.Tags[0] = "changed"
	clone.SegmentEvidence[0].Clips["clip-a"] = clonedDetail

	if original.SegmentEvidence[0].Clips["clip-a"].Tags[0] != "original" {
		t.Fatal("segment clip tags were not defensively copied")
	}
}

func TestGenerationEnvelopeValidate_AllowsAdvancedOwnershipMixedWithRoot(t *testing.T) {
	envelope := GenerationEnvelopeV2{
		Version: 2,
		Items: []GenerationItemV2{{
			ID:     "item-advanced",
			Source: SourceSpec{Type: SourceClips, ClipIDs: []string{"clip-a"}},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{{
				Topic:   "Scene",
				ClipIDs: []string{"clip-a"},
			}}},
		}},
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("advanced segment ownership should override legacy root IDs: %v", err)
	}
	got := CollectRequestedClipIDs(envelope.Items[0].Source, envelope.Items[0].ScriptParams.Segments)
	if len(got) != 1 || got[0] != "clip-a" {
		t.Fatalf("legacy root ID was resolved despite advanced ownership: %v", got)
	}
}

func TestGenerationEnvelopeValidate_AllowsSegmentOnlyClipSource(t *testing.T) {
	envelope := GenerationEnvelopeV2{
		Version: 2,
		Items: []GenerationItemV2{{
			ID:     "item-1",
			Source: SourceSpec{Type: SourceClips},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{
				{ID: "intro", Topic: "Opening", ClipIDs: []string{"clip-a", "clip-b"}},
				{ID: "scene", Topic: "Scene", ClipIDs: nil},
			}},
		}},
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("segment-only clip source rejected: %v", err)
	}
}

// TestGenerationEnvelopePayloadContract_PreservesEditorialSegments pins the
// wire contract that belongs to the caller: segment order, kind, source_text,
// and zero/one/many clip cardinality must survive JSON decoding unchanged.
func TestGenerationEnvelopePayloadContract_PreservesEditorialSegments(t *testing.T) {
	original := GenerationEnvelopeV2{
		Version: 2,
		Preset:  PresetCustom,
		Items: []GenerationItemV2{{
			ID:     "payload-contract",
			Source: SourceSpec{Type: SourceClips},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{
				{ID: "intro", Kind: "intro", Topic: "Opening", SourceText: "INTRO_SOURCE_SENTINEL"},
				{ID: "single", Kind: "scene", Topic: "One clip", SourceText: "ONE_SOURCE_SENTINEL", ClipIDs: []string{"clip-one"}},
				{ID: "many", Kind: "scene", Topic: "Many clips", SourceText: "MANY_SOURCE_SENTINEL", ClipIDs: []string{"clip-a", "clip-b", "clip-c"}},
			}},
		}},
	}

	if err := original.Validate(); err != nil {
		t.Fatalf("valid caller payload rejected: %v", err)
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	decoded, err := DecodeEnvelopeV2(raw)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	segments := decoded.Items[0].ScriptParams.Segments
	if len(segments) != 3 {
		t.Fatalf("segment count = %d, want 3", len(segments))
	}
	wantIDs := []string{"intro", "single", "many"}
	wantTexts := []string{"INTRO_SOURCE_SENTINEL", "ONE_SOURCE_SENTINEL", "MANY_SOURCE_SENTINEL"}
	wantClipCounts := []int{0, 1, 3}
	for i, segment := range segments {
		if segment.ID != wantIDs[i] {
			t.Errorf("segment[%d].id = %q, want %q", i, segment.ID, wantIDs[i])
		}
		if segment.SourceText != wantTexts[i] {
			t.Errorf("segment[%d].source_text = %q, want %q", i, segment.SourceText, wantTexts[i])
		}
		if len(segment.ClipIDs) != wantClipCounts[i] {
			t.Errorf("segment[%d] clip count = %d, want %d", i, len(segment.ClipIDs), wantClipCounts[i])
		}
	}
	if segments[0].Kind != "intro" {
		t.Fatalf("intro kind was not preserved: %q", segments[0].Kind)
	}
	if segments[2].ClipIDs[0] != "clip-a" || segments[2].ClipIDs[1] != "clip-b" || segments[2].ClipIDs[2] != "clip-c" {
		t.Fatalf("multi-clip order was not preserved: %v", segments[2].ClipIDs)
	}
}

// TestGenerationEnvelopePayloadContract_ResolvesOnlyDeclaredClips pins the
// no-automatic-search rule at the source boundary: the resolver input is the
// ordered union of intro and segment-owned IDs, with no inferred replacements.
func TestGenerationEnvelopePayloadContract_ResolvesOnlyDeclaredClips(t *testing.T) {
	source := SourceSpec{
		Type:         SourceClips,
		IntroClipIDs: []string{"intro-a"},
	}
	segments := []ScriptSegment{
		{ID: "intro", Topic: "Opening", ClipIDs: []string{"clip-a", "clip-b"}},
		{ID: "scene", Topic: "Scene", ClipIDs: []string{"clip-c"}},
	}

	got := CollectRequestedClipIDs(source, segments)
	want := []string{"intro-a", "clip-a", "clip-b", "clip-c"}
	if len(got) != len(want) {
		t.Fatalf("requested clip count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("requested clip[%d] = %q, want %q; got %v", i, got[i], want[i], got)
		}
	}
}

func TestGenerationEnvelopePayloadContract_AdvancedSegmentsIgnoreLegacyRootIDs(t *testing.T) {
	envelope := GenerationEnvelopeV2{
		Version: 2,
		Items: []GenerationItemV2{{
			ID:     "mixed-ownership",
			Source: SourceSpec{Type: SourceClips, ClipIDs: []string{"ignored-root-clip"}},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{{
				ID:      "scene-1",
				Topic:   "Scene",
				ClipIDs: []string{"segment-clip"},
			}}},
		}},
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("advanced segment ownership should override legacy root IDs: %v", err)
	}
	got := CollectRequestedClipIDs(envelope.Items[0].Source, envelope.Items[0].ScriptParams.Segments)
	if len(got) != 1 || got[0] != "segment-clip" {
		t.Fatalf("legacy root ID was resolved despite advanced ownership: %v", got)
	}
}

func TestGenerationEnvelopePayloadContract_RejectsDuplicateClipWithinOneSegment(t *testing.T) {
	envelope := GenerationEnvelopeV2{
		Version: 2,
		Items: []GenerationItemV2{{
			ID:     "duplicate-in-scene",
			Source: SourceSpec{Type: SourceClips},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{{
				ID:      "scene-1",
				Topic:   "Scene",
				ClipIDs: []string{"clip-a", "clip-a"},
			}}},
		}},
	}
	if err := envelope.Validate(); err == nil {
		t.Fatal("duplicate clip within one segment must be rejected")
	}
}

func TestGenerationEnvelopePayloadContract_AllowsExplicitClipReuseAcrossSegments(t *testing.T) {
	envelope := GenerationEnvelopeV2{
		Version: 2,
		Items: []GenerationItemV2{{
			ID:     "reuse-across-scenes",
			Source: SourceSpec{Type: SourceClips},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{
				{ID: "scene-1", Topic: "First use", ClipIDs: []string{"clip-a"}},
				{ID: "scene-2", Topic: "Second use", ClipIDs: []string{"clip-a"}},
			}},
		}},
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("the same explicitly declared clip may be reused across scenes: %v", err)
	}
}

func TestGenerationEnvelopePayloadContract_SourceTextStaysWithItsSegment(t *testing.T) {
	segments := []ScriptSegment{
		{ID: "scene-a", Topic: "A", SourceText: "SOURCE_A", ClipIDs: []string{"clip-a"}},
		{ID: "scene-b", Topic: "B", SourceText: "SOURCE_B", ClipIDs: nil},
	}
	got := CanonicalizeSegmentClipIDs(SourceSpec{Type: SourceClips}, segments)
	if got[0].SourceText != "SOURCE_A" || got[1].SourceText != "SOURCE_B" {
		t.Fatalf("source_text crossed segment boundaries: %+v", got)
	}
	if len(got[0].ClipIDs) != 1 || got[0].ClipIDs[0] != "clip-a" || len(got[1].ClipIDs) != 0 {
		t.Fatalf("segment clip ownership changed while preserving source_text: %+v", got)
	}
}
