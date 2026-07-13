// Package usecase — engine_prompt_pacquiao_test.go pins the
// LLM-COMPACT-CONTRACT wave against the canonical Pacquiao
// fixture (tests/operational/pacquiao_broner_script_mini_test.json).
//
// USER-SPEC invariants pinned here:
//  1. buildSegmentInstructions injects topic + source_excerpt
//     + Ref: slot-N header into the prompt.
//  2. buildNarrativeClipViews injects a per-slot
//     NarrativeClipView JSON block with the canonical 5-field
//     shape (slot_ref, description, visual_summary, transcript,
//     duration_ms) — strict AllowList only, no infra-locators.
//  3. jsonOutputInstruction appends the strict
//     {segments:[{ref,text}]} contract block.
//  4. ParseModelOutputStrict on the canonical Pacquiao
//     response shape succeeds with the plan's slot set.
//  5. ParseModelOutputStrict on common injection shapes
//     fails closed with the right typed sentinel.
package usecase

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// makePacquiaoFixturePlan builds the canonical Pacquiao topology
// matching tests/operational/pacquiao_broner_script_mini_test.json:
//
//   - 1 clip: yt_RRJvrDKunyA_32_37_v1 (Pacquiao-Broner Round 1)
//   - 1 segment: topic "fase di studio iniziale",
//     source text "Pacquiao mostra subito mobilita e rapidita
//     di gambe, jab da mancino, prendere le misure…"
//   - target_words 55, segment_words 11, num_clips 1
//   - Italian / documentary tone
//
// godlike/06 SSOT: this is the canonical projection of the
// fixture into the ResolvedGenerationPlan shape used by the
// prompt builders and the strict validator. Use this function
// in any future Pacquiao regression test so the topology stays
// identical.
func makePacquiaoFixturePlan() *scriptpkg.ResolvedGenerationPlan {
	const clipID = "yt_RRJvrDKunyA_32_37_v1"
	const segmentTopic = "fase di studio iniziale"
	const segmentExcerpt = "Pacquiao mostra subito mobilita e rapidita di gambe, jab da mancino, prendere le misure, " +
		"Pacquiao parte piu veloce e piu leggero sui piedi."

	return &scriptpkg.ResolvedGenerationPlan{
		ID:       "pacquiao-broner-mini",
		Title:    "Manny Pacquiao vs Adrien Broner: recap essenziale",
		Topic:    "Pacquiao vs Broner: fase di studio iniziale",
		Language: "it",
		Tone:     "documentary",
		Model:    "gemma4:e4b",
		Mode:     "clip_to_script",
		// Per-script sizing (matches fixture target_words=55).
		TargetWords:  55,
		SegmentWords: 11,
		NumClips:     1,
		// Single clip evidence (matches fixture clip_ids=[…]).
		ClipEvidence: &scriptpkg.ClipEvidence{
			AcceptedClipIDs:   []string{clipID},
			RenderableClipIDs: []string{clipID},
			ClipCount:         1,
			AssembledText:     "CLIP " + clipID + ": Pacquiao-Broner Round 1\n  Description: opening jab measurement\n  Transcript: " + segmentExcerpt + "\n",
			ClipNames: map[string]string{
				clipID: "Pacquiao-Broner Round 1",
			},
			DriveLinks: map[string]string{
				clipID: "https://drive.google.com/file/d/pacquiao-broner-r1",
			},
		},
		// Single segment block (matches segment_topics[0]).
		Segments: []scriptpkg.ScriptSegment{
			{
				Topic:       segmentTopic,
				SourceText:  segmentExcerpt,
				TargetWords: 11,
			},
		},
	}
}

// ── 1. Prompt emit: per-segment topic + source_excerpt + Ref: slot ──────

func TestEnginePrompt_Pacquiao_SegmentBlock_ContainsTopicAndExcerptAndRef(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()

	got := buildSegmentInstructions(plan)

	// Header + topic + target words + source_excerpt + ref bind
	// marker MUST all appear for the canonical Pacquiao topology.
	mustContain(t, got, []string{
		"SEGMENT 1 (Ref: slot-1)",
		"Topic: fase di studio iniziale",
		"Target words: 11",
		"Source text excerpt:",
		"Pacquiao mostra subito mobilita",
	})

	// Footer line MUST remain canonical (per existing DoD).
	mustContain(t, got, []string{
		"Write one continuous narrative.",
		"Output only the script text",
	})
}

// ── 2. Prompt emit: NarrativeClipView per slot ──────────────────────────

func TestEnginePrompt_Pacquiao_NarrativeClipViews_ContainsJSONPerSlot(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()

	got := buildNarrativeClipViews(plan)

	// Per-godlike/06 SSOT: the prompt includes a
	// NarrativeClipView-shaped JSON block per slot, with the
	// canonical 5 allow-listed fields and no infra-locator
	// fields.
	mustContain(t, got, []string{
		"CLIP VIEWS",
		"slot-1:",
		`"slot_ref":"slot-1"`,
		`"description"`,
		// Pacquiao's clip name flows through as the
		// description (the only candidate-friendly
		// field at the prompt layer).
		"Pacquiao-Broner Round 1",
	})

	// Forbidden infra-locator fields MUST NOT leak through
	// the prompt emit. godlike/06 redact discipline.
	mustNotContain(t, got, []string{
		`"clip_id"`,
		`"asset_id"`,
		`"drive_link"`,
		`"file_hash"`,
		`"local_path"`,
		`"source_url"`,
		`"speaker"`,
		`"commentator"`,
		`"raw_metadata"`,
	})
}

func TestEnginePrompt_Pacquiao_NarrativeClipViews_NoClips_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()
	plan.ClipEvidence = nil

	if got := buildNarrativeClipViews(plan); got != "" {
		t.Fatalf("plan with no clip evidence MUST return an empty CLIP VIEWS block, got %q", got)
	}
}

// ── 3. Prompt emit: jsonOutputInstruction is appended ────────────────────

func TestEnginePrompt_Pacquiao_JsonOutputInstruction_IsAppended(t *testing.T) {
	t.Parallel()

	// We invoke jsonOutputInstruction directly (it's a const
	// inside engine_prompt.go). The engine calls this in
	// engine_generate.go::Generate — package-internal so the
	// test can also touch it.
	if !strings.Contains(jsonOutputInstruction, "[OUTPUT_FORMAT") {
		t.Fatalf("jsonOutputInstruction MUST carry the [OUTPUT_FORMAT] header")
	}
	if !strings.Contains(jsonOutputInstruction, "MUST emit ONLY a strict JSON object") {
		t.Fatalf("jsonOutputInstruction MUST pin the EXACT prescribed envelope")
	}
	if !strings.Contains(jsonOutputInstruction, `"segments"`) {
		t.Fatalf("jsonOutputInstruction MUST mention the `segments` key in the prescribed shape")
	}
	if !strings.Contains(jsonOutputInstruction, `"ref"`) || !strings.Contains(jsonOutputInstruction, `"text"`) {
		t.Fatalf("jsonOutputInstruction MUST pin the per-segment EXACT shape (ref + text only)")
	}
	// godlike/07 defense: anything else is rejected.
	if !strings.Contains(jsonOutputInstruction, "REJECTED") {
		t.Fatalf("jsonOutputInstruction MUST mark off-contract shapes as REJECTED (defense against V1-shaped injection)")
	}
}

// ── 4. Validator: canonical Pacquiao payload parses through ─────────────

func TestEnginePrompt_Pacquiao_Validator_AcceptsCanonicalPayload(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()
	validRefs := DeriveValidRefsFromPlan(plan)

	// Sanity: the plan's slot set is exactly {slot-1} for
	// Pacquiao topology (1 segment + 1 clip).
	require.Len(t, validRefs, 1, "Pacquiao topology MUST produce a single slot")
	_, ok := validRefs["slot-1"]
	require.True(t, ok, "Pacquiao topology MUST include slot-1 in validRefs")

	// Canonical response: 1 segment, slot-1, real Italian prose
	// matching the source excerpt's spirit.
	canonical := []byte(`{
	  "segments": [
	    {
	      "ref": "slot-1",
	      "text": "Pacquiao parte subito piu veloce sui piedi, impostando il ritmo con il jab da mancino."
	    }
	  ]
	}`)
	out, err := scriptpkg.ParseModelOutputStrict(canonical, validRefs)
	require.NoError(t, err, "canonical Pacquiao payload MUST parse")
	require.Len(t, out.Segments, 1)
	assert.Equal(t, "slot-1", out.Segments[0].Ref)
	assert.Equal(t, "Pacquiao parte subito piu veloce sui piedi, impostando il ritmo con il jab da mancino.",
		out.Segments[0].Text)

	// MSOV1 composition matches the validator's output.
	ms := composeModelOutputToMSOV1(out, plan)
	require.NotNil(t, ms)
	assert.Equal(t, 1, ms.SchemaVersion)
	require.Len(t, ms.SpecScene.Scenes, 1)
	// Pacquiao has 1 clip-backed slot → Kind = SceneClip
	// (since i=0 < clipSlotCount=1).
	assert.Equal(t, scriptpkg.SceneClip, ms.SpecScene.Scenes[0].Kind)
}

// ── 5. Validator: rejection paths on the Pacquiao topology ─────────────

func TestEnginePrompt_Pacquiao_Validator_RejectsExtraFieldOnSegment(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()
	validRefs := DeriveValidRefsFromPlan(plan)

	// A model that follows an injection and slips a
	// infra-locator into a segment MUST be caught.
	payload := []byte(`{
	  "segments": [
	    {
	      "ref": "slot-1",
	      "text": "Pacquiao parte subito.",
	      "clip_id": "yt_RRJvrDKunyA_32_37_v1"
	    }
	  ]
	}`)
	_, err := scriptpkg.ParseModelOutputStrict(payload, validRefs)
	require.Error(t, err, "infra-locator per-segment field MUST be rejected (LLM-COMPACT-CONTRACT godlike/07)")
	assert.True(t, errors.Is(err, scriptpkg.ErrModelOutputExtraField),
		"infra-locator per-segment rejection MUST surface ErrModelOutputExtraField, got %v", err)
}

func TestEnginePrompt_Pacquiao_Validator_RejectsRefNotInPlan(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()
	validRefs := DeriveValidRefsFromPlan(plan)

	// The model's attempt to bind to a clip ID (which the
	// plan does NOT include in validRefs) MUST be caught.
	payload := []byte(`{
	  "segments": [
	    {"ref": "yt_RRJvrDKunyA_32_37_v1", "text": "Pacquiao."}
	  ]
	}`)
	_, err := scriptpkg.ParseModelOutputStrict(payload, validRefs)
	require.Error(t, err, "ref outside validRefs set MUST be rejected — anti-hallucination gate")
	assert.True(t, errors.Is(err, scriptpkg.ErrModelOutputRefNotInPlan),
		"non-plan ref MUST surface ErrModelOutputRefNotInPlan, got %v", err)
}

func TestEnginePrompt_Pacquiao_Validator_RejectsV1ShapedInjection(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()
	validRefs := DeriveValidRefsFromPlan(plan)

	// A model that follows the "Return JSON" injection and
	// emits a V1 envelope MUST be rejected (no graceful
	// fallback to a V1 wrapper anymore under LLM-COMPACT-
	// CONTRACT).
	v1Shape := []byte(`{
	  "schema_version": 1,
	  "text": "Pacquiao opens aggressively.",
	  "specscene": {"version": 1, "scenes": []}
	}`)
	_, err := scriptpkg.ParseModelOutputStrict(v1Shape, validRefs)
	require.Error(t, err, "V1-shaped injection MUST be rejected")
	assert.True(t, errors.Is(err, scriptpkg.ErrModelOutputExtraField),
		"V1 envelope rejection MUST surface ErrModelOutputExtraField, got %v", err)
}

func TestEnginePrompt_Pacquiao_Validator_RejectsEmptySegmentsArray(t *testing.T) {
	t.Parallel()
	plan := makePacquiaoFixturePlan()
	validRefs := DeriveValidRefsFromPlan(plan)

	empty := []byte(`{"segments":[]}`)
	_, err := scriptpkg.ParseModelOutputStrict(empty, validRefs)
	require.Error(t, err)
	assert.True(t, errors.Is(err, scriptpkg.ErrModelOutputEmptySegments))
}
