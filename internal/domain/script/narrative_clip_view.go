// Package script — narrative_clip_view.go owns the per-slot
// construction + redaction audit for the model-facing
// NarrativeClipView projection.
//
// godlike/06 SSOT: the SHAPE of NarrativeClipView lives in
// source_spec.go (5 fields: SlotRef + Description + VisualSummary +
// Transcript + DurationMs). This file owns the construction
// (`NewNarrativeClipViewForSlot`), the explicit allow-list +
// deny-list of JSON field names, and the runtime redaction guard
// (`ValidateForModelView`) that ensures forbidden field names
// can never appear in the model-facing JSON envelope.
//
// godlike/07 NO-FAKE-AVAILABILITY: a forbidden field observed in
// the model-facing JSON is a hard redaction-leak failure. The
// deny-list is exhaustive of the 9 known infra-locator / over-
// disclosure field names (clip_id, asset_id, drive_link, file_hash,
// local_path, source_url, speaker, commentator, raw_metadata).
package script

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// AllowedNarrativeClipViewJSONFields is the canonical allow-list
// of JSON field names that may appear on a marshalled
// NarrativeClipView. The list is the single source of truth — the
// runtime redaction guard (ValidateForModelView) and the reflection
// guard (narrative_clip_view_test.go::TestNarrativeClipView_
// StructShapeStripsForbidden) BOTH validate against this slice.
//
// New model-facing fields must be added here AND to the
// NarrativeClipView struct simultaneously. Adding a field to the
// struct without the allow-list trips the test immediately.
var AllowedNarrativeClipViewJSONFields = []string{
	"slot_ref",
	"description",
	"visual_summary",
	"transcript",
	"duration_ms",
}

// ForbiddenNarrativeClipViewJSONFields is the canonical deny-list
// of JSON field names that MUST NEVER appear on a marshalled
// NarrativeClipView. Adding any of these as a struct member trips
// the redaction-leak guard.
//
// SlotRef is mandatory for slot-N binding reconciliation in the
// backend; it is the ONLY prefix-like marker allowed in the
// projection (and only because it does not identify a specific
// clip — "slot-1", "slot-2" bind to manifests, not media assets).
//
// Any new field that exposes infra-locator/state-machine data MUST
// be added here, not to the allow-list, before a regression seal
// can be lifted.
var ForbiddenNarrativeClipViewJSONFields = []string{
	"clip_id",      // canonical media_assets.id fragment
	"asset_id",     // exact same id under a different label
	"drive_link",   // Google Drive URL — internal locator
	"file_hash",    // content_hash / source_version locator
	"local_path",   // filesystem-absolute path — internal locator
	"source_url",   // origin URL (YouTube / Pexels / etc.)
	"speaker",      // diarization label — speaker identity leak
	"commentator",  // play-by-play identity leak
	"raw_metadata", // opaque metadata blob — source-side state
}

// Typed validation errors. Each is exposed as a sentinel so callers
// (engine prompt builder, audit log, archcheck precheck) can
// programmatic-distinguish via errors.Is.
var (
	// ErrNarrativeClipViewNilReceiver: a method was invoked on a
	// nil *NarrativeClipView pointer. Distinct from construction
	// errors so callers can branch on "pointer is nil" vs "view
	// carries a zero-value / missing field".
	ErrNarrativeClipViewNilReceiver = errors.New(
		"narrative_clip_view: nil receiver")

	// ErrNarrativeClipViewEmptySlotRef: SlotRef is required for
	// the per-slot binding machinery in the post-projector stage
	// (the backend binder reads SlotRef to look up the matching
	// BindingManifest entry). Empty SlotRef breaks the round-trip.
	ErrNarrativeClipViewEmptySlotRef = errors.New(
		"narrative_clip_view: slot_ref must not be empty")

	// ErrNarrativeClipViewRedactionLeak: the canonical godlike/07
	// sentinel for model-context contamination. Returned when ANY
	// forbidden JSON field name is observed on a marshalled
	// NarrativeClipView (struct-shape audit OR marshalled-bytes
	// audit). Wrapped with fmt.Errorf to surface the specific
	// forbidden name observed so the audit log carries the breach
	// detail.
	ErrNarrativeClipViewRedactionLeak = errors.New(
		"narrative_clip_view: redaction leak detected")
)

// NewNarrativeClipViewForSlot builds the per-slot, model-facing
// projection from a ClipCandidate + an optional VisualSummary +
// an explicit transcript + duration.
//
// The constructor enforces the strip by structural exclusion:
// the returned *NarrativeClipView has only the 5 fields allowed
// by the allow-list (SlotRef + Description + VisualSummary +
// Transcript + DurationMs). The richer ClipCandidate fields
// (AssetRef, SemanticScore, VisualScore, QualityScore,
// DriveLinkEmpty, WitnessedAtMs, PerSlotScoreBreakdown) are
// deliberately not carried over; the per-slot resolution
// machinery threads them through SlotClipBinding (the backend
// binding spec) instead, so the audit trail is preserved
// without leaking any infra locator to the model.
//
// SlotRef MUST be the planning-side slot reference ("slot-1",
// "slot-2", ...); it is not a clip_id and never identifies a
// specific media asset.
func NewNarrativeClipViewForSlot(
	slotRef string,
	candidate ClipCandidate,
	visualSummary *asset.VisualSummary,
	transcript string,
	durationMs int64,
) (*NarrativeClipView, error) {
	ref := strings.TrimSpace(slotRef)
	if ref == "" {
		return nil, ErrNarrativeClipViewEmptySlotRef
	}

	// TranscriptSnippet is the slot-side summary snippet
	// (description) — chosen because it's the model-friendly
	// text field, never an infra locator.
	description := strings.TrimSpace(candidate.TranscriptSnippet)

	var visualSummaryText string
	if visualSummary != nil {
		visualSummaryText = strings.TrimSpace(visualSummary.VisualSummaryText)
	}

	return &NarrativeClipView{
		SlotRef:       ref,
		Description:   description,
		VisualSummary: visualSummaryText,
		Transcript:    strings.TrimSpace(transcript),
		DurationMs:    durationMs,
	}, nil
}

// ValidateForModelView is the runtime redaction guard. It enforces
// THREE checks in sequence:
//
//  1. Struct-shape audit (reflect): every JSON-tagged field name on
//     NarrativeClipView must NOT appear in the deny-list and MUST
//     appear in the allow-list. Catches a future field addition
//     that exposes an infra locator (compile-time-equivalent at
//     test/runtime).
//
//  2. Marshalled-bytes audit: json.Marshal(v) MUST produce keys
//     strictly within the allow-list. The redaction leak test
//     uses this phase as the canonical fail-closed surface.
//
//  3. nil-safety: a nil receiver returns
//     ErrNarrativeClipViewNilReceiver so the typed-error contract
//     is preserved at every call site.
//
// godlike/07 NO-FAKE-AVAILABILITY: any violation produces a
// wrapped ErrNarrativeClipViewRedactionLeak so callers / the
// audit log can errors.Is-detect the breach type.
func (v *NarrativeClipView) ValidateForModelView() error {
	if v == nil {
		return ErrNarrativeClipViewNilReceiver
	}

	// Phase 1 — reflect the struct shape. Any forbidden tag is an
	// immediate fail.
	t := reflect.TypeOf(*v)
	var structTags []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		structTags = append(structTags, name)

		if err := assertTagAllowed(name); err != nil {
			return err
		}
	}

	// Phase 2 — marshal and inspect the produced bytes.
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("narrative_clip_view: marshal failed: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("narrative_clip_view: unmarshal failed: %w", err)
	}

	for k := range raw {
		if err := assertTagAllowed(k); err != nil {
			return fmt.Errorf(
				"narrative_clip_view: marshalled JSON contains forbidden key %q: %w",
				k, err)
		}
	}

	return nil
}

// assertTagAllowed is the shared invariant check used by both
// the struct-shape phase and the marshalled-bytes phase. Returns
// a wrapped ErrNarrativeClipViewRedactionLeak on denial.
//
// NOTE: the slot_ref tag is NOT intentionally listed in the
// deny-list because the slot reference is the only public-facing
// prefix-style identifier ("slot-1", "slot-2") that does not
// collapse to a specific media asset. Future additions must add
// their names to the allow-list AND to the struct definition
// simultaneously.
func assertTagAllowed(tag string) error {
	for _, forbidden := range ForbiddenNarrativeClipViewJSONFields {
		if tag == forbidden {
			return fmt.Errorf("%w: forbidden JSON field name %q",
				ErrNarrativeClipViewRedactionLeak, tag)
		}
	}
	for _, allowed := range AllowedNarrativeClipViewJSONFields {
		if tag == allowed {
			return nil
		}
	}
	return fmt.Errorf(
		"%w: JSON field name %q not in allow-list %v",
		ErrNarrativeClipViewRedactionLeak, tag,
		AllowedNarrativeClipViewJSONFields)
}

// sortForAudit is a deterministic ordering helper for the
// audit-stable serialization of the deny-list during fail-mode
// diagnostics. Currently unused directly — kept as godlike/06
// SSOT for future audit dumps.
func sortForAudit(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}
