// Package usecase — json_envelope_helpers.go
//
// Application-layer glue for the LLM-COMPACT-CONTRACT strict envelope
// (LLM-COMPACT-CONTRACT, PR-CS-1 follow-up, July 2026):
//   - JSONOutputInstruction is the prompt suffix that authoritatively
//     instructs the model to emit ONLY the strict envelope shape.
//   - DeriveValidRefsFromPlan computes the {slot-1, ..., slot-N} set
//     the engine expects the validator to accept (N = number of
//     segments the plan authored).
//   - BuildNarrativeClipViews renders the per-slot CLIP VIEW block
//     (source_excerpt + topic + underlying clip_id when present) that
//     is INJECTED into the engine prompt so the model has grounding
//     for every "ref" it is allowed to emit.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// valid-ref computation + per-slot CLIP VIEW block render. Other
// call-sites that build a slot list or a CLIP VIEW block MUST route
// through these helpers (no inlined reimplementations).
//
// godlike/07 NO-FAKE-AVAILABILITY: DeriveValidRefsFromPlan clamps to
// at least 1 slot (slot-1) for nil/empty plans rather than returning
// the empty set, so the validator never hits the empty-segments
// pre-condition with no allowed refs (which would force every
// ref-not-in-plan error to fail-closed even for a structural
// well-formed envelope).
//
// Addtive only: this file does NOT modify the existing
// plainTextInstruction prompt path. Future waves may migrate the
// engine end-to-end onto these helpers; this wave establishes the
// contract surface.
package usecase

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// JSONOutputInstruction is the prompt suffix that authoritatively
// instructs the model to emit ONLY the strict envelope shape.
//
// PR-CS-1 follow-up (LLM-COMPACT-CONTRACT, July 2026). The model
// is FORBIDDEN from producing:
//   - schema_version / specscene / scenes keys (legacy V1 shape).
//   - top-level keys other than `segments`.
//   - per-segment keys other than `ref` and `text` (clip_id, id,
//     index, kind, bindings, speaker labels, etc. are REJECTED).
//   - any "ref" not in the {slot-1, ..., slot-N} set introduced
//     by the CLIP VIEWS block (see BuildNarrativeClipViews).
//
// godlike/07 honest lock: the contract lists every forbidden shape
// so a model that drifts gets a typed sentinel on parse rather than
// silently being fuzzed through the binder.
const JSONOutputInstruction = `
[OUTPUT_FORMAT — STRICT JSON ENVELOPE — LLM-COMPACT-CONTRACT]
You MUST emit a single JSON object with EXACTLY this shape and NO OTHER keys at any nesting level:

{
  "segments": [
    { "ref": "slot-<N>", "text": "<narrative prose>" }
  ]
}

Rules:
1. The top-level object MUST contain exactly one key: "segments". Any other top-level key (schema_version, text, specscene, scenes, ok, version, etc.) is REJECTED.
2. The "segments" array MUST contain at least one element.
3. Each segment object MUST contain exactly two keys — "ref" and "text". Any other per-segment key (clip_id, id, index, kind, bindings, scene-N, speaker, etc.) is REJECTED.
4. Each "ref" MUST be exactly "slot-1", "slot-2", ... up to the slot count the [CLIP VIEW] block defined. Any ref outside that set is REJECTED.
5. Each "text" MUST be non-empty narrative prose for that slot's underlying clip.
6. DO NOT wrap the JSON in markdown fences, code blocks, or any commentary outside the JSON object.
7. DO NOT output schema_version, specscene, scenes, scene IDs, scene indexes, kind labels, or bindings objects — those are owned by downstream Go code.

Defensive: any extra field, unknown ref, empty ` + "`text`" + `, or non-JSON output is REJECTED on parse.
`

// DeriveValidRefsFromPlan canonical owner is now strict_contract_composer.go
// (godlike/06 SSOT composition site). Same-package callers (tests in this
// directory) resolve the symbol automatically. This file retains its
// prompt-glue role: JSONOutputInstruction + BuildNarrativeClipViews.

// BuildNarrativeClipViews renders the per-slot CLIP VIEW block that
// is INJECTED into the engine prompt so the model has grounding for
// every `ref` it is allowed to emit. The block is also the contract
// surface DeriveValidRefsFromPlan reads from — both helpers MUST
// stay in sync (godlike/06 SSOT).
//
// The block:
//
//	[CLIP VIEW: slot-<N>]
//	Topic: <plan.Segments[i].Topic when present, blank otherwise>
//	Source excerpt: <plan.Segments[i].SourceText when present, blank otherwise>
//	Underlying clip: <plan.ClipEvidence.AcceptedClipIDs[i] when present>
//	---
//
// Returns "" for nil plan or plan with no Segments — callers
// appending the block to the prompt should treat empty return as
// "no per-slot CLIP VIEW groundings authored for this plan".
func BuildNarrativeClipViews(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil || len(plan.Segments) == 0 {
		return ""
	}

	var clipIDs []string
	if plan.ClipEvidence != nil {
		clipIDs = plan.ClipEvidence.AcceptedClipIDs
	}

	var b strings.Builder
	b.WriteString("\n\n[CLIP VIEWS — one per slot, in order]\n\n")
	for i, seg := range plan.Segments {
		slot := fmt.Sprintf("slot-%d", i+1)
		fmt.Fprintf(&b, "[CLIP VIEW: %s]\n", slot)
		if strings.TrimSpace(seg.Topic) != "" {
			fmt.Fprintf(&b, "Topic: %s\n", strings.TrimSpace(seg.Topic))
		}
		if strings.TrimSpace(seg.SourceText) != "" {
			fmt.Fprintf(&b, "Source excerpt: %s\n", strings.TrimSpace(seg.SourceText))
		}
		if i < len(clipIDs) && strings.TrimSpace(clipIDs[i]) != "" {
			fmt.Fprintf(&b, "Underlying clip: %s\n", strings.TrimSpace(clipIDs[i]))
		}
		b.WriteString("---\n")
	}
	return b.String()
}
