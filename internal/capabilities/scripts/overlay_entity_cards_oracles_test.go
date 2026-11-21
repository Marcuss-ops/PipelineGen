package scriptgeneration

import (
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func admitIdentityImagesByProductionPlan(items []capmodel.OverlayItem, plan *SemanticProductionPlan) []capmodel.OverlayItem {
	if plan == nil || len(plan.Identity) == 0 || len(items) == 0 {
		return items
	}
	admitted := plan.IdentitiesAdmittedForMedia()
	out := make([]capmodel.OverlayItem, 0, len(items))
	for _, item := range items {
		if len(item.AssetRefs) == 0 {
			out = append(out, item)
			continue
		}
		name := ""
		if item.EntityRef != nil {
			name = strings.TrimSpace(item.EntityRef.Name)
		}
		if name == "" {
			name = strings.TrimSpace(item.EntityCaption)
		}
		if name == "" {
			// The item cannot be attributed to an identity: keep it rather than
			// silently dropping a composition the plan never judged.
			out = append(out, item)
			continue
		}
		if _, ok := admitted[normalizeIdentitySurface(name)]; ok {
			out = append(out, item)
			continue
		}
		// The asset already exists in verified form. The semantic plan's media
		// budget controls acquisition, not whether produced bytes may render.
		if overlayItemHasVerifiedIdentityAsset(item) {
			out = append(out, item)
			continue
		}
		out = append(out, demoteEntityImageToTextCard(item, name))
	}
	return out
}

func overlayItemHasVerifiedIdentityAsset(item capmodel.OverlayItem) bool {
	for _, ref := range item.AssetRefs {
		if overlayLooksLikeSHA256(ref.SHA256) || overlayLooksLikeSHA256(ref.AssetID) {
			return true
		}
	}
	return false
}

func demoteEntityImageToTextCard(item capmodel.OverlayItem, name string) capmodel.OverlayItem {
	item.AssetRefs = nil
	item.ImageLayers = nil
	item.EntityCaption = ""
	item.Text = name
	if item.EntityRef != nil {
		kind := capmodel.EntityTypeToKind(item.EntityRef.Type)
		item.Kind = string(kind)
		if entry, err := capmodel.DefaultChrononOverlayRegistry.Resolve(string(kind)); err == nil {
			item.TemplateID = entry.Template
		}
	}
	return item
}

func imageCandidate(binding *scriptpkg.EntityImageBinding, occ *capabilityentities.EntityOccurrence, score float64) capabilityoverlay.ImageCandidate {
	startUS := (occ.AudioStartUS / 1000) * 1000
	durationUS := capabilityentities.EntitySpokenWindowDuration(occ.AudioStartUS, occ.AudioEndUS)
	return capabilityoverlay.ImageCandidate{
		AssetID:    binding.SHA256,
		URL:        entityImageURL(binding),
		LocalPath:  binding.LocalPath,
		SHA256:     binding.SHA256,
		MediaType:  "image",
		StartMs:    startUS / 1000,
		EndMs:      startUS/1000 + durationUS/1000,
		StartUS:    startUS,
		DurationUS: durationUS,
		Score:      score,
	}
}
