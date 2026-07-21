// Package mediamemory — scene_visual_plan_dto.go is the canonical
// JSON wire shape consumed by the headless compositing engine
// (Fase 4.2).
//
// godlike/06 SSOT (wire-shape SSOT): the DTO field set is the
// SINGLE source of truth for what the renderer reads. Any drift
// between the canonical SceneVisualPlan / Layer types and these
// DTOs surfaces as a serialization error in ParsePlans.
//
// godlike/06 SSOT (schema versioning): the wire envelope
// carries a SchemaVersion field so future schema bumps are
// non-breaking for older renderers. The renderer's parser MUST
// reject any envelope whose schema_version is unknown.
//
// godlike/06 SSOT (snake_case convention): every JSON tag uses
// snake_case to match the canonical wire convention used
// elsewhere in PipelineGen (e.g. phrase_fingerprint,
// media_type, project_id).
package mediamemory

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ScenePlanSchemaVersion is the canonical wire schema version.
// godlike/06 SSOT (bump contract): every future change to the
// PlanDTO / LayerDTO struct shape MUST bump this constant
// (e.g. v1 → v2) and add the corresponding parse path. Older
// renderers that only recognise v1 reject v2 envelopes cleanly
// at the parser boundary.
const ScenePlanSchemaVersion = "v1"

// PlanEnvelope is the top-level wire shape. godlike/06 SSOT
// (envelope over array): the renderer parses one PlanEnvelope
// per project, not one per scene. Multiple plans across scenes
// batch into one envelope so the renderer can ingest a full
// project's plan in one I/O round-trip.
type PlanEnvelope struct {
	SchemaVersion string    `json:"schema_version"`
	ProjectID     string    `json:"project_id"`
	Plans         []PlanDTO `json:"plans"`
}

// PlanDTO is the JSON wire shape of one SceneVisualPlan. Fields
// mirror the canonical SceneVisualPlan with snake_case tags.
type PlanDTO struct {
	ProjectID  string     `json:"project_id"`
	SceneID    string     `json:"scene_id"`
	Text       string     `json:"text"`
	Language   string     `json:"language"`
	DurationMs int64      `json:"duration_ms"`
	Layers     []LayerDTO `json:"layers"`
	Source     string     `json:"source"`
}

// LayerDTO is the JSON wire shape of one Layer. Fields mirror
// the canonical Layer + coerce Layout to the canonical
// LayoutKind predicate so the renderer's IsKnownLayout call
// rejects drift at parse time.
type LayerDTO struct {
	Slot           string  `json:"slot"`
	AssetID        string  `json:"asset_id"`
	BindingID      string  `json:"binding_id"`
	StartMs        int64   `json:"start_ms"`
	EndMs          int64   `json:"end_ms"`
	Layout         string  `json:"layout"`
	CandidateScore float64 `json:"candidate_score"`
	Provider       string  `json:"provider"`
}

// SerializePlans projects canonical plans into the wire envelope.
// godlike/06 SSOT: always emit the schema_version so the
// renderer can branch on it without breaking the wire format.
func SerializePlans(projectID string, plans []SceneVisualPlan) ([]byte, error) {
	dtoPlans := make([]PlanDTO, 0, len(plans))
	for _, p := range plans {
		dto, err := dtoFromPlan(p)
		if err != nil {
			return nil, fmt.Errorf(
				"mediamemory: SerializePlans scene=%q: %w", p.SceneID, err)
		}
		dtoPlans = append(dtoPlans, dto)
	}
	env := PlanEnvelope{
		SchemaVersion: ScenePlanSchemaVersion,
		ProjectID:     projectID,
		Plans:         dtoPlans,
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf(
			"mediamemory: SerializePlans marshal: %w", err)
	}
	return out, nil
}

// ParsePlans validates the wire envelope and projects back to
// canonical plans. godlike/07 NO-FAKE-AVAILABILITY: an unknown
// schema_version surface as ErrPlanSchemaDrift (a typed
// sentinel wrapping the canonical envelope) so the caller can
// branch on errors.Is without parsing the error string.
func ParsePlans(raw []byte) (string, []SceneVisualPlan, error) {
	if len(raw) == 0 {
		return "", nil, ErrPlanSchemaDrift
	}
	var env PlanEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", nil, fmt.Errorf(
			"mediamemory: ParsePlans unmarshal: %w", err)
	}
	if env.SchemaVersion != ScenePlanSchemaVersion {
		return env.ProjectID, nil, fmt.Errorf(
			"mediamemory: ParsePlans schema_version=%q (want %q): %w",
			env.SchemaVersion, ScenePlanSchemaVersion, ErrPlanSchemaDrift)
	}
	if env.ProjectID == "" {
		return "", nil, ErrPlanSchemaDrift
	}
	out := make([]SceneVisualPlan, 0, len(env.Plans))
	for i, dto := range env.Plans {
		plan, err := planFromDTO(dto)
		if err != nil {
			return env.ProjectID, nil, fmt.Errorf(
				"mediamemory: ParsePlans plan[%d] scene=%q: %w", i, dto.SceneID, err)
		}
		out = append(out, plan)
	}
	return env.ProjectID, out, nil
}

// dtoFromPlan projects one canonical plan to its wire DTO.
// godlike/06 SSOT: a canonical-but-unknown layout in the input
// is passed through as the literal string (the renderer's
// IsKnownLayout will reject), so the wire shape stays
// faithful for diagnostic purposes.
func dtoFromPlan(p SceneVisualPlan) (PlanDTO, error) {
	layers := make([]LayerDTO, 0, len(p.Layers))
	for _, l := range p.Layers {
		layers = append(layers, LayerDTO{
			Slot:           string(l.Slot),
			AssetID:        l.AssetID,
			BindingID:      l.BindingID,
			StartMs:        l.StartMs,
			EndMs:          l.EndMs,
			Layout:         l.Layout,
			CandidateScore: l.CandidateScore,
			Provider:       l.Provider,
		})
	}
	return PlanDTO{
		ProjectID:  p.ProjectID,
		SceneID:    p.SceneID,
		Text:       p.Text,
		Language:   p.Language,
		DurationMs: p.DurationMs,
		Layers:     layers,
		Source:     p.Source,
	}, nil
}

// planFromDTO projects the wire DTO back into the canonical
// plan. godlike/06 SSOT (closed-set validation): an unknown
// Layout is REJECTED here (not silently mapped) so a drift on
// either side surfaces at the parse boundary.
func planFromDTO(dto PlanDTO) (SceneVisualPlan, error) {
	layers := make([]Layer, 0, len(dto.Layers))
	for _, l := range dto.Layers {
		slot := SlotKind(l.Slot)
		if !IsKnownSlotKind(slot) {
			return SceneVisualPlan{}, fmt.Errorf(
				"mediamemory: planFromDTO unknown slot=%q (closed-set): %w",
				l.Slot, ErrInvalidSlotKind)
		}
		if !IsKnownLayout(LayoutKind(l.Layout)) {
			return SceneVisualPlan{}, fmt.Errorf(
				"mediamemory: planFromDTO unknown layout=%q (closed-set)", l.Layout)
		}
		layers = append(layers, Layer{
			Slot:           slot,
			AssetID:        l.AssetID,
			BindingID:      l.BindingID,
			StartMs:        l.StartMs,
			EndMs:          l.EndMs,
			Layout:         l.Layout,
			CandidateScore: l.CandidateScore,
			Provider:       l.Provider,
		})
	}
	return SceneVisualPlan{
		ProjectID:  dto.ProjectID,
		SceneID:    dto.SceneID,
		Text:       dto.Text,
		Language:   dto.Language,
		DurationMs: dto.DurationMs,
		Layers:     layers,
		Source:     dto.Source,
	}, nil
}

// ErrPlanSchemaDrift is the canonical sentinel for a wire
// envelope whose schema_version is missing or unknown. godlike/07
// NO-FAKE-AVAILABILITY: never silently zero-parsed — the caller
// MUST branch on errors.Is to decide whether to upgrade.
var ErrPlanSchemaDrift = errors.New(
	"mediamemory: scene-visual-plan wire envelope schema_version drift (render this project with a matching renderer)",
)
