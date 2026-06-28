// Package scripts — clip_evidence_builder_test.go covers the
// PR 5 (June 2026) split between resolved clip IDs and missing
// clip IDs inside BuildClipEvidence.
//
// PR 5 contract:
//   - ClipEvidence.ClipIDs is the RESOLVED-only slice (looked up
//     successfully AND has a non-empty DriveLink).
//   - ClipEvidence.MissingClipIDs carries the request-but-dropped
//     IDs with a structured reason: "not_found" (neither DB lookup
//     hit) or "drivenotfound" (DB hit but no DriveLink).
//   - ClipEvidence.ClipCount always equals len(ClipIDs).
package scripts

import (
	"reflect"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestBuildClipEvidence_NilPack_ReturnsNil pins the no-input
// contract: a nil pack returns nil. nil is not "empty evidence
// with no missing IDs"; it is "no resolution happened, skip the
// whole fidelity work".
func TestBuildClipEvidence_NilPack_ReturnsNil(t *testing.T) {
	if ev := usecase.BuildClipEvidence(nil, ""); ev != nil {
		t.Fatalf("usecase.BuildClipEvidence(nil) = %+v, want nil", ev)
	}
}

// TestBuildClipEvidence_EmptyPack_ReturnsNil pins the empty-pack
// contract: a pack with no resolved IDs AND no missing IDs returns
// nil so a downstream caller doesn't accidentally persist an empty
// evidence record.
func TestBuildClipEvidence_EmptyPack_ReturnsNil(t *testing.T) {
	ev := usecase.BuildClipEvidence(map[string]any{}, "")
	if ev != nil {
		t.Fatalf("usecase.BuildClipEvidence(empty) = %+v, want nil", ev)
	}
}

// TestBuildClipEvidence_AllResolved_NoMissing pins the happy
// path: resolved clip IDs only, no missing IDs. MissingClipIDs
// must stay nil (omitempty-friendly) so the JSON shape doesn't
// include a phony "missing_clip_ids":[] field.
func TestBuildClipEvidence_AllResolved_NoMissing(t *testing.T) {
	pack := map[string]any{
		"clip_ids":   []string{"clip-a", "clip-b"},
		"clip_names": []string{"Clip A", "Clip B"},
		"clip_drive_links": map[string]string{
			"clip-a": "https://drive.google.com/a",
			"clip-b": "https://drive.google.com/b",
		},
	}
	ev := usecase.BuildClipEvidence(pack, "CLIP clip-a: Clip A\n")
	if ev == nil {
		t.Fatal("ev = nil, want non-nil (resolved IDs present)")
	}
	if !reflect.DeepEqual(ev.ClipIDs, []string{"clip-a", "clip-b"}) {
		t.Fatalf("ClipIDs = %v, want [clip-a clip-b]", ev.ClipIDs)
	}
	if ev.ClipCount != 2 {
		t.Fatalf("ClipCount = %d, want 2 (PR 5 contract: equals len(ClipIDs))", ev.ClipCount)
	}
	if len(ev.MissingClipIDs) != 0 {
		t.Fatalf("MissingClipIDs = %v, want nil/empty "+
			"(missing-collection must be nil when all resolved)",
			ev.MissingClipIDs)
	}
	if ev.AssembledText != "CLIP clip-a: Clip A" {
		t.Fatalf("AssembledText = %q, want trimmed source text", ev.AssembledText)
	}
}

// TestBuildClipEvidence_MixedResolvedAndMissing is the central
// PR 5 scenario: a pack carrying BOTH resolved IDs and missing
// IDs (with reasons). ClipIDs / ClipCount reflect the RESOLVED
// half; MissingClipIDs captures the missing half with reasons
// preserved verbatim.
func TestBuildClipEvidence_MixedResolvedAndMissing(t *testing.T) {
	pack := map[string]any{
		"clip_ids":   []string{"clip-a", "clip-c"}, // resolved
		"clip_names": []string{"Clip A", "Clip C"},
		"clip_drive_links": map[string]string{
			"clip-a": "https://drive.google.com/a",
			"clip-c": "https://drive.google.com/c",
		},
		"missing_clip_ids": []scriptpkg.MissingClipID{
			{ClipID: "missing-1", Reason: scriptpkg.MissingClipReasonNotFound},
			{ClipID: "missing-2", Reason: scriptpkg.MissingClipReasonDriveNotFound},
		},
	}
	ev := usecase.BuildClipEvidence(pack, "")
	if ev == nil {
		t.Fatal("ev = nil, want non-nil")
	}
	want := []string{"clip-a", "clip-c"}
	if !reflect.DeepEqual(ev.ClipIDs, want) {
		t.Fatalf("ClipIDs = %v, want %v "+
			"(PR 5: only resolved IDs reach ClipIDs)", ev.ClipIDs, want)
	}
	if ev.ClipCount != 2 {
		t.Fatalf("ClipCount = %d, want 2", ev.ClipCount)
	}
	wantMissing := []scriptpkg.MissingClipID{
		{ClipID: "missing-1", Reason: "not_found"},
		{ClipID: "missing-2", Reason: "drivenotfound"},
	}
	if !reflect.DeepEqual(ev.MissingClipIDs, wantMissing) {
		t.Fatalf("MissingClipIDs = %v, want %v "+
			"(reasons must be preserved verbatim)", ev.MissingClipIDs, wantMissing)
	}
}

// TestBuildClipEvidence_AllMissing_ReturnsMissingOnly pins a
// subtle case: every requested ID failed to resolve. The
// resolved-only `clip_ids` slice is empty, but BuildClipEvidence
// must NOT return nil — the missing list carries operator-facing
// value (typo / orphan rate) and must reach the dashboard. The
// returned evidence carries MissingClipIDs but no ClipIDs.
func TestBuildClipEvidence_AllMissing_ReturnsMissingOnly(t *testing.T) {
	pack := map[string]any{
		"clip_ids": nil, // resolved-only slice is empty
		"missing_clip_ids": []scriptpkg.MissingClipID{
			{ClipID: "missing-1", Reason: scriptpkg.MissingClipReasonNotFound},
			{ClipID: "missing-2", Reason: scriptpkg.MissingClipReasonDriveNotFound},
		},
	}
	ev := usecase.BuildClipEvidence(pack, "  some source text  ")
	if ev == nil {
		t.Fatal("ev = nil, want non-nil " +
			"(all-missing path must keep MissingClipIDs visible)")
	}
	if len(ev.ClipIDs) != 0 {
		t.Fatalf("ClipIDs = %v, want empty (no resolved IDs)", ev.ClipIDs)
	}
	if ev.ClipCount != 0 {
		t.Fatalf("ClipCount = %d, want 0", ev.ClipCount)
	}
	wantMissing := []scriptpkg.MissingClipID{
		{ClipID: "missing-1", Reason: "not_found"},
		{ClipID: "missing-2", Reason: "drivenotfound"},
	}
	if !reflect.DeepEqual(ev.MissingClipIDs, wantMissing) {
		t.Fatalf("MissingClipIDs = %v, want %v", ev.MissingClipIDs, wantMissing)
	}
	// AssembledText is still trimmed if sourceText is provided.
	if ev.AssembledText != "some source text" {
		t.Fatalf("AssembledText = %q, want %q",
			ev.AssembledText, "some source text")
	}
}

// TestBuildClipEvidence_DualShapeExtraction pins that the
// JSON-replayed pack shape ([]map[string]any) and the runtime
// typed shape ([]scriptpkg.MissingClipID) both produce identical
// typed evidence. The dual-shape tolerance matters for fixtures
// that JSON-decode the pack and replay it; we want the evidence
// schema stable across both shapes.
func TestBuildClipEvidence_DualShapeExtraction(t *testing.T) {
	tests := []struct {
		name string
		raw  interface{}
	}{
		{
			name: "typed_slice",
			raw: []scriptpkg.MissingClipID{
				{ClipID: "a", Reason: scriptpkg.MissingClipReasonNotFound},
				{ClipID: "b", Reason: scriptpkg.MissingClipReasonDriveNotFound},
			},
		},
		{
			name: "map_string_any_slice",
			raw: []map[string]any{
				{"clip_id": "a", "reason": "not_found"},
				{"clip_id": "b", "reason": "drivenotfound"},
			},
		},
		{
			name: "map_string_string_slice",
			raw: []map[string]string{
				{"clip_id": "a", "reason": "not_found"},
				{"clip_id": "b", "reason": "drivenotfound"},
			},
		},
		{
			name: "interface_slice",
			raw: []interface{}{
				map[string]interface{}{"clip_id": "a", "reason": "not_found"},
				map[string]interface{}{"clip_id": "b", "reason": "drivenotfound"},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			pack := map[string]any{
				"clip_ids":         []string{"c", "d"}, // resolved-only
				"missing_clip_ids": tc.raw,
			}
			ev := usecase.BuildClipEvidence(pack, "")
			if ev == nil {
				t.Fatal("ev = nil, want non-nil")
			}
			if !reflect.DeepEqual(ev.ClipIDs, []string{"c", "d"}) {
				t.Fatalf("ClipIDs = %v, want [c d]", ev.ClipIDs)
			}
			wantMissing := []scriptpkg.MissingClipID{
				{ClipID: "a", Reason: "not_found"},
				{ClipID: "b", Reason: "drivenotfound"},
			}
			if !reflect.DeepEqual(ev.MissingClipIDs, wantMissing) {
				t.Fatalf("MissingClipIDs = %v, want %v (shape %s)",
					ev.MissingClipIDs, wantMissing, tc.name)
			}
		})
	}
}

// TestBuildClipEvidence_NilMissingIsOmitted pins the omitempty
// surface: when there are no missing IDs and the pack key is
// absent, BuildClipEvidence must produce nil MissingClipIDs (not
// []scriptpkg.MissingClipID{}), so JSON consumers don't see a
// phony "missing_clip_ids":[] field. PR 2 review preference:
// nil-vs-empty-slice asymmetry preserved.
func TestBuildClipEvidence_NilMissingIsOmitted(t *testing.T) {
	pack := map[string]any{
		"clip_ids": []string{"clip-a"},
		// missing_clip_ids absent → nil
	}
	ev := usecase.BuildClipEvidence(pack, "")
	if ev == nil {
		t.Fatal("ev = nil, want non-nil")
	}
	if ev.MissingClipIDs != nil {
		t.Fatalf("MissingClipIDs = %v, want nil "+
			"(absent missing key must NOT spawn an empty slice)", ev.MissingClipIDs)
	}
}

// TestBuildClipEvidence_EmptyMissingIsOmitted pins the same
// contract when the resolver explicitly emits an empty missing
// slice (defensive: a buggy future collector should never produce
// evidence with a phony "missing_clip_ids":[] field). BuildClipEvidence
// preserves the nil-ness for JSON omitempty.
func TestBuildClipEvidence_EmptyMissingIsOmitted(t *testing.T) {
	t.Run("typed_empty_slice", func(t *testing.T) {
		pack := map[string]any{
			"clip_ids":         []string{"clip-a"},
			"missing_clip_ids": []scriptpkg.MissingClipID{},
		}
		ev := usecase.BuildClipEvidence(pack, "")
		if ev == nil {
			t.Fatal("ev = nil, want non-nil")
		}
		if ev.MissingClipIDs != nil {
			t.Fatalf("MissingClipIDs = %v, want nil (zero-length input → nil output)", ev.MissingClipIDs)
		}
	})
	t.Run("map_empty_slice", func(t *testing.T) {
		pack := map[string]any{
			"clip_ids":         []string{"clip-a"},
			"missing_clip_ids": []map[string]any{},
		}
		ev := usecase.BuildClipEvidence(pack, "")
		if ev == nil {
			t.Fatal("ev = nil, want non-nil")
		}
		if ev.MissingClipIDs != nil {
			t.Fatalf("MissingClipIDs = %v, want nil", ev.MissingClipIDs)
		}
	})
}

// TestBuildClipEvidence_MissingEntryWithEmptyID_Dropped pins
// the defensive filter inside extractMissingClipIDs: a malformed
// entry (no clip_id) must NOT poison the evidence list with a
// blank-row. Reason can be empty (no enum validation in this
// layer — schema-side concerns, not builder-side).
func TestBuildClipEvidence_MissingEntryWithEmptyID_Dropped(t *testing.T) {
	pack := map[string]any{
		"clip_ids": []string{"clip-a"},
		"missing_clip_ids": []map[string]any{
			{"clip_id": "", "reason": "not_found"}, // empty id → dropped
			{"clip_id": "real", "reason": "drivenotfound"},
		},
	}
	ev := usecase.BuildClipEvidence(pack, "")
	if ev == nil {
		t.Fatal("ev = nil, want non-nil")
	}
	if len(ev.MissingClipIDs) != 1 {
		t.Fatalf("MissingClipIDs = %v, want 1 entry (empty-id dropped)",
			ev.MissingClipIDs)
	}
	if ev.MissingClipIDs[0].ClipID != "real" {
		t.Fatalf("MissingClipIDs[0].ClipID = %q, want %q",
			ev.MissingClipIDs[0].ClipID, "real")
	}
}

// TestBuildClipEvidence_ClipCount_MatchesLenClipIDs is the
// invariant contract: ClipCount is always len(ClipIDs), no
// matter the input shape. The packed clip_count value is
// intentionally ignored — BuildClipEvidence is the canonical
// source of truth.
func TestBuildClipEvidence_ClipCount_MatchesLenClipIDs(t *testing.T) {
	tests := []struct {
		name    string
		pack    map[string]any
		wantIDs []string
		wantCnt int
	}{
		{
			name:    "single_resolved_no_missing",
			pack:    map[string]any{"clip_ids": []string{"a"}},
			wantIDs: []string{"a"}, wantCnt: 1,
		},
		{
			name: "many_resolved_with_missing",
			pack: map[string]any{
				"clip_ids":         []string{"a", "b", "c"},
				"missing_clip_ids": []scriptpkg.MissingClipID{{ClipID: "m1", Reason: "not_found"}},
			},
			wantIDs: []string{"a", "b", "c"}, wantCnt: 3,
		},
		{
			name: "packed_clip_count_is_overridden",
			pack: map[string]any{
				"clip_ids":   []string{"a", "b"},
				"clip_count": 99, // deliberate wrong value
			},
			wantIDs: []string{"a", "b"}, wantCnt: 2,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ev := usecase.BuildClipEvidence(tc.pack, "")
			if ev == nil {
				t.Fatal("ev = nil")
			}
			if !reflect.DeepEqual(ev.ClipIDs, tc.wantIDs) {
				t.Fatalf("ClipIDs = %v, want %v", ev.ClipIDs, tc.wantIDs)
			}
			if ev.ClipCount != tc.wantCnt {
				t.Fatalf("ClipCount = %d, want %d", ev.ClipCount, tc.wantCnt)
			}
		})
	}
}

// TestBuildClipEvidence_ReasonConstantsArePinnedToEnumValues is
// a regression guard: the bounded enum values for the reason
// field MUST NOT silently drift. Any future PR that adds a new
// reason code is required to update both this test and the
// canonical docstring on MissingClipID (PR 5 contract pinned
// via zero-legacy policy 07).
func TestBuildClipEvidence_ReasonConstantsArePinnedToEnumValues(t *testing.T) {
	if scriptpkg.MissingClipReasonNotFound != "not_found" {
		t.Fatalf("MissingClipReasonNotFound = %q, want %q",
			scriptpkg.MissingClipReasonNotFound, "not_found")
	}
	if scriptpkg.MissingClipReasonDriveNotFound != "drivenotfound" {
		t.Fatalf("MissingClipReasonDriveNotFound = %q, want %q",
			scriptpkg.MissingClipReasonDriveNotFound, "drivenotfound")
	}
}
