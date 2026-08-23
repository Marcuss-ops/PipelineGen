// Package script_test — narrative_clip_view_test.go is the
// canonical redaction-leak contract for the model-facing
// NarrativeClipView projection.
//
// godlike/07 NO-FAKE-AVAILABILITY: a forbidden field observed in
// the model-facing JSON is a hard fail. Each test in this file
// pins a STRUCTURAL invariant that cannot be silently regressed:
//
//   - TestNarrativeClipView_NewForSlot_PopulatesFourAllowedFields
//     pins the per-slot construction contract: 4 allowed fields
//     populated from a (deliberately dirty) ClipCandidate + a
//     VisualSummary + explicit transcript + duration.
//
//   - TestNarrativeClipView_StructShapeStripsForbidden pins the
//     REFLECT-LEVEL invariant: adding any of the 9 forbidden
//     JSON field names to the NarrativeClipView struct trips this
//     test immediately. The deny-list is checked bidirectionally:
//     forbidden names may not appear AND allow-list names that are
//     declared must actually exist on the struct.
//
//   - TestNarrativeClipView_JSONMarshallingStripsForbidden pins the
//     MARSHAL-LEVEL invariant: even when the FORBIDDEN STRING
//     appears inside a transcript / description value (free-form
//     user content, allowed), the marshalled JSON envelope MUST
//     NOT contain the forbidden STRING as a top-level KEY. The test
//     iterates every forbidden name and runs a sub-test, plus a
//     full-sentinel test that mixes all 9 forbidden names into one
//     transcript.
//
//   - TestNarrativeClipView_ValidateForModelView_HappyAndErrorPaths
//     pins the ValidateForModelView contract: happy path passes,
//     nil receiver surfaces ErrNarrativeClipViewNilReceiver,
//     empty SlotRef surfaces ErrNarrativeClipViewEmptySlotRef.
package script_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Test 1: per-slot construction populates the 4 allowed fields ─

// TestNarrativeClipView_NewForSlot_PopulatesFourAllowedFields
// verifies the constructor populates Slot + Description +
// VisualSummary + Transcript + DurationMs from a dirty
// ClipCandidate (whose AssetRef MUST NOT leak) plus a real
// VisualSummary plus an explicit transcript and duration.
func TestNarrativeClipView_NewForSlot_PopulatesFourAllowedFields(t *testing.T) {
	t.Parallel()

	candidate := scriptpkg.ClipCandidate{
		SlotRef:           "slot-source-id-should-not-leak",
		AssetRef:          "yt_RRJvrDKunyA_32_37_v1", // infra id — MUST NOT leak
		SemanticScore:     0.95,
		TranscriptSnippet: "Pacquiao mostra mobilità nel primo round",
		DurationMs:        9999, // deliberately different from the proj duration
	}
	summary := &asset.VisualSummary{
		VisualSummaryText:    "Opening round footwork jab",
		VisibleActions:       []string{"jab", "footwork"},
		VisibleEntities:      []string{"boxer_1", "ring"},
		FrameCount:           12,
		PreprocessingVersion: "vlm-sampler/1.0",
		ModelName:            "llava-1.6-7b",
		ModelVersion:         "2026-07-13",
	}

	view, err := scriptpkg.NewNarrativeClipViewForSlot(
		"slot-1",
		candidate,
		summary,
		"Pacquiao appears faster and lighter on his feet",
		5000,
	)
	require.NoError(t, err)
	require.NotNil(t, view)

	// 5 allowed fields populated.
	require.Equal(t, "slot-1", view.SlotRef)
	require.Equal(t, "Pacquiao mostra mobilità nel primo round", view.Description)
	require.Equal(t, "Opening round footwork jab", view.VisualSummary)
	require.Equal(t, "Pacquiao appears faster and lighter on his feet", view.Transcript)
	require.Equal(t, int64(5000), view.DurationMs)

	// The infra-id-bearing ClipCandidate fields MUST NOT surface:
	// the only infra-locator-shaped marker that survives is the
	// canonical SlotRef.
	require.NotContains(t, strings.TrimSpace(view.Description+" "+view.Transcript+" "+view.VisualSummary),
		"yt_RRJvrDKunyA", "AssetRef MUST NOT leak into Description/Transcript/VisualSummary")
	require.Equal(t, int64(5000), view.DurationMs,
		"DurationMs comes from the explicit projection param, NOT from ClipCandidate.DurationMs (which was 9999)")
}

// ── Test 2: struct-shape reflect audit catches forbidden names ────

// TestNarrativeClipView_StructShapeStripsForbidden is the canonical
// redaction-leak test at the REFLECT level. It walks every JSON
// tag on the NarrativeClipView struct and asserts:
//
//   - none of the 9 forbidden names appears as a JSON field name;
//   - every declared JSON field name is in the allow-list;
//   - every allow-list name is actually declared on the struct
//     (no orphan allow-list entries).
//
// Adding any forbidden-named field trips this test immediately —
// this is the godlike/07 structural enforcement layer.
func TestNarrativeClipView_StructShapeStripsForbidden(t *testing.T) {
	t.Parallel()

	var v scriptpkg.NarrativeClipView
	t0 := reflect.TypeOf(v)

	allowSet := make(map[string]struct{}, len(scriptpkg.AllowedNarrativeClipViewJSONFields))
	for _, name := range scriptpkg.AllowedNarrativeClipViewJSONFields {
		allowSet[name] = struct{}{}
	}
	forbidSet := make(map[string]struct{}, len(scriptpkg.ForbiddenNarrativeClipViewJSONFields))
	for _, name := range scriptpkg.ForbiddenNarrativeClipViewJSONFields {
		forbidSet[name] = struct{}{}
	}

	var declared []string
	for i := 0; i < t0.NumField(); i++ {
		f := t0.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		declared = append(declared, name)

		_, isForbidden := forbidSet[name]
		require.False(t, isForbidden,
			"GODLIKE-07-LEAK: forbidden JSON field name %q "+
				"appears on NarrativeClipView struct — this is a "+
				"model-context contamination. Update the deny-list "+
				"or remove the field.",
			name)

		_, isAllowed := allowSet[name]
		require.True(t, isAllowed,
			"GODLIKE-07-LEAK: JSON field name %q on NarrativeClipView "+
				"is not in AllowedNarrativeClipViewJSONFields — "+
				"extend the allow-list or rename.",
			name)
	}

	// Belt-and-braces: every allow-list name MUST be declared on the
	// struct (no orphan allow-list entries — those would silently
	// fail the model-facing projection contract).
	for _, allowed := range scriptpkg.AllowedNarrativeClipViewJSONFields {
		require.Contains(t, declared, allowed,
			"allow-list name %q is not declared on the struct — "+
				"add it to NarrativeClipView or remove from allow-list",
			allowed)
	}
}

// ── Test 3: marshalled JSON never leaks a forbidden top-level key ─

// TestNarrativeClipView_JSONMarshallingStripsForbidden pins the
// marshal-level invariant. The test deliberately writes the
// FORBIDDEN STRING NAME into the free-form transcript / description
// fields (allowed: those are user-provided free text), then asserts
// the marshalled JSON envelope MUST NOT contain the forbidden name
// as a TOP-LEVEL KEY (the value side is allowed because transcripts
// are user content; the structural key is what we forbid).
func TestNarrativeClipView_JSONMarshallingStripsForbidden(t *testing.T) {
	t.Parallel()

	forbiddenSentinels := map[string]string{
		"clip_id":         "FORBIDDEN_KEY_clip_id_string_in_transcript",
		"asset_id":        "FORBIDDEN_KEY_asset_id_string_in_transcript",
		"drive_link":      "FORBIDDEN_KEY_drive_link_string_in_transcript",
		"legacy_file_md5": "FORBIDDEN_KEY_file_hash_string_in_transcript",
		"local_path":      "FORBIDDEN_KEY_local_path_string_in_transcript",
		"source_url":      "FORBIDDEN_KEY_source_url_string_in_transcript",
		"speaker":         "FORBIDDEN_KEY_speaker_string_in_transcript",
		"commentator":     "FORBIDDEN_KEY_commentator_string_in_transcript",
		"raw_metadata":    "FORBIDDEN_KEY_raw_metadata_string_in_transcript",
	}

	for _, forbiddenKey := range scriptpkg.ForbiddenNarrativeClipViewJSONFields {
		forbiddenKey := forbiddenKey
		t.Run("forbidden_key_"+forbiddenKey, func(t *testing.T) {
			t.Parallel()
			sentinel := forbiddenSentinels[forbiddenKey]

			view, err := scriptpkg.NewNarrativeClipViewForSlot(
				"slot-1",
				scriptpkg.ClipCandidate{
					TranscriptSnippet: sentinel, // free-form — allowed
				},
				nil,
				sentinel, // free-form — allowed
				1000,
			)
			require.NoError(t, err)
			require.NotNil(t, view)

			b, err := json.Marshal(view)
			require.NoError(t, err)
			jsonString := string(b)

			var raw map[string]any
			require.NoError(t, json.Unmarshal(b, &raw))

			_, hasForbiddenKey := raw[forbiddenKey]
			require.False(t, hasForbiddenKey,
				"GODLIKE-07-LEAK: marshalled JSON envelope contains "+
					"top-level key %q — this is a model-context "+
					"contamination per godlike/07.\nMarshalled bytes: %s",
				forbiddenKey, jsonString)

			// Every observed key MUST be in the allow-list (no surprises).
			for k := range raw {
				require.Contains(t,
					scriptpkg.AllowedNarrativeClipViewJSONFields, k,
					"GODLIKE-07-LEAK: marshalled JSON contains unexpected "+
						"key %q (not in allow-list)",
					k)
			}
		})
	}

	// Belt-and-braces: all 9 forbidden sentinels mixed into one
	// transcript + one description; envelope still must not surface
	// ANY of them as a top-level key.
	t.Run("all_nine_forbidden_keys_mixed_into_one_transcript", func(t *testing.T) {
		t.Parallel()
		parts := make([]string, 0, len(forbiddenSentinels))
		for _, v := range forbiddenSentinels {
			parts = append(parts, v)
		}
		mixed := strings.Join(parts, " ")

		view, err := scriptpkg.NewNarrativeClipViewForSlot(
			"slot-1",
			scriptpkg.ClipCandidate{TranscriptSnippet: mixed},
			nil,
			mixed,
			5000,
		)
		require.NoError(t, err)

		b, err := json.Marshal(view)
		require.NoError(t, err)

		var raw map[string]any
		require.NoError(t, json.Unmarshal(b, &raw))

		for _, forbiddenKey := range scriptpkg.ForbiddenNarrativeClipViewJSONFields {
			_, present := raw[forbiddenKey]
			require.False(t, present,
				"GODLIKE-07-LEAK: full-sentinel marshal leaks top-level "+
					"key %q",
				forbiddenKey)
		}
	})
}

// ── Test 4: ValidateForModelView happy path + error paths ─────────

// TestNarrativeClipView_ValidateForModelView_HappyAndErrorPaths
// pins the ValidateForModelView contract:
//   - happy path passes;
//   - nil receiver surfaces ErrNarrativeClipViewNilReceiver;
//   - empty SlotRef surfaces ErrNarrativeClipViewEmptySlotRef (via
//     the constructor's gate).
func TestNarrativeClipView_ValidateForModelView_HappyAndErrorPaths(t *testing.T) {
	t.Parallel()

	// Happy path.
	goodView, err := scriptpkg.NewNarrativeClipViewForSlot(
		"slot-1",
		scriptpkg.ClipCandidate{TranscriptSnippet: "ok"},
		nil,
		"ok transcript",
		1000,
	)
	require.NoError(t, err)
	require.NoError(t, goodView.ValidateForModelView(),
		"well-formed NarrativeClipView MUST pass ValidateForModelView")

	// Nil receiver surfaces the typed sentinel.
	var nilView *scriptpkg.NarrativeClipView
	require.ErrorIs(t,
		nilView.ValidateForModelView(),
		scriptpkg.ErrNarrativeClipViewNilReceiver)

	// Empty SlotRef is rejected by the constructor (the typed
	// ErrNarrativeClipViewEmptySlotRef).
	_, err = scriptpkg.NewNarrativeClipViewForSlot(
		"",
		scriptpkg.ClipCandidate{},
		nil,
		"transcript",
		1000,
	)
	require.ErrorIs(t, err, scriptpkg.ErrNarrativeClipViewEmptySlotRef)
}

// ── Test 5: constructor leaves Forbidden-clip-candidate fields behind
//
// redaction is structural — the constructor's per-slot projection
// already drops ClipCandidate.AssetRef and friends. Pin that
// discipline: a candidate with all the dirty fields populated must
// produce a view with no leakage to any allowed field.
func TestNarrativeClipView_ConstructorScrubsClipCandidateInfraFields(t *testing.T) {
	t.Parallel()

	candidate := scriptpkg.ClipCandidate{
		SlotRef:               "slot-source-tag-leak",
		AssetRef:              "dir_link_should_not_leak:yt_clip_id_should_not_leak",
		SemanticScore:         0.99,
		TranscriptSnippet:     "ok description",
		DurationMs:            999_999,
		WitnessedAtMs:         123456,
		PerSlotScoreBreakdown: map[string]float64{"forbidden_metric": 0.5},
	}
	view, err := scriptpkg.NewNarrativeClipViewForSlot(
		"slot-2",
		candidate,
		&asset.VisualSummary{
			VisualSummaryText: "ok visual summary",
		},
		"ok transcript text",
		2500,
	)
	require.NoError(t, err)
	require.NotNil(t, view)

	// Compose every model-visible string and assert no infra marker.
	modelVisible := view.SlotRef + "|" +
		view.Description + "|" +
		view.VisualSummary + "|" +
		view.Transcript
	require.NotContains(t, modelVisible, "dir_link_should_not_leak")
	require.NotContains(t, modelVisible, "yt_clip_id_should_not_leak")
	require.NotContains(t, modelVisible, "forbidden_metric")
	require.NotContains(t, modelVisible, "999999", "candidate DurationMs MUST NOT override the explicit projection DurationMs")
}
