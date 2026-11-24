package overlayplan

import (
	"encoding/json"
	"strings"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// BuildOverlayAuditWithLineage is the ONE entry point: the intent lineage is
// computed by the same pass that freezes the intents, so the audit describes
// the plan that was actually rendered. A second, lineage-less entry point
// existed briefly and was removed: it produced an audit without
// overlay_timing, which reads as "every intent was fine" instead of "the
// lineage was not measured". A caller that genuinely has no lineage passes
// nil, and the field is then omitted rather than fabricated.
func BuildOverlayAuditWithLineage(bundle *capabilityoverlay.SemanticRenderBundleV1, plan *capmodel.OverlayPlan, lineage *OverlayIntentLineage) *contract.OverlayAudit {
	if bundle == nil && plan == nil {
		return nil
	}
	audit := &contract.OverlayAudit{
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
	imageBindings := make([]contract.OverlayAuditImage, 0)
	if plan != nil {
		if encoded, err := json.Marshal(plan); err == nil {
			audit.PlanSHA256 = digest.SHA256Bytes(encoded)
		}
		audit.PlanID = plan.PlanID
		audit.VideoID = plan.VideoID
		if intake := plan.Intake; intake != nil {
			audit.Intake = &contract.OverlayAuditIntake{
				CandidateCount:   intake.Candidates,
				CandidatesByKind: intake.CandidatesByKind,
				DroppedByReason:  intake.DroppedByReason,
				PhraseBudget:     intake.Budget.PhraseOverlays,
				ImageBudget:      intake.Budget.ImageOverlays,
				MapBudget:        intake.Budget.MapOverlays,
			}
		}
		audit.PlannedOverlays = make([]contract.OverlayAuditItem, 0, len(plan.Items))
		for _, item := range plan.Items {
			kind := strings.ToLower(strings.TrimSpace(item.Kind))
			if kind == "" {
				kind = strings.ToLower(strings.TrimSpace(item.TemplateID))
			}
			class, words := overlayAuditClass(kind, item.Text)
			entry := contract.OverlayAuditItem{
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
			if entry.ImageCount > 0 && overlayAuditEntityKind(kind) {
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
				surfaces := []string{item.Text}
				entityID := strings.TrimSpace(item.EntityID)
				entityType := ""
				if item.EntityRef != nil {
					surfaces = append(surfaces, item.EntityRef.SurfaceText, item.EntityRef.Name)
					entityID = firstNonEmptyAuditValue(item.EntityRef.CanonicalEntityID, item.EntityRef.EntityID, item.EntityID)
					entityType = item.EntityRef.Type
				}
				for _, surface := range surfaces {
					if normalizedAuditSurface(surface) != "" {
						selectedImageEntity[normalizedAuditSurface(surface)] = true
					}
				}
				imageBindings = append(imageBindings, contract.OverlayAuditImage{
					EntityID:   entityID,
					Entity:     firstNonEmptyAuditValue(entry.Entity, item.Text),
					EntityType: entityType,
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
		audit.DetectedEntities = make([]contract.OverlayAuditEntity, 0, len(bundle.Entities))
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
			audit.DetectedEntities = append(audit.DetectedEntities, contract.OverlayAuditEntity{
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
	audit.EntityConcordance = BuildOverlayAuditEntityConcordance(audit)
	return audit
}

// BuildOverlayAuditEntityConcordance projects the one contradiction TICKET-49
// measured: the image contract reported a planned portrait while the intent
// lineage reported the same identity as never planned. Both sides join on the
// entity id / canonical id first and fall back to the grounded surface only for
// a legacy item that carries no identity.
func BuildOverlayAuditEntityConcordance(audit *contract.OverlayAudit) *contract.OverlayAuditEntityConcordance {
	if audit == nil || audit.OverlayTiming == nil {
		return nil
	}
	out := &contract.OverlayAuditEntityConcordance{}
	unresolved := make([]contract.OverlayAuditTimingItem, 0, len(audit.OverlayTiming.UnresolvedItems))
	for _, item := range audit.OverlayTiming.UnresolvedItems {
		switch item.Reason {
		case OverlayIntentReasonEntityNotPlanned, OverlayIntentReasonTemplateNotMaterialized:
			unresolved = append(unresolved, item)
		}
	}
	seen := make(map[string]struct{}, len(audit.DetectedEntities))
	for _, entity := range audit.DetectedEntities {
		if !strings.EqualFold(strings.TrimSpace(entity.ImageStatus), "planned") {
			continue
		}
		key := auditEntityImageGroupKey(entity)
		if key != "" {
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
		}
		out.Checked++
		for _, item := range unresolved {
			if !auditIntentEntityMatches(item, entity) {
				continue
			}
			out.Mismatched++
			if len(out.Mismatches) < maxAuditMissingEntities {
				out.Mismatches = append(out.Mismatches, contract.OverlayAuditEntityConflict{
					EntityID: entity.ID,
					Entity:   firstNonEmptyAuditValue(entity.Text, entity.CanonicalText),
					SceneID:  entity.SceneID,
					Reason:   item.Reason,
				})
			}
			break
		}
	}
	return out
}

// auditIntentEntityMatches joins an unresolved intent to a detected entity by
// identity first, then by the grounded surface for legacy items without ids.
func auditIntentEntityMatches(item contract.OverlayAuditTimingItem, entity contract.OverlayAuditEntity) bool {
	entityKeys := map[string]struct{}{}
	for _, key := range []string{entity.ID, entity.CanonicalEntityID, entity.OccurrenceID} {
		if key = strings.TrimSpace(key); key != "" {
			entityKeys[key] = struct{}{}
		}
	}
	for _, key := range []string{item.EntityID, item.CanonicalEntityID} {
		if key = strings.TrimSpace(key); key != "" {
			if _, ok := entityKeys[key]; ok {
				return true
			}
		}
	}
	surface := normalizedAuditSurface(item.Entity)
	if surface == "" {
		return false
	}
	return surface == normalizedAuditSurface(entity.Text) ||
		surface == normalizedAuditSurface(entity.CanonicalText)
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

// overlayAuditEntityKind keeps image-binding coverage on entity surfaces even
// when the plan item has no EntityRef (the entity timeline cards use only
// EntityID + Text).
func overlayAuditEntityKind(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "entity_card", "entity_image", "location", "organization", "concept", "lower_third":
		return true
	default:
		return strings.Contains(kind, "entity")
	}
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
