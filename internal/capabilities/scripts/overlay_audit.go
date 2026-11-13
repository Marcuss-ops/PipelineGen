package scriptgeneration

import (
	"encoding/json"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
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
	ImageBindings       []OverlayAuditImage  `json:"image_bindings,omitempty"`
	Intake              *OverlayAuditIntake  `json:"intake,omitempty"`
	// EntityImages answers the question the r20minuti report could not: the
	// image budget was 3, zero images were planned, and nothing said why. It
	// separates a selected entity that got its image from one that did not,
	// with a reason derived from the plan and the verified-asset bundle.
	EntityImages *OverlayAuditEntityImages `json:"entity_images,omitempty"`
	// OverlayTiming records the intent→plan lineage: how many authored
	// intents were frozen into a timed item and how many stayed PENDING. A
	// PENDING intent is not a render instruction, and hiding it inside a
	// 39-element array made it look like one.
	OverlayTiming *OverlayAuditOverlayTiming `json:"overlay_timing,omitempty"`
	// PlanSHA256 is the canonical digest of the sealed plan. Two runs that
	// claim the same plan identity must publish the same digest; a difference
	// is evidence of non-determinism, not a benchmark result.
	PlanSHA256 string `json:"plan_sha256,omitempty"`
	PlanID     string `json:"plan_id,omitempty"`
	VideoID    string `json:"video_id,omitempty"`
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
	ID                     string   `json:"id"`
	CanonicalEntityID      string   `json:"canonical_entity_id,omitempty"`
	OccurrenceID           string   `json:"occurrence_id,omitempty"`
	Type                   string   `json:"type"`
	Text                   string   `json:"text"`
	CanonicalText          string   `json:"canonical_text,omitempty"`
	SceneID                string   `json:"scene_id"`
	Confidence             float64  `json:"confidence"`
	SelectedForVideo       bool     `json:"selected_for_video"`
	ImageRequired          bool     `json:"image_required"`
	ImageDriveRequired     bool     `json:"image_drive_required,omitempty"`
	ImageAcquisitionStatus string   `json:"image_acquisition_status,omitempty"`
	HasVerifiedImage       bool     `json:"has_verified_image"`
	ImageStatus            string   `json:"image_status,omitempty"`
	ImageAssetIDs          []string `json:"image_asset_ids,omitempty"`
	PlannedKinds           []string `json:"planned_kinds,omitempty"`
	ImageQuery             string   `json:"image_query,omitempty"`
	ImageCandidateID       string   `json:"image_candidate_id,omitempty"`
	ImageSelectionReason   string   `json:"image_selection_reason,omitempty"`
	ImageFailureReason     string   `json:"image_failure_reason,omitempty"`
	ImageSHA256            string   `json:"image_sha256,omitempty"`
	ImageDriveFileID       string   `json:"image_drive_file_id,omitempty"`
	OverlayItemID          string   `json:"overlay_item_id,omitempty"`
}

// OverlayAuditImage makes the image decision auditable at the plan boundary:
// which entity the item names, which bytes it would render, and whether the
// bundle already carried a verified asset. It deliberately does not claim that
// a frame was visible; that is a render-audit fact, not a plan fact.
type OverlayAuditImage struct {
	EntityID    string   `json:"entity_id,omitempty"`
	Entity      string   `json:"entity,omitempty"`
	EntityType  string   `json:"entity_type,omitempty"`
	SceneID     string   `json:"scene_id,omitempty"`
	OverlayID   string   `json:"overlay_id"`
	AssetIDs    []string `json:"asset_ids,omitempty"`
	Status      string   `json:"status"`
	HasVerified bool     `json:"has_verified_asset"`
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

// buildOverlayAuditWithLineage is the ONE entry point: the intent lineage is
// computed by the same pass that freezes the intents, so the audit describes
// the plan that was actually rendered. A second, lineage-less entry point
// existed briefly and was removed: it produced an audit without
// overlay_timing, which reads as "every intent was fine" instead of "the
// lineage was not measured". A caller that genuinely has no lineage passes
// nil, and the field is then omitted rather than fabricated.
func buildOverlayAuditWithLineage(bundle *capabilityoverlay.SemanticRenderBundleV1, plan *capmodel.OverlayPlan, lineage *overlayIntentLineage) *OverlayAudit {
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
	selectedKinds := make(map[string][]string)
	selectedOverlayIDs := make(map[string][]string)
	selectedAssets := make(map[string][]string)
	verifiedAssetIDs := make(map[string]bool)
	plannedEntityImages := overlayPlanEntityImageIndex(plan)
	imageBindings := make([]OverlayAuditImage, 0)
	if plan != nil {
		if encoded, err := json.Marshal(plan); err == nil {
			audit.PlanSHA256 = digest.SHA256Bytes(encoded)
		}
		audit.PlanID = plan.PlanID
		audit.VideoID = plan.VideoID
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
				keys := overlayAuditIdentityKeys(item.EntityID, item.EntityRef)
				for _, key := range keys {
					if key != "" {
						selected[key] = true
						selectedKinds[key] = appendUniqueAuditValue(selectedKinds[key], kind)
						selectedOverlayIDs[key] = appendUniqueAuditValue(selectedOverlayIDs[key], item.ID)
					}
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
				keys := overlayAuditIdentityKeys(item.EntityID, item.EntityRef)
				for _, key := range keys {
					if key == "" {
						continue
					}
					selectedImageEntity[key] = true
					for _, assetID := range entry.AssetIDs {
						selectedAssets[key] = appendUniqueAuditValue(selectedAssets[key], assetID)
					}
				}
				for _, surface := range []string{item.EntityRef.SurfaceText, item.EntityRef.Name} {
					if normalizedAuditSurface(surface) != "" {
						selectedImageEntity[normalizedAuditSurface(surface)] = true
					}
				}
				imageBindings = append(imageBindings, OverlayAuditImage{
					EntityID:   firstNonEmptyAuditValue(item.EntityRef.CanonicalEntityID, item.EntityRef.EntityID, item.EntityID),
					Entity:     firstNonEmptyAuditValue(entry.Entity, item.Text),
					EntityType: item.EntityRef.Type,
					SceneID:    item.SceneID,
					OverlayID:  item.ID,
					AssetIDs:   append([]string(nil), entry.AssetIDs...),
					Status:     "planned",
				})
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
			if asset.Verified && asset.AssetID != "" {
				verifiedAssetIDs[asset.AssetID] = true
			}
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
			surfaceKey := normalizedAuditSurface(entity.Text)
			canonicalKey := normalizedAuditSurface(entity.CanonicalText)
			hasVerifiedImage := assetByEntity[entity.EntityID]
			hasImage := hasVerifiedImage || selectedImageEntity[entity.EntityID] || selectedImageEntity[surfaceKey] || selectedImageEntity[canonicalKey]
			assetIDs := append([]string(nil), selectedAssets[entity.EntityID]...)
			for _, key := range []string{surfaceKey, canonicalKey} {
				assetIDs = appendUniqueAuditValues(assetIDs, selectedAssets[key]...)
			}
			plannedKinds := append([]string(nil), selectedKinds[entity.EntityID]...)
			for _, key := range []string{surfaceKey, canonicalKey} {
				plannedKinds = appendUniqueAuditValues(plannedKinds, selectedKinds[key]...)
			}
			imageStatus := "missing"
			switch {
			case len(assetIDs) > 0:
				imageStatus = "planned"
			case hasVerifiedImage:
				imageStatus = "verified_asset_not_planned"
			case hasImage:
				imageStatus = "planned_without_verified_asset"
			}
			imageFailureReason := entity.ImageFailureReason
			imageDriveFileID := entity.ImageDriveFileID
			imageSHA256 := entity.ImageSHA256
			// A chain item renders its entity images from nested segments. Those
			// bindings are absent from the annotation projection and from the
			// item-level EntityRef, so the audit must take them from the compiled
			// plan itself. Once the plan carries the image, a stale annotation
			// failure reason (or an empty annotation Drive id) is not the truth.
			if planned, ok := overlayPlannedEntityImageFor(plannedEntityImages, entity.SceneID, entity.EntityID); ok {
				imageStatus = "planned"
				imageFailureReason = ""
				imageDriveFileID = planned.DriveFileID
				imageSHA256 = planned.SHA256
				assetIDs = appendUniqueAuditValue(assetIDs, firstNonEmptyAuditValue(planned.AssetID, planned.SHA256))
			}
			audit.DetectedEntities = append(audit.DetectedEntities, OverlayAuditEntity{
				ID: entity.EntityID, OccurrenceID: entity.OccurrenceID, Type: entityType,
				CanonicalEntityID: entity.CanonicalEntityID,
				Text:              entity.Text, CanonicalText: entity.CanonicalText, SceneID: entity.SceneID,
				Confidence: entity.Confidence, SelectedForVideo: selected[key] ||
					selectedSurface[surfaceKey] || selectedSurface[canonicalKey],
				HasVerifiedImage: hasVerifiedImage, ImageStatus: imageStatus,
				ImageAssetIDs: assetIDs, PlannedKinds: plannedKinds,
				ImageQuery: entity.ImageQuery, ImageCandidateID: entity.ImageCandidateID,
				ImageSelectionReason: entity.ImageSelectionReason, ImageFailureReason: imageFailureReason,
				ImageAcquisitionStatus: entity.ImageStatus,
				ImageSHA256:            imageSHA256, ImageDriveFileID: imageDriveFileID,
				OverlayItemID: strings.Join(selectedOverlayIDs[entity.EntityID], ","),
			})
		}
		audit.DetectedEntityCount = len(bundle.Entities)
		audit.UniqueEntityCount = len(uniqueEntities)
	}
	for index := range imageBindings {
		for _, assetID := range imageBindings[index].AssetIDs {
			if verifiedAssetIDs[assetID] {
				imageBindings[index].HasVerified = true
				break
			}
		}
	}
	audit.ImageBindings = imageBindings
	audit.PlannedOverlayCount = len(audit.PlannedOverlays)
	audit.EntityImages = buildOverlayAuditEntityImages(audit, bundle, plan)
	audit.OverlayTiming = buildOverlayAuditOverlayTiming(lineage)
	return audit
}

// overlayAuditIdentityKeys returns the stable keys an audit entry may join on:
// the item-level entity id, the producer-only EntityRef id, and the canonical
// identity the media resolver selected. Keeping all three prevents a plan item
// from looking "missing" merely because one projection used a different id.
func overlayAuditIdentityKeys(entityID string, ref *capmodel.OverlayEntityRef) []string {
	keys := make([]string, 0, 4)
	keys = appendUniqueAuditValue(keys, entityID)
	if ref != nil {
		keys = appendUniqueAuditValue(keys, ref.EntityID)
		keys = appendUniqueAuditValue(keys, ref.CanonicalEntityID)
		// An artifact persisted before the type-alias fold carries "gpe:…"/
		// "organization:…" ids; the folded key lets it join the entity the
		// bundle now names "place:…"/"org:…" instead of reporting a missing
		// image that is actually bound.
		keys = appendUniqueAuditValue(keys, capabilityentities.FoldCanonicalEntityID(ref.CanonicalEntityID))
	}
	return keys
}

func appendUniqueAuditValue(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func appendUniqueAuditValues(values []string, more ...string) []string {
	for _, value := range more {
		values = appendUniqueAuditValue(values, value)
	}
	return values
}

func firstNonEmptyAuditValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
