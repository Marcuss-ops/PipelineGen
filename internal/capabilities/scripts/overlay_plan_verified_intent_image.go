package scriptgeneration

import (
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

// appendVerifiedIntentImages recovers a verified entity image that the
// editorial budget dropped with its scene. A scene can be cut after the
// resolver already verified a Drive-backed asset (the ANSA aftermath case);
// silently dropping that asset loses a promised subject even though the run
// has an image slot left. The fallback is deliberately narrow: only an
// already-verified entity intent of a place/organization, only when its
// content address is absent from the plan, and only up to the run image cap.
// It never searches or invents an asset.
func appendVerifiedIntentImages(items []capmodel.OverlayItem, intents []capmodel.OverlayIntent, timeline *capabilityentities.EntityTimeline, planID string, maxImages int) []capmodel.OverlayItem {
	if timeline == nil || len(intents) == 0 || maxImages <= 0 {
		return items
	}
	used := make(map[string]struct{})
	imageItems := 0
	for _, item := range items {
		if strings.Contains(strings.ToLower(item.Kind), "image") {
			imageItems++
		}
		for _, ref := range item.AssetRefs {
			if sha := strings.ToLower(strings.TrimSpace(ref.SHA256)); sha != "" {
				used[sha] = struct{}{}
			}
		}
		for _, segment := range chainSegments(item) {
			assets, _ := segment["assets"].([]any)
			for _, raw := range assets {
				if asset, ok := raw.(map[string]any); ok {
					if sha, _ := asset["sha256"].(string); sha != "" {
						used[strings.ToLower(strings.TrimSpace(sha))] = struct{}{}
					}
				}
			}
		}
	}
	for _, intent := range intents {
		if imageItems >= maxImages {
			break
		}
		entityType := strings.ToUpper(strings.TrimSpace(intent.Entity.Type))
		if entityType != "ORG" && entityType != "ORGANIZATION" && entityType != "GPE" && entityType != "LOCATION" {
			continue
		}
		ref, ok := verifiedIntentAsset(intent)
		if !ok {
			continue
		}
		sha := strings.ToLower(strings.TrimSpace(ref.SHA256))
		if sha == "" {
			continue
		}
		if _, exists := used[sha]; exists {
			continue
		}
		occ, ok := intentOccurrence(timeline, intent)
		if !ok {
			continue
		}
		name := strings.TrimSpace(intent.Entity.CanonicalName)
		if name == "" {
			name = strings.TrimSpace(occ.Name)
		}
		item := capmodel.OverlayItem{
			ID:            "image-focus-reveal__" + capabilityentities.SafeEntityID(name) + "__entity_image__01",
			SceneID:       occ.SceneID,
			Kind:          "organization",
			TemplateID:    "org_default",
			Text:          name,
			EntityCaption: name,
			EntityID:      occ.EntityID,
			StartUS:       occ.AudioStartUS,
			DurationUS:    capabilityentities.EntitySpokenWindowDuration(occ.AudioStartUS, occ.AudioEndUS),
			StartMs:       occ.AudioStartUS / 1000,
			EndMs:         (occ.AudioEndUS + 999) / 1000,
			EntityRef: &capmodel.OverlayEntityRef{
				EntityID: occ.EntityID, Type: occ.Type, Name: name,
				SurfaceText: occ.Name, CanonicalEntityID: intent.Entity.CanonicalEntityID,
			},
		}
		if item.DurationUS <= 0 || item.EndMs <= item.StartMs {
			continue
		}
		item = promoteEntityCardToImage(item, capabilityentities.ResolvedAssetRef{
			AssetID: ref.AssetID, URL: ref.URL, SHA256: ref.SHA256, MediaType: "image/jpeg",
		}, intent.Entity.CanonicalEntityID, planID)
		item.CaptionLayout = "below"
		item.CaptionFontFamily = playfairRuntimeFamily
		item.CaptionColor = presentationTextFill
		items = append(items, item)
		used[sha] = struct{}{}
		imageItems++
	}
	return items
}

func verifiedIntentAsset(intent capmodel.OverlayIntent) (capmodel.OverlayAssetRef, bool) {
	refs := intent.Payload.AssetRefs
	if len(refs) == 0 {
		refs = intent.AssetRefs
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref.URL) != "" && len(strings.TrimSpace(ref.SHA256)) == 64 {
			return ref, true
		}
	}
	return capmodel.OverlayAssetRef{}, false
}

func intentOccurrence(timeline *capabilityentities.EntityTimeline, intent capmodel.OverlayIntent) (capabilityentities.EntityOccurrence, bool) {
	canonical := strings.TrimSpace(intent.Entity.CanonicalEntityID)
	name := strings.ToLower(strings.TrimSpace(intent.Entity.CanonicalName))
	for _, scene := range timeline.Scenes {
		if strings.TrimSpace(intent.SceneID) != "" && strings.TrimSpace(scene.SceneID) != strings.TrimSpace(intent.SceneID) {
			continue
		}
		for _, occurrence := range scene.Entities {
			if canonical != "" && strings.EqualFold(strings.TrimSpace(occurrence.EntityID), canonical) {
				return occurrence, true
			}
			if name != "" && strings.EqualFold(strings.TrimSpace(occurrence.Name), name) {
				return occurrence, true
			}
		}
	}
	return capabilityentities.EntityOccurrence{}, false
}
