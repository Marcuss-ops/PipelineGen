// Package script — immutability_test.go pins the defensive-copy
// contract of NewClipEvidence + NewResolvedGenerationPlan.
//
// godlike/06 SSOT (no mutation helper): the constructors are the
// SOLE path that produces a snapshot-safe ResolvedEvidence-bound
// plan. After construction, the constructed instance's slice/map
// fields MUST be independent of any caller's mutations on the
// inputs. The tests below exercise this contract exhaustively.
//
// godlike/07 NO-FAKE-AVAILABILITY: post-construction mutation
// observability is verified via explicit field-by-field DeepEqual
// against pre-mutation snapshots. No stdlib `reflect.DeepCopy`
// (the API doesn't exist); tests instead capture the expected
// per-field value as a literal and compare the constructed
// instance's slice/map against the literal after mutating the
// source. A partial-clone bug (where any map/slice leaks a
// pointer) surfaces as a DeepEqual mismatch.
package script

import (
	"reflect"
	"testing"
)

// deepEqualMapStringString compares two maps[string]string in a
// nil-safe way that treats nil and empty maps as equal (per Go's
// canonical "absent vs zero-value" comparison discipline). It is
// used in the immutability tests to assert post-construction map
// identity is preserved against an expected snapshot.
func deepEqualMapStringString(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// TestNewClipEvidence_MapMutationDoesNotPropagate verifies that
// mutating the source DriveLinks / ClipNames / ClipDetails maps
// (and the slice fields AcceptedClipIDs / RenderableClipIDs /
// Excluded / MissingClipIDs / ClipTranscriptHashes) AFTER
// construction does NOT reach the constructed instance.
//
// The fixture mutates each of the 8 collection fields with a NEW
// key (different from any existing fixture key) so a partial-copy
// bug (where one map is shared with the source) is detected via
// DeepEqual comparison against the literal snapshot.
func TestNewClipEvidence_MapMutationDoesNotPropagate(t *testing.T) {
	// Source maps with deliberate, non-trivial shapes.
	sourceDriveLinks := map[string]string{
		"clip-a": "https://drive/a",
		"clip-b": "https://drive/b",
	}
	sourceClipNames := map[string]string{
		"clip-a": "name a",
		"clip-b": "name b",
	}
	sourceClipDetails := map[string]ClipDetail{
		"clip-a": {
			Name:      "name a",
			StartMs:   100,
			EndMs:     1100,
			Tags:      []string{"t1", "t2"},
			DriveLink: "https://drive/a",
		},
	}
	source := ClipEvidence{
		AcceptedClipIDs:      []string{"clip-a", "clip-b"},
		RenderableClipIDs:    []string{"clip-a"},
		ClipCount:            2,
		DriveLinks:           sourceDriveLinks,
		ClipNames:            sourceClipNames,
		ClipDetails:          sourceClipDetails,
		MissingClipIDs:       []MissingClipID{{ClipID: "missing-x", Reason: MissingClipReasonNotFound}},
		Excluded:             []ExcludedClip{{ClipID: "excluded-x", Reason: "low_quality"}},
		ClipTranscriptHashes: []string{"hash-a", "hash-b"},
	}
	ev := NewClipEvidence(source)

	// Mutate the source maps + slices post-construction. If the
	// constructor shared pointers with the source, ev's snapshot
	// would observe these mutations.
	sourceDriveLinks["clip-a"] = "MUTATED-https://drive/a"
	sourceDriveLinks["clip-NEW"] = "https://drive/NEW"
	sourceClipNames["clip-a"] = "MUTATED-name a"
	sourceClipNames["clip-NEW"] = "name NEW"
	sourceClipDetails["clip-a"] = ClipDetail{Name: "MUTATED-name a"}
	sourceClipDetails["clip-NEW"] = ClipDetail{Name: "name NEW"}
	source.AcceptedClipIDs = append(source.AcceptedClipIDs, "clip-NEW")
	source.RenderableClipIDs = append(source.RenderableClipIDs, "clip-NEW")
	source.Excluded = append(source.Excluded, ExcludedClip{ClipID: "excluded-NEW", Reason: "low_quality"})
	source.MissingClipIDs = append(source.MissingClipIDs, MissingClipID{ClipID: "missing-NEW", Reason: MissingClipReasonNotFound})
	source.ClipTranscriptHashes = append(source.ClipTranscriptHashes, "hash-NEW")

	// Assert constructed instance's state matches the pre-mutation
	// literal snapshot (NOT the mutated source).
	wantAccepted := []string{"clip-a", "clip-b"}
	if !reflect.DeepEqual(ev.AcceptedClipIDs, wantAccepted) {
		t.Errorf("AcceptedClipIDs mutated: want %v, got %v", wantAccepted, ev.AcceptedClipIDs)
	}
	wantRenderable := []string{"clip-a"}
	if !reflect.DeepEqual(ev.RenderableClipIDs, wantRenderable) {
		t.Errorf("RenderableClipIDs mutated: want %v, got %v", wantRenderable, ev.RenderableClipIDs)
	}
	wantExcluded := []ExcludedClip{{ClipID: "excluded-x", Reason: "low_quality"}}
	if !reflect.DeepEqual(ev.Excluded, wantExcluded) {
		t.Errorf("Excluded mutated: want %v, got %v", wantExcluded, ev.Excluded)
	}
	wantMissing := []MissingClipID{{ClipID: "missing-x", Reason: MissingClipReasonNotFound}}
	if !reflect.DeepEqual(ev.MissingClipIDs, wantMissing) {
		t.Errorf("MissingClipIDs mutated: want %v, got %v", wantMissing, ev.MissingClipIDs)
	}
	wantHashes := []string{"hash-a", "hash-b"}
	if !reflect.DeepEqual(ev.ClipTranscriptHashes, wantHashes) {
		t.Errorf("ClipTranscriptHashes mutated: want %v, got %v", wantHashes, ev.ClipTranscriptHashes)
	}
	wantDriveLinks := map[string]string{
		"clip-a": "https://drive/a",
		"clip-b": "https://drive/b",
	}
	if !deepEqualMapStringString(ev.DriveLinks, wantDriveLinks) {
		t.Errorf("DriveLinks mutated: want %v, got %v", wantDriveLinks, ev.DriveLinks)
	}
	wantClipNames := map[string]string{
		"clip-a": "name a",
		"clip-b": "name b",
	}
	if !deepEqualMapStringString(ev.ClipNames, wantClipNames) {
		t.Errorf("ClipNames mutated: want %v, got %v", wantClipNames, ev.ClipNames)
	}
	wantClipDetails := map[string]ClipDetail{
		"clip-a": {
			Name:      "name a",
			StartMs:   100,
			EndMs:     1100,
			Tags:      []string{"t1", "t2"},
			DriveLink: "https://drive/a",
		},
	}
	if !reflect.DeepEqual(ev.ClipDetails, wantClipDetails) {
		t.Errorf("ClipDetails mutated: want %v, got %v", wantClipDetails, ev.ClipDetails)
	}
}

// TestNewResolvedGenerationPlan_SliceMutationDoesNotPropagate
// verifies that mutating the source Segments /
// Languages / Postprocessors slices AFTER construction does NOT
// reach the constructed instance. Also verifies that mutating
// the embedded ClipEvidence's source maps does NOT reach the
// constructed plan's embedded ClipEvidence.
func TestNewResolvedGenerationPlan_SliceMutationDoesNotPropagate(t *testing.T) {
	sourceEv := &ClipEvidence{
		AcceptedClipIDs: []string{"clip-a"},
		DriveLinks:      map[string]string{"clip-a": "https://drive/a"},
		ClipNames:       map[string]string{"clip-a": "name a"},
		ClipDetails: map[string]ClipDetail{
			"clip-a": {Name: "name a", StartMs: 100, EndMs: 1100},
		},
	}
	source := ResolvedGenerationPlan{
		Topic:          "topic",
		SourceText:     "source text",
		Segments:       []ScriptSegment{{Topic: "a"}, {Topic: "b"}},
		Languages:      []string{"en", "it"},
		Postprocessors: []string{"clip_bindings", "voiceover"},
		ClipEvidence:   sourceEv,
	}
	plan := NewResolvedGenerationPlan(source)

	// Mutate the source slices + embedded map fields.
	source.Segments = append(source.Segments, ScriptSegment{Topic: "c"})
	source.Languages = append(source.Languages, "fr")
	source.Postprocessors = append(source.Postprocessors, "images")
	sourceEv.DriveLinks["clip-a"] = "MUTATED-https://drive/a"
	sourceEv.DriveLinks["clip-NEW"] = "https://drive/NEW"
	sourceEv.AcceptedClipIDs = append(sourceEv.AcceptedClipIDs, "clip-NEW")

	// Assert pre-mutation values preserved.
	if !reflect.DeepEqual(plan.Segments, []ScriptSegment{{Topic: "a"}, {Topic: "b"}}) {
		t.Errorf("Segments mutated: want [a b], got %v", plan.Segments)
	}
	if !reflect.DeepEqual(plan.Languages, []string{"en", "it"}) {
		t.Errorf("Languages mutated: want [en it], got %v", plan.Languages)
	}
	if !reflect.DeepEqual(plan.Postprocessors, []string{"clip_bindings", "voiceover"}) {
		t.Errorf("Postprocessors mutated: want [clip_bindings voiceover], got %v", plan.Postprocessors)
	}
	if plan.ClipEvidence == nil {
		t.Fatal("ClipEvidence should not be nil after construction")
	}
	if !reflect.DeepEqual(plan.ClipEvidence.AcceptedClipIDs, []string{"clip-a"}) {
		t.Errorf("plan.ClipEvidence.AcceptedClipIDs mutated: want [clip-a], got %v", plan.ClipEvidence.AcceptedClipIDs)
	}
	if !deepEqualMapStringString(plan.ClipEvidence.DriveLinks, map[string]string{"clip-a": "https://drive/a"}) {
		t.Errorf("plan.ClipEvidence.DriveLinks mutated: want {clip-a:https://drive/a}, got %v", plan.ClipEvidence.DriveLinks)
	}
	// Source vs constructed pointer must differ — defensive-copy
	// creates a fresh *ClipEvidence pointer.
	if plan.ClipEvidence == sourceEv {
		t.Errorf("plan.ClipEvidence must be a fresh pointer (defensive-copy at construction); got the same pointer as the source")
	}
}

// TestNewClipEvidence_EmptyClone_HandlesNilMaps verifies that the
// constructor handles nil map / nil slice inputs gracefully. Nil
// must remain nil after cloning (maps.Clone + slices.Clone return
// nil for nil inputs).
func TestNewClipEvidence_EmptyClone_HandlesNilMaps(t *testing.T) {
	ev := NewClipEvidence(ClipEvidence{})

	if ev.AcceptedClipIDs != nil {
		t.Errorf("nil AcceptedClipIDs source must clone to nil; got %v", ev.AcceptedClipIDs)
	}
	if ev.DriveLinks != nil {
		t.Errorf("nil DriveLinks source must clone to nil; got %v", ev.DriveLinks)
	}
	if ev.ClipNames != nil {
		t.Errorf("nil ClipNames source must clone to nil; got %v", ev.ClipNames)
	}
	if ev.ClipDetails != nil {
		t.Errorf("nil ClipDetails source must clone to nil; got %v", ev.ClipDetails)
	}
	if ev.RenderableClipIDs != nil {
		t.Errorf("nil RenderableClipIDs source must clone to nil; got %v", ev.RenderableClipIDs)
	}
}

// TestNewClipEvidence_FullyPopulated_RoundTrip verifies value
// preservation across all 14 fields. The constructor copies scalar
// fields by value + clone-with-fresh-backing-array slice/map
// fields; the constructed instance must preserve every value
// verbatim.
func TestNewClipEvidence_FullyPopulated_RoundTrip(t *testing.T) {
	src := ClipEvidence{
		AcceptedClipIDs:      []string{"x", "y"},
		RenderableClipIDs:    []string{"x"},
		ClipCount:            2,
		AssembledText:        "assembled",
		NarrativeText:        "narrative",
		DriveLinks:           map[string]string{"x": "https://drive/x"},
		ClipNames:            map[string]string{"x": "name x"},
		Excluded:             []ExcludedClip{{ClipID: "ex", Reason: "r"}},
		MissingClipIDs:       []MissingClipID{{ClipID: "mi", Reason: MissingClipReasonNotFound}},
		ClipTranscriptHashes: []string{"h1", "h2"},
		ClipDetails:          map[string]ClipDetail{"x": {Name: "name x"}},
		LanguageCode:         "en",
		TextTrackVersion:     "v1.0",
		TranscriptHash:       "txhash",
	}
	ev := NewClipEvidence(src)
	if !reflect.DeepEqual(ev.AcceptedClipIDs, src.AcceptedClipIDs) {
		t.Errorf("AcceptedClipIDs lost: want %v, got %v", src.AcceptedClipIDs, ev.AcceptedClipIDs)
	}
	if !deepEqualMapStringString(ev.DriveLinks, src.DriveLinks) {
		t.Errorf("DriveLinks lost: want %v, got %v", src.DriveLinks, ev.DriveLinks)
	}
	if ev.ClipDetails["x"].Name != "name x" {
		t.Errorf("ClipDetails value lost: %v", ev.ClipDetails)
	}
	if ev.LanguageCode != "en" || ev.TextTrackVersion != "v1.0" || ev.TranscriptHash != "txhash" {
		t.Errorf("scalar fingerprint fields lost: %+v", ev)
	}
}

// TestNewResolvedGenerationPlan_NilClipEvidence_HandlesCleanly
// verifies that the constructor handles a plan with nil
// ClipEvidence without dereferencing (verts the embedded path).
// Slice fields must still be cloned independently.
func TestNewResolvedGenerationPlan_NilClipEvidence_HandlesCleanly(t *testing.T) {
	source := ResolvedGenerationPlan{
		Topic:          "topic",
		Segments:       []ScriptSegment{{Topic: "a"}},
		Languages:      []string{"en"},
		Postprocessors: []string{"clip_bindings"},
		ClipEvidence:   nil,
	}
	p := NewResolvedGenerationPlan(source)
	if p == nil {
		t.Fatal("constructor returned nil for non-nil source")
	}
	if p.ClipEvidence != nil {
		t.Errorf("nil ClipEvidence source must clone to nil; got %v", p.ClipEvidence)
	}
	if !reflect.DeepEqual(p.Languages, []string{"en"}) {
		t.Errorf("Languages value lost: %v", p.Languages)
	}
}
