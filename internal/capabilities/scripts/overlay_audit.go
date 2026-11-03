package scriptgeneration

import (
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// OverlayAudit is a compact user-facing projection of the semantic source
// entities and the overlays selected for rendering. It distinguishes what was
// detected from what survived planning, and describes phrase length and image
// or map payloads explicitly.
type OverlayAudit struct {
	SchemaVersion       string               `json:"schema_version"`
	DetectedEntityCount int                  `json:"detected_entity_count"`
	UniqueEntityCount   int                  `json:"unique_entity_count"`
	DetectedByType      map[string]int       `json:"detected_by_type"`
	DetectedEntities    []OverlayAuditEntity `json:"detected_entities,omitempty"`
	PlannedOverlayCount int                  `json:"planned_overlay_count"`
	PlannedByKind       map[string]int       `json:"planned_by_kind"`
	PlannedOverlays     []OverlayAuditItem   `json:"planned_overlays,omitempty"`
	Intake              *OverlayAuditIntake  `json:"intake,omitempty"`
}

// OverlayAuditIntake exposes admission limits and drop reasons beside the final plan.
type OverlayAuditIntake struct {
	CandidateCount   int            `json:"candidate_count"`
	CandidatesByKind map[string]int `json:"candidates_by_kind,omitempty"`
	DroppedByReason  map[string]int `json:"dropped_by_reason,omitempty"`
	PhraseBudget     int            `json:"phrase_budget"`
	ImageBudget      int            `json:"image_budget"`
	MapBudget        int            `json:"map_budget"`
}

type OverlayAuditEntity struct {
	ID               string  `json:"id"`
	OccurrenceID     string  `json:"occurrence_id,omitempty"`
	Type             string  `json:"type"`
	Text             string  `json:"text"`
	CanonicalText    string  `json:"canonical_text,omitempty"`
	SceneID          string  `json:"scene_id"`
	Confidence       float64 `json:"confidence"`
	SelectedForVideo bool    `json:"selected_for_video"`
	HasVerifiedImage bool    `json:"has_verified_image"`
}

type OverlayAuditItem struct {
	ID         string   `json:"id"`
	SceneID    string   `json:"scene_id,omitempty"`
	Kind       string   `json:"kind"`
	Class      string   `json:"class"`
	Entity     string   `json:"entity,omitempty"`
	EntityType string   `json:"entity_type,omitempty"`
	Text       string   `json:"text,omitempty"`
	WordCount  int      `json:"word_count,omitempty"`
	AssetIDs   []string `json:"asset_ids,omitempty"`
	ImageCount int      `json:"image_count,omitempty"`
	HasMap     bool     `json:"has_map,omitempty"`
	StartMS    int64    `json:"start_ms"`
	EndMS      int64    `json:"end_ms"`
}

func buildOverlayAudit(bundle *capabilityoverlay.SemanticRenderBundleV1, plan *capabilityoverlay.OverlayPlan) *OverlayAudit {
	if bundle == nil && plan == nil {
		return nil
	}
	audit := &OverlayAudit{
		SchemaVersion:  "pipelinegen.overlay-audit.v1",
		DetectedByType: make(map[string]int),
		PlannedByKind:  make(map[string]int),
	}
	selected := make(map[string]bool)
	selectedSurface := make(map[string]bool)
	selectedImageEntity := make(map[string]bool)
	if plan != nil {
		if intake := plan.Intake; intake != nil {
			audit.Intake = &OverlayAuditIntake{
				CandidateCount:   intake.Candidates,
				CandidatesByKind: intake.CandidatesByKind,
				DroppedByReason:  intake.DroppedByReason,
				PhraseBudget:     intake.Budget.PhraseOverlays,
				ImageBudget:      intake.Budget.ImageOverlays,
				MapBudget:        intake.Budget.MapOverlays,
			}
		}
		audit.PlannedOverlays = make([]OverlayAuditItem, 0, len(plan.Items))
		for _, item := range plan.Items {
			kind := strings.ToLower(strings.TrimSpace(item.Kind))
			if kind == "" {
				kind = strings.ToLower(strings.TrimSpace(item.TemplateID))
			}
			class, words := overlayAuditClass(kind, item.Text)
			entry := OverlayAuditItem{
				ID: item.ID, SceneID: item.SceneID, Kind: kind, Class: class,
				Text: item.Text, WordCount: words, StartMS: item.StartMs, EndMS: item.EndMs,
				HasMap: item.Map != nil || strings.Contains(kind, "map"),
			}
			if item.EntityRef != nil {
				entry.Entity = item.EntityRef.SurfaceText
				if entry.Entity == "" {
					entry.Entity = item.EntityRef.Name
				}
				entry.EntityType = item.EntityRef.Type
				key := item.EntityRef.EntityID
				if key != "" {
					selected[key] = true
				}
				for _, surface := range []string{item.EntityRef.SurfaceText, item.EntityRef.Name} {
					if normalizedAuditSurface(surface) != "" {
						selectedSurface[normalizedAuditSurface(surface)] = true
					}
				}
			}
			for _, asset := range item.AssetRefs {
				if asset.AssetID == "" {
					continue
				}
				entry.AssetIDs = append(entry.AssetIDs, asset.AssetID)
			}
			entry.ImageCount = len(item.AssetRefs) + len(item.ImageLayers)
			if entry.ImageCount > 0 || strings.Contains(kind, "image") || strings.Contains(kind, "product") || strings.Contains(kind, "logo") {
				if strings.Contains(kind, "entity") {
					entry.Class = "entity_with_image"
				} else {
					entry.Class = "image"
				}
			}
			if entry.ImageCount > 0 && item.EntityRef != nil {
				if item.EntityRef.EntityID != "" {
					selectedImageEntity[item.EntityRef.EntityID] = true
				}
				for _, surface := range []string{item.EntityRef.SurfaceText, item.EntityRef.Name} {
					if normalizedAuditSurface(surface) != "" {
						selectedImageEntity[normalizedAuditSurface(surface)] = true
					}
				}
			}
			if entry.HasMap {
				entry.Class = "map"
			}
			audit.PlannedOverlays = append(audit.PlannedOverlays, entry)
			audit.PlannedByKind[kind]++
		}
	}
	if bundle != nil {
		assetByEntity := make(map[string]bool, len(bundle.Assets))
		uniqueEntities := make(map[string]struct{}, len(bundle.Entities))
		for _, asset := range bundle.Assets {
			assetByEntity[asset.EntityID] = asset.Verified
		}
		audit.DetectedEntities = make([]OverlayAuditEntity, 0, len(bundle.Entities))
		for _, entity := range bundle.Entities {
			entityType := strings.ToUpper(strings.TrimSpace(entity.Type))
			audit.DetectedByType[entityType]++
			uniqueID := entity.EntityID
			if uniqueID == "" {
				uniqueID = entityType + ":" + normalizedAuditSurface(entity.CanonicalText)
			}
			if uniqueID != "" {
				uniqueEntities[uniqueID] = struct{}{}
			}
			key := entity.OccurrenceID
			if key == "" {
				key = entity.EntityID
			}
			hasImage := assetByEntity[entity.EntityID] || selectedImageEntity[entity.EntityID] ||
				selectedImageEntity[normalizedAuditSurface(entity.Text)] || selectedImageEntity[normalizedAuditSurface(entity.CanonicalText)]
			audit.DetectedEntities = append(audit.DetectedEntities, OverlayAuditEntity{
				ID: entity.EntityID, OccurrenceID: entity.OccurrenceID, Type: entityType,
				Text: entity.Text, CanonicalText: entity.CanonicalText, SceneID: entity.SceneID,
				Confidence: entity.Confidence, SelectedForVideo: selected[key] ||
					selectedSurface[normalizedAuditSurface(entity.Text)] || selectedSurface[normalizedAuditSurface(entity.CanonicalText)],
				HasVerifiedImage: hasImage,
			})
		}
		audit.DetectedEntityCount = len(bundle.Entities)
		audit.UniqueEntityCount = len(uniqueEntities)
	}
	audit.PlannedOverlayCount = len(audit.PlannedOverlays)
	return audit
}

func overlayAuditClass(kind, text string) (string, int) {
	words := countOverlayWords(text)
	switch {
	case kind == "map" || strings.Contains(kind, "map_chain"):
		return "map", words
	case strings.Contains(kind, "image") || kind == "product" || kind == "logo" || kind == "entity_group":
		return "image", words
	case kind == "important_phrase" || kind == "text_phrase" || strings.Contains(kind, "phrase_chain"):
		if words >= 2 && words <= 5 {
			return "short_phrase", words
		}
		if words < 2 {
			return "phrase_under_minimum", words
		}
		return "long_phrase", words
	case kind == "entity_card" || kind == "person" || kind == "organization" || kind == "location" || kind == "concept":
		return "entity_text", words
	case kind == "number" || kind == "timeline_date":
		return "date_or_number", words
	default:
		return "other", words
	}
}

func countOverlayWords(value string) int {
	return len(strings.Fields(strings.TrimSpace(value)))
}

func normalizedAuditSurface(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}
