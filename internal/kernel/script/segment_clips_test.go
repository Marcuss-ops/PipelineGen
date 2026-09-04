package script

import (
	"encoding/json"
	"testing"
)

func TestCanonicalizeSegmentClipIDs_ExplicitSegmentsWin(t *testing.T) {
	source := SourceSpec{
		Type:    SourceClips,
		ClipIDs: []string{"legacy-a", "legacy-b"},
	}
	segments := []ScriptSegment{
		{ID: "scene", Topic: "Scene", ClipIDs: []string{"clip-a", "clip-b"}},
		{ID: "narration", Topic: "Narration", ClipIDs: []string{}},
	}

	got := CanonicalizeSegmentClipIDs(source, segments)
	want := [][]string{{"clip-a", "clip-b"}, {}}
	assertSegmentClipIDs(t, got, want)
}

func TestCanonicalizeSegmentClipIDs_ExplicitEmptySegmentsWin(t *testing.T) {
	source := SourceSpec{Type: SourceClips, ClipIDs: []string{"legacy-a", "legacy-b"}}
	segments := []ScriptSegment{
		{ID: "empty", Topic: "Narration", ClipIDs: []string{}},
		{ID: "also-empty", Topic: "More", ClipIDs: []string{}},
	}

	got := CanonicalizeSegmentClipIDs(source, segments)
	if !HasExplicitSegmentClipIDs(segments) {
		t.Fatal("non-nil empty clip_ids must select advanced ownership")
	}
	assertSegmentClipIDs(t, got, [][]string{{}, {}})
}

func TestCanonicalizeSegmentClipIDs_ExplicitNilDoesNotReactivateLegacyDistribution(t *testing.T) {
	source := SourceSpec{Type: SourceClips, ClipIDs: []string{"legacy-a", "legacy-b"}}
	segments := []ScriptSegment{
		{ID: "explicit", Topic: "Explicit", ClipIDs: []string{"declared"}},
		{ID: "narration", Topic: "Narration", ClipIDs: nil},
	}

	got := CanonicalizeSegmentClipIDs(source, segments)
	assertSegmentClipIDs(t, got, [][]string{{"declared"}, {}})
	if got[1].ClipIDs != nil {
		t.Fatalf("nil segment reactivated legacy distribution: %v", got[1].ClipIDs)
	}
}

func TestCanonicalizeSegmentClipIDs_ExplicitSegmentsKeepOwnership(t *testing.T) {
	source := SourceSpec{Type: SourceClips, ClipIDs: []string{"ignored-root"}}
	segments := []ScriptSegment{
		{ID: "scene-1", Kind: "scene", Topic: "Main", ClipIDs: []string{"scene-clip"}},
		{ID: "opening", Kind: "intro", Topic: "Opening", ClipIDs: []string{"opening-clip"}},
	}

	got := CanonicalizeSegmentClipIDs(source, segments)
	assertSegmentClipIDs(t, got, [][]string{{"scene-clip"}, {"opening-clip"}})
}

func TestCanonicalizeSegmentClipIDs_LegacyRootCompatibility(t *testing.T) {
	source := SourceSpec{Type: SourceClips, ClipIDs: []string{"a", "b", "c"}}
	segments := []ScriptSegment{{ID: "one", Topic: "One"}, {ID: "two", Topic: "Two"}}

	got := CanonicalizeSegmentClipIDs(source, segments)
	assertSegmentClipIDs(t, got, [][]string{{"a"}, {"b", "c"}})
	if source.ClipIDs[0] != "a" {
		t.Fatal("canonicalization mutated the caller source")
	}
}

func TestBuildSegmentClipEvidencePreservesZeroToManyOwnership(t *testing.T) {
	evidence := &ClipEvidence{ClipDetails: map[string]ClipDetail{
		"a": {Name: "A", Transcript: "alpha"},
		"b": {Name: "B", Transcript: "bravo"},
	}}
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
	original := ClipEvidence{SegmentEvidence: []SegmentClipEvidence{{
		SegmentID: "scene-1",
		Clips:     map[string]ClipDetail{"clip-a": {Tags: []string{"original"}}},
	}}}
	clone := NewClipEvidence(original)
	clonedDetail := clone.SegmentEvidence[0].Clips["clip-a"]
	clonedDetail.Tags[0] = "changed"
	clone.SegmentEvidence[0].Clips["clip-a"] = clonedDetail
	if original.SegmentEvidence[0].Clips["clip-a"].Tags[0] != "original" {
		t.Fatal("segment clip tags were not defensively copied")
	}
}

func TestNewClipEvidence_DeepCopyPreservesEmptySlicesTagsAndEditorialOwnership(t *testing.T) {
	original := ClipEvidence{
		AcceptedClipIDs:      []string{"clip-a"},
		RenderableClipIDs:    make([]string, 0, 1),
		Excluded:             make([]ExcludedClip, 0, 1),
		MissingClipIDs:       make([]MissingClipID, 0, 1),
		ClipTranscriptHashes: make([]string, 0, 1),
		ClipDetails: map[string]ClipDetail{
			"clip-a": {Name: "Clip A", Tags: []string{"top-level"}},
		},
		SegmentEvidence: []SegmentClipEvidence{
			{
				SegmentID:  "segment-one",
				Kind:       "scene",
				Topic:      "First topic",
				SourceText: "First source",
				ClipIDs:    []string{"clip-a"},
				Clips: map[string]ClipDetail{
					"clip-a": {Name: "Clip A", Tags: []string{"segment-tag"}},
				},
			},
			{
				SegmentID:  "segment-two",
				Kind:       "narration",
				Topic:      "Second topic",
				SourceText: "Second source",
				ClipIDs:    make([]string, 0, 1),
				Clips:      map[string]ClipDetail{},
			},
		},
	}

	clone := NewClipEvidence(original)
	if clone == nil {
		t.Fatal("NewClipEvidence returned nil")
	}
	for name, got := range map[string][]string{
		"RenderableClipIDs":    clone.RenderableClipIDs,
		"ClipTranscriptHashes": clone.ClipTranscriptHashes,
	} {
		if got == nil || len(got) != 0 {
			t.Errorf("%s = %#v, want a preserved non-nil empty slice", name, got)
		}
	}
	if clone.Excluded == nil || len(clone.Excluded) != 0 {
		t.Errorf("Excluded = %#v, want a preserved non-nil empty slice", clone.Excluded)
	}
	if clone.MissingClipIDs == nil || len(clone.MissingClipIDs) != 0 {
		t.Errorf("MissingClipIDs = %#v, want a preserved non-nil empty slice", clone.MissingClipIDs)
	}

	if len(clone.SegmentEvidence) != 2 {
		t.Fatalf("SegmentEvidence length = %d, want 2", len(clone.SegmentEvidence))
	}
	first, second := clone.SegmentEvidence[0], clone.SegmentEvidence[1]
	if first.SegmentID != "segment-one" || first.Kind != "scene" || first.Topic != "First topic" || first.SourceText != "First source" {
		t.Fatalf("first segment editorial ownership changed: %+v", first)
	}
	if second.SegmentID != "segment-two" || second.Kind != "narration" || second.Topic != "Second topic" || second.SourceText != "Second source" {
		t.Fatalf("second segment editorial ownership changed: %+v", second)
	}
	if second.ClipIDs == nil || len(second.ClipIDs) != 0 {
		t.Errorf("second segment ClipIDs = %#v, want a preserved non-nil empty slice", second.ClipIDs)
	}
	if second.Clips == nil || len(second.Clips) != 0 {
		t.Errorf("second segment Clips = %#v, want a preserved non-nil empty map", second.Clips)
	}

	clone.AcceptedClipIDs[0] = "mutated-accepted"
	clone.ClipDetails["clip-a"] = ClipDetail{Name: "mutated top-level", Tags: []string{"mutated"}}
	clone.SegmentEvidence[0].ClipIDs[0] = "mutated-segment"
	segmentDetail := clone.SegmentEvidence[0].Clips["clip-a"]
	segmentDetail.Tags[0] = "mutated-segment-tag"
	clone.SegmentEvidence[0].Clips["clip-a"] = segmentDetail
	clone.RenderableClipIDs = append(clone.RenderableClipIDs, "clone-renderable")
	clone.ClipTranscriptHashes = append(clone.ClipTranscriptHashes, "clone-hash")
	clone.Excluded = append(clone.Excluded, ExcludedClip{ClipID: "clone-excluded"})
	clone.MissingClipIDs = append(clone.MissingClipIDs, MissingClipID{ClipID: "clone-missing"})
	clone.SegmentEvidence[1].ClipIDs = append(clone.SegmentEvidence[1].ClipIDs, "new-clip")
	clone.SegmentEvidence[1].Clips["new-clip"] = ClipDetail{Name: "new clip"}

	if original.AcceptedClipIDs[0] != "clip-a" {
		t.Fatal("AcceptedClipIDs shares storage with the clone")
	}
	if original.ClipDetails["clip-a"].Name != "Clip A" || original.ClipDetails["clip-a"].Tags[0] != "top-level" {
		t.Fatal("top-level ClipDetails or tags share storage with the clone")
	}
	if original.SegmentEvidence[0].ClipIDs[0] != "clip-a" {
		t.Fatal("segment ClipIDs share storage with the clone")
	}
	if original.SegmentEvidence[0].Clips["clip-a"].Tags[0] != "segment-tag" {
		t.Fatal("segment ClipDetails tags share storage with the clone")
	}
	if len(original.RenderableClipIDs) != 0 || original.RenderableClipIDs[:1][0] == "clone-renderable" {
		t.Fatal("empty RenderableClipIDs shares backing storage with the clone")
	}
	if len(original.ClipTranscriptHashes) != 0 || original.ClipTranscriptHashes[:1][0] == "clone-hash" {
		t.Fatal("empty ClipTranscriptHashes shares backing storage with the clone")
	}
	if len(original.Excluded) != 0 || original.Excluded[:1][0].ClipID == "clone-excluded" {
		t.Fatal("empty Excluded shares backing storage with the clone")
	}
	if len(original.MissingClipIDs) != 0 || original.MissingClipIDs[:1][0].ClipID == "clone-missing" {
		t.Fatal("empty MissingClipIDs shares backing storage with the clone")
	}
	if len(original.SegmentEvidence[1].ClipIDs) != 0 || original.SegmentEvidence[1].ClipIDs[:1][0] == "new-clip" || len(original.SegmentEvidence[1].Clips) != 0 {
		t.Fatal("empty second segment ownership changed through the clone")
	}
}

func TestNewClipEvidence_DeepCopyPreservesNilSlicesAndMaps(t *testing.T) {
	clone := NewClipEvidence(ClipEvidence{
		AcceptedClipIDs:   nil,
		RenderableClipIDs: nil,
		ClipDetails:       nil,
		SegmentEvidence:   nil,
	})
	if clone.AcceptedClipIDs != nil || clone.RenderableClipIDs != nil || clone.ClipDetails != nil || clone.SegmentEvidence != nil {
		t.Fatalf("nil collection identity was not preserved: %+v", clone)
	}
}

func TestGenerationEnvelopeValidate_AllowsAdvancedOwnershipMixedWithRoot(t *testing.T) {
	envelope := GenerationEnvelopeV2{
		Version: 2,
		Items: []GenerationItemV2{{
			ID: "advanced", Source: SourceSpec{Type: SourceClips, ClipIDs: []string{"legacy-root"}},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{{Topic: "Scene", ClipIDs: []string{"clip-a"}}}},
		}},
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("explicit segments must override legacy root clip_ids: %v", err)
	}
	got := CollectRequestedClipIDs(envelope.Items[0].Source, envelope.Items[0].ScriptParams.Segments)
	if len(got) != 1 || got[0] != "clip-a" {
		t.Fatalf("legacy root clip was resolved in authoritative mode: %v", got)
	}
}

func TestGenerationEnvelopeValidate_AllowsSegmentOnlyClipSource(t *testing.T) {
	envelope := GenerationEnvelopeV2{
		Version: 2,
		Items: []GenerationItemV2{{
			ID: "segment-only", Source: SourceSpec{Type: SourceClips},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{{Topic: "Scene", ClipIDs: []string{"clip-a"}}}},
		}},
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("segment-only clip source rejected: %v", err)
	}
}

func TestGenerationEnvelopePayloadContract_PreservesEditorialSegments(t *testing.T) {
	original := GenerationEnvelopeV2{
		Version: 2, Preset: PresetCustom,
		Items: []GenerationItemV2{{
			ID: "payload-contract", Source: SourceSpec{Type: SourceClips},
			ScriptParams: ScriptSpec{Segments: []ScriptSegment{
				{ID: "intro", Kind: "intro", Topic: "Opening", SourceText: "INTRO"},
				{ID: "single", Topic: "One", SourceText: "ONE", ClipIDs: []string{"clip-one"}},
				{ID: "many", Topic: "Many", SourceText: "MANY", ClipIDs: []string{"clip-a", "clip-b", "clip-c"}},
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
	if len(segments) != 3 || segments[0].SourceText != "INTRO" || segments[1].SourceText != "ONE" || len(segments[2].ClipIDs) != 3 {
		t.Fatalf("editorial segment contract changed across JSON: %+v", segments)
	}
}

func TestGenerationEnvelopePayloadContract_ResolvesOnlyDeclaredClips(t *testing.T) {
	source := SourceSpec{Type: SourceClips}
	segments := []ScriptSegment{{ClipIDs: []string{"clip-a", "clip-b"}}, {ClipIDs: []string{"clip-c"}}}
	got := CollectRequestedClipIDs(source, segments)
	want := []string{"clip-a", "clip-b", "clip-c"}
	if len(got) != len(want) {
		t.Fatalf("requested IDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("requested[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGenerationEnvelopePayloadContract_RejectsDuplicateClipWithinOneSegment(t *testing.T) {
	envelope := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
		ID: "duplicate", Source: SourceSpec{Type: SourceClips},
		ScriptParams: ScriptSpec{Segments: []ScriptSegment{{Topic: "Scene", ClipIDs: []string{"clip-a", "clip-a"}}}},
	}}}
	if err := envelope.Validate(); err == nil {
		t.Fatal("duplicate clip within one segment must be rejected")
	}
}

func TestGenerationEnvelopePayloadContract_AllowsExplicitClipReuseAcrossSegments(t *testing.T) {
	envelope := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
		ID: "reuse", Source: SourceSpec{Type: SourceClips},
		ScriptParams: ScriptSpec{Segments: []ScriptSegment{{Topic: "First", ClipIDs: []string{"clip-a"}}, {Topic: "Second", ClipIDs: []string{"clip-a"}}}},
	}}}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("explicit cross-segment reuse should be allowed: %v", err)
	}
}

func TestGenerationEnvelopeValidate_RejectsAllEmptyExplicitSegmentsWithoutIntro(t *testing.T) {
	envelope := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
		ID: "all-empty", Source: SourceSpec{Type: SourceClips, ClipIDs: []string{"ignored-root"}},
		ScriptParams: ScriptSpec{Segments: []ScriptSegment{{Topic: "One", ClipIDs: []string{}}, {Topic: "Two", ClipIDs: []string{}}}},
	}}}
	if err := envelope.Validate(); err == nil {
		t.Fatal("all-empty explicit segments must not fall back to root clips")
	}
}

func TestGenerationEnvelopeValidate_RejectsDuplicateLegacyIntroClipIDs(t *testing.T) {
	envelope := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
		ID: "duplicate-intro", Source: SourceSpec{Type: SourceClips, IntroClipIDs: []string{"intro", "intro"}},
	}}}
	if err := envelope.Validate(); err == nil {
		t.Fatal("duplicate intro_clip_ids must be rejected")
	}
}

func TestGenerationEnvelopeValidate_RejectsDuplicateAcrossLegacyFields(t *testing.T) {
	envelope := GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{{
		ID: "duplicate-legacy", Source: SourceSpec{Type: SourceClips, IntroClipIDs: []string{"shared"}, ClipIDs: []string{"shared"}},
	}}}
	if err := envelope.Validate(); err == nil {
		t.Fatal("duplicate IDs across legacy fields must be rejected")
	}
}

func assertSegmentClipIDs(t *testing.T, got []ScriptSegment, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("segment count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i].ClipIDs) != len(want[i]) {
			t.Fatalf("segment[%d] clips = %v, want %v", i, got[i].ClipIDs, want[i])
		}
		for j := range want[i] {
			if got[i].ClipIDs[j] != want[i][j] {
				t.Fatalf("segment[%d].clip_ids[%d] = %q, want %q", i, j, got[i].ClipIDs[j], want[i][j])
			}
		}
	}
}
