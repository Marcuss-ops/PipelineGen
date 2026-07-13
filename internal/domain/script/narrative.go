// Package script — narrative.go defines the canonical contracts for
// the narrative evidence projection layer (NarrativeEvidenceProjector).
//
// NarrativeEvidence is the model-facing projection of resolved
// source material. It contains only narration-safe evidence blocks
// and excludes all infrastructure locators (clip_id, asset_id,
// Drive links, YouTube URLs, local paths, file hashes, raw metadata,
// speaker/commentator tags).
//
// BindingManifest carries the backend binding spec (slot → clip_id,
// Drive link, timestamps). It travels alongside NarrativeEvidence
// but is NEVER visible to the model.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of
// NarrativeEvidence, BindingManifest, and NarrativeClip. Anything
// that projects source material for model consumption MUST produce
// this shape. Anything that binds resolved clips to scenes MUST
// consume BindingManifest.
package script

// NarrativeEvidence is the model-facing projection of resolved
// source material. The model sees ONLY this shape — no infra IDs,
// no Drive links, no raw metadata.
//
// Construction: produced by NarrativeEvidenceProjector.Project.
// The projector strips all technical locators and keeps only
// evidence that can change the voiceover choice.
type NarrativeEvidence struct {
	// OriginalSource is the user-provided source text (topic +
	// source_text + guidelines), projected as PlainProse. Empty
	// when the source is clip-native (no user-supplied prose).
	OriginalSource PlainProse

	// Clips is the ordered list of narrative clip views. Each
	// entry represents one slot the model must narrate. The model
	// sees Slot + Description + VisualSummary + Transcript +
	// DurationMs — never clip_id, asset_id, Drive link, or
	// timestamps.
	Clips []NarrativeClip
}

// NarrativeClip is the model-facing view of a single resolved clip.
// By contract this struct EXCLUDES infra IDs: no clip_id, no
// asset_id, no drive_link, no local_path, no source_url, no
// speaker, no commentator, no raw_metadata.
//
// This is the production-site equivalent of NarrativeClipView
// (source_spec.go) — the latter is a planner-time projection;
// this is the evidence-projector-time projection. Both exclude
// the same fields; the difference is lifecycle stage.
type NarrativeClip struct {
	// Slot is the temporary skeleton key the model references.
	// Format: "slot-N" with N = 1..len(Clips). NOT a clip_id.
	Slot string

	// Description is the human-readable clip description or title.
	Description string

	// VisualSummary is a brief visual description of the clip content.
	VisualSummary string

	// Transcript is the canonical transcript excerpt for the clip.
	Transcript string

	// DurationMs is the clip duration in milliseconds.
	DurationMs int64
}

// PlainProse is a value object for user-provided source text that
// has been projected through ProseProjectionPolicy. It contains
// only narrative-safe prose — no URLs, no references, no metadata.
type PlainProse struct {
	value string
}

// NewPlainProse creates a PlainProse from a string. The caller
// MUST ensure the content has been projected through the canonical
// ProseProjectionPolicy before constructing this value.
func NewPlainProse(s string) PlainProse {
	return PlainProse{value: s}
}

// String returns the plain prose content.
func (p PlainProse) String() string { return p.value }

// IsEmpty reports whether the prose is empty.
func (p PlainProse) IsEmpty() bool { return p.value == "" }

// ── Binding manifest ──────────────────────────────────────────────

// BindingManifest carries the backend binding spec. It maps narrative
// slots to resolved clip IDs, Drive links, and timestamps. This data
// travels alongside NarrativeEvidence but is NEVER visible to the
// model.
//
// Construction: produced by the same resolver that produced the
// NarrativeEvidence. Consumed by BindingResolver.Bind to attach
// ClipBinding to each scene.
type BindingManifest struct {
	// Slots is the ordered list of binding entries. One per
	// narrative slot. The order matches NarrativeEvidence.Clips.
	Slots []BindingSlot
}

// BindingSlot maps a narrative slot to a resolved backend clip.
type BindingSlot struct {
	// Slot is the narrative slot reference ("slot-1", "slot-2", ...).
	// Matches NarrativeClip.Slot.
	Slot string

	// ClipID is the canonical asset ID of the resolved clip.
	// Internal — never visible to the model.
	ClipID string

	// ClipTitle is the human-readable clip title.
	ClipTitle string

	// DriveLink is the Google Drive URL for the clip.
	DriveLink string

	// StartMs is the optional clip start offset in milliseconds.
	StartMs int64

	// EndMs is the optional clip end offset in milliseconds.
	EndMs int64

	// DurationMs is the optional clip duration in milliseconds.
	DurationMs int64
}

// IsEmpty reports whether the manifest has no binding slots.
func (m *BindingManifest) IsEmpty() bool {
	return m == nil || len(m.Slots) == 0
}

// SlotByRef returns the BindingSlot for the given slot reference,
// or nil if not found.
func (m *BindingManifest) SlotByRef(slot string) *BindingSlot {
	if m == nil {
		return nil
	}
	for i := range m.Slots {
		if m.Slots[i].Slot == slot {
			return &m.Slots[i]
		}
	}
	return nil
}
