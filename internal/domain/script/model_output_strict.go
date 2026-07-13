// Package script — model_output_strict.go
//
// Defines the strict LLM-COMPACT-CONTRACT envelope and its canonical
// validator. The LLM MUST emit only:
//
//	{
//	  "segments": [
//	    {"ref": "slot-1", "text": "..."},
//	    {"ref": "slot-2", "text": "..."}
//	  ]
//	}
//
// Any other shape — schema_version + text + specscene + scenes, segments
// with extra keys (clip_id, id, index, kind, bindings), out-of-plan
// refs, or empty segments — is REJECTED by ParseModelOutputStrict
// with a typed sentinel error.
//
// godlike/06 SSOT (one canonical owner per fact): this file owns the
// strict envelope shape. The application layer MUST go through
// ParseModelOutputStrict; ad-hoc unmarshal into the legacy V1 shape is
// a forward-prevention violation (godlike/08).
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure path emits a typed
// sentinel that the application layer can match via errors.Is — there
// is no silent-success fallback. The validator never returns
// (zero-value, nil) when the input is malformed.
package script

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for strict-envelope validation. Each is wrapped
// (via fmt.Errorf with %w) so callers can match with errors.Is while
// still surfacing positional context (the offending key/ref/index).
var (
	// ErrModelOutputExtraField is returned when:
	//   - the top-level object contains a key other than "segments"
	//     (e.g. "schema_version", "text", "specscene", "scenes",
	//     "version", "ok", etc.); OR
	//   - a segment object contains a key other than "ref" and "text"
	//     (e.g. "id", "index", "clip_id", "kind", "bindings",
	//     "scene", "speaker", any extra marker).
	//
	// godlike/07 honest lock: the validator REJECTS rather than
	// silently accepting / fuzzing forward. A model that emits
	// schema_version+specscene is forced to fail loudly so the
	// operator can fix the upstream contract — never silently
	// accepted as "compat".
	ErrModelOutputExtraField = errors.New("script: model output has extra field outside the strict envelope {segments:[{ref,text}]}")

	// ErrModelOutputRefNotInPlan is returned when a segment's "ref"
	// is not in the validRefs set the engine supplied (typically
	// {slot-1, slot-2, ..., slot-N} derived from the active plan's
	// ClipEvidence.AcceptedClipIDs or Segments). The model invented
	// a slot that the plan did not authorise — FAIL CLOSED.
	ErrModelOutputRefNotInPlan = errors.New("script: model output ref is not in the active plan")

	// ErrModelOutputEmptySegments is returned when the parsed
	// envelope has zero segments — defensively rejecting an empty
	// script (the spec contract requires at least 1 segment).
	ErrModelOutputEmptySegments = errors.New("script: model output empty segments array (the strict envelope requires ≥1)")
)

// ModelOutputSegment is a single (slot ref, prose text) pair emitted
// by the model. The two-key shape is canonical — adding fields here
// is a godlike/06 SSOT violation.
type ModelOutputSegment struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

// ModelOutput is the canonical strict envelope. The zero value
// (Segments: nil) is invalid; use ParseModelOutputStrict to obtain
// a populated value or a typed error.
type ModelOutput struct {
	Segments []ModelOutputSegment `json:"segments"`
}

// ParseModelOutputStrict parses the LLM output bytes as a strict
// ModelOutput envelope. It rejects all structural and semantic
// deviations with the typed sentinels above.
//
// validRefs is the set of allowed segment refs — typically derived
// from the active plan via DeriveValidRefsFromPlan (caller-side
// helper, see internal/application/scripts/usecase/json_envelope_helpers.go).
//
// godlike/06: this is the SOLE canonical decoder. Decoders that
// unmarshal into the legacy V1 shape are INTERNALLY FORBIDDEN.
// godlike/07: every failure path returns a typed error — no silent
// success, no best-effort fallback.
func ParseModelOutputStrict(raw []byte, validRefs map[string]struct{}) (ModelOutput, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return ModelOutput{}, fmt.Errorf("%w: empty raw bytes", ErrModelOutputEmptySegments)
	}

	// First pass: probe unknown top-level keys via map[string]json.RawMessage.
	// This lets the validator reject "schema_version", "text", "specscene",
	// "scenes", "version" etc. BEFORE unmarshalling into ModelOutput.
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return ModelOutput{}, fmt.Errorf("script: model output decode failed (strict): %w", err)
	}
	if len(root) != 1 {
		// More than one top-level key — collect the offending key(s)
		// for diagnostics, then reject.
		offenders := make([]string, 0, len(root))
		for k := range root {
			if k != "segments" {
				offenders = append(offenders, k)
			}
		}
		return ModelOutput{}, fmt.Errorf("%w: top-level %v (only `segments` is allowed)",
			ErrModelOutputExtraField, offenders)
	}
	segmentsRaw, ok := root["segments"]
	if !ok {
		return ModelOutput{}, fmt.Errorf("%w: missing required top-level `segments` key",
			ErrModelOutputExtraField)
	}

	// Second pass: probe each segment for extra keys (raw map of RawMessage).
	var rawSegments []map[string]json.RawMessage
	if err := json.Unmarshal(segmentsRaw, &rawSegments); err != nil {
		return ModelOutput{}, fmt.Errorf("script: model output decode failed (strict `segments`): %w", err)
	}
	if len(rawSegments) == 0 {
		return ModelOutput{}, ErrModelOutputEmptySegments
	}

	out := ModelOutput{Segments: make([]ModelOutputSegment, 0, len(rawSegments))}
	for i, rawSeg := range rawSegments {
		// Defensive: a segment may ONLY have "ref" and "text".
		if len(rawSeg) > 2 || len(rawSeg) < 2 {
			offenders := make([]string, 0, len(rawSeg))
			for k := range rawSeg {
				offenders = append(offenders, k)
			}
			return ModelOutput{}, fmt.Errorf("%w: segment %d has keys %v (only `ref` and `text` are allowed)",
				ErrModelOutputExtraField, i, offenders)
		}
		// Both keys must be present.
		refRaw, hasRef := rawSeg["ref"]
		textRaw, hasText := rawSeg["text"]
		if !hasRef || !hasText {
			missing := []string{}
			if !hasRef {
				missing = append(missing, "ref")
			}
			if !hasText {
				missing = append(missing, "text")
			}
			return ModelOutput{}, fmt.Errorf("%w: segment %d missing required key(s) %v",
				ErrModelOutputExtraField, i, missing)
		}

		var seg ModelOutputSegment
		if err := json.Unmarshal(refRaw, &seg.Ref); err != nil {
			return ModelOutput{}, fmt.Errorf("script: model output segment %d `ref` decode error: %w", i, err)
		}
		if err := json.Unmarshal(textRaw, &seg.Text); err != nil {
			return ModelOutput{}, fmt.Errorf("script: model output segment %d `text` decode error: %w", i, err)
		}

		// godlike/07: ref MUST be in the active plan's valid set. Fail closed.
		if _, ok := validRefs[seg.Ref]; !ok {
			return ModelOutput{}, fmt.Errorf("%w: %q at segment %d (allowed: allowed-set cardinality %d)",
				ErrModelOutputRefNotInPlan, seg.Ref, i, len(validRefs))
		}

		if strings.TrimSpace(seg.Text) == "" {
			return ModelOutput{}, fmt.Errorf("script: model output segment %d has empty `text` (godlike/07 fail closed)", i)
		}

		out.Segments = append(out.Segments, seg)
	}

	return out, nil
}
