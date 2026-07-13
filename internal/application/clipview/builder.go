package clipview

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// NewCandidateView projects a raw internal Candidate into the
// model-facing CandidateView.
//
// Ref construction (SSOT):
//
//	The opaque ref is built as `<slotRef>:candidate-<index>` where
//	slotRef is the raw planner slot name AND index is the 0-based
//	position in the per-slot candidate list. This DOES NOT:
//	  - include the asset_id (Stripped — the backend can resolve
//	    it from the ref lookup table at binding time).
//	  - include content_hash (Stripped — these are
//	    model-adversarial in adversarial settings).
//	  - include the drive_link (Stripped — the backend resolves
//	    it after Gemma emits the selection).
//
// Construction invariants:
//   - The projection never panics; any internal-state failure
//     surfaces as a typed sentinel error so callers can probe via
//     errors.Is.
//
// godlike/07 NO-FAKE-AVAILABILITY: ErrCandidateViewEmptyRef fires
// when ref construction fails. We never invent a fallback ref.
//
// godlike/06 SSOT: this is the SOLE canonical constructor for
// CandidateView. Direct struct literals would bypass the
// marker; tests + future archcheck rules enforce this.
func NewCandidateView(slotRef string, index int, c Candidate) (*CandidateView, error) {
	slotRef = strings.TrimSpace(slotRef)
	if slotRef == "" {
		return nil, fmt.Errorf(
			"%w: slotRef MUST be non-empty (the projection must always know the slot to fold into the opaque ref)",
			ErrCandidateViewEmptyRef,
		)
	}
	if index < 0 {
		return nil, fmt.Errorf(
			"%w: index MUST be >= 0 (got %d)",
			ErrCandidateViewEmptyRef,
			index,
		)
	}

	ref := slotRef + ":candidate-" + strconv.Itoa(index)
	if c.AssetRef != "" {
		// Defensive: the AssetRef is intentionally NOT folded into
		// the ref; this branch documents the rule and is a no-op.
		_ = c.AssetRef
	}

	return &CandidateView{
		Ref:           ref,
		Description:   c.Description,
		VisualSummary: c.VisualSummary,
		Transcript:    c.Transcript,
		DurationMs:    c.DurationMs,
		Score:         c.Score,
	}, nil
}

// ValidateForModelView is the runtime redaction guard. Marshals
// the CandidateView to JSON and confirms:
//  1. none of the FORBIDDEN keys are present
//  2. the caller's ref is non-empty (so the projection cannot
//     have produced a zero-valued placeholder)
//
// Returns the parsed JSON map on success so callers can branch
// without re-marshalling.
//
// godlike/07 NO-FAKE-AVAILABILITY: any forbidden key surfaces as
// ErrCandidateViewRedactionLeak — never as a silent success.
// godlike/06 SSOT: this is the canonical runtime surface for
// "is this CandidateView safe to send to Gemma?". A caller that
// skips this check risks a production leak.
func (v *CandidateView) ValidateForModelView() (map[string]any, error) {
	if v == nil {
		return nil, ErrCandidateViewNilReceiver
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("clipview: marshal CandidateView: %w", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		return nil, fmt.Errorf("clipview: unmarshal CandidateView: %w", err)
	}
	if ref, _ := back["ref"].(string); strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf(
			"%w: model-facing JSON must carry a non-empty ref",
			ErrCandidateViewEmptyRef,
		)
	}
	for _, forbidden := range ForbiddenCandidateViewJSONFields {
		if _, present := back[forbidden]; present {
			return nil, fmt.Errorf(
				"%w: %q appeared in marshalled JSON (key name is in the canonical deny-list)",
				ErrCandidateViewRedactionLeak,
				forbidden,
			)
		}
	}
	return back, nil
}

// MarshalForModelView is a convenience: marshals + validates in one
// step. Returns the raw JSON bytes (NOT pretty-printed) plus nil
// error on the safe path; on failure returns ([]byte, error).
//
// godlike/06 SSOT: this is the canonical "send to Gemma" surface.
// Construction callers should call this (NOT json.Marshal alone)
// so the validate-then-marshal discipline is one call at the
// composition root.
func (v *CandidateView) MarshalForModelView() ([]byte, error) {
	if v == nil {
		return nil, ErrCandidateViewNilReceiver
	}
	if _, err := v.ValidateForModelView(); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
