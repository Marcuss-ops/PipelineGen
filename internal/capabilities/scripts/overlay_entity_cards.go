// Package scriptgeneration — overlay_entity_cards.go: the entity-card half
// of the overlay plan derivation (PERSON / ORGANIZATION / LOCATION /
// CONCEPT cards resolved from the certified EntityTimeline).
//
// Extracted 2026-09-12 from overlay_plan.go to keep both halves under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"strings"

	"go.uber.org/zap"

	logger "github.com/Marcuss-ops/PipelineGen/internal/platform/logging"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// entityCardTemplate reports whether a resolver template is an entity card
// (as opposed to a planner-owned overlay template).
func entityCardTemplate(templateID string) bool {
	switch templateID {
	case "person_default", "org_default", "gpe_default", "concept_default":
		return true
	}
	return false
}

// entityCardKind reports whether an overlay kind is an entity-card kind
// (PERSON / ORGANIZATION / LOCATION / CONCEPT) — the kinds whose image is
// carried BY the card instead of a generic IMAGE_OVERLAY.
func entityCardKind(kind capabilityoverlay.OverlayKind) bool {
	switch kind {
	case capabilityoverlay.KindEntityCard, capabilityoverlay.KindOrganization, capabilityoverlay.KindLocation, capabilityoverlay.KindConcept:
		return true
	}
	return false
}

// entityCardMediaIndex builds the run-scoped, canonical_entity_id-keyed media
// index from the result's OWN entity-image bindings: every entity-card-kind
// annotation entity with a content-addressed binding (asset id + sha256 +
// url) is indexed so the EntityMediaResolver can pick the best asset for its
// card. The join key is the resolver's CanonicalEntityID when the annotation
// was stamped with one (capabilities/imagesearch), else the deterministic
// derivation from (type, canonical name) — the two agree whenever the
// surface IS the canonical name. Bindings without a content address are
// deliberately NOT indexed: a card asset must be verifiable, never a bare
// reference.
//
// The second return maps each annotated entity's StableEntityID to its
// canonical id, so a resolver card item (keyed by the same StableEntityID)
// can look up the identity to resolve.
func entityCardMediaIndex(result *GenerateResult) (*capabilityentities.EntityMediaResolver, map[string]string) {
	index := capabilityentities.NewEntityMediaIndex()
	media := capabilityentities.NewEntityMediaResolver()
	canonicalByStable := map[string]string{}
	for i := range result.Scenes {
		ann := result.Scenes[i].Annotations
		if ann == nil {
			continue
		}
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
			if !entityCardKind(capabilityoverlay.EntityTypeToKind(entity.Type)) {
				continue
			}
			binding := entity.Image
			if binding == nil || strings.TrimSpace(binding.AssetID) == "" {
				continue
			}
			canonical := strings.TrimSpace(entity.CanonicalEntityID)
			if canonical == "" {
				canonical = capabilityentities.CanonicalEntityID(entity.Type, entity.CanonicalName)
			}
			if canonical == "" {
				continue
			}
			stable := capabilityentities.StableEntityID(entity.Type, entity.CanonicalName)
			canonicalByStable[stable] = canonical
			url := entityImageURL(binding)
			if url == "" || strings.TrimSpace(binding.SHA256) == "" {
				continue
			}
			score := entity.Confidence
			if score <= 0 {
				score = 0.9
			}
			// Fail-open on an invalid record: the card stays text-only rather
			// than failing the whole overlay plan over one unverifiable asset.
			// Fail-open must still be visible: a registry rejecting every
			// binding would silently degrade every card to text-only.
			if err := index.IndexForCanonicalID(canonical, capabilityentities.EntityAsset{
				AssetID: binding.AssetID, AssetType: entityImageAssetType(binding),
				SHA256: binding.SHA256, StorageURL: url,
				QualityScore: score, Source: binding.Source,
			}); err != nil {
				logger.Warn("overlay plan: entity card asset not indexed (card stays text-only)",
					zap.String("entity", entity.CanonicalName),
					zap.String("canonical_entity_id", canonical),
					zap.Error(err),
				)
			}
		}
	}
	media.SetIndex(index)
	return media, canonicalByStable
}

// entityImageAssetType keeps the semantic path extension aligned with the
// verified bytes. Drive download URLs commonly have no suffix, so relying on
// filepath.Ext(URL) would manufacture a misleading .png path for a JPEG.
// The worker still sniffs the materialized bytes and can correct an honest
// provider MIME mismatch before Chronon.
func entityImageAssetType(binding *scriptpkg.EntityImageBinding) string {
	if binding != nil {
		switch strings.ToLower(strings.TrimSpace(strings.SplitN(binding.MediaType, ";", 2)[0])) {
		case "image/png":
			return "photo"
		case "image/jpeg", "image/jpg":
			return "photo_jpeg"
		}
		url := strings.ToLower(strings.TrimSpace(entityImageURL(binding)))
		if strings.HasSuffix(url, ".png") {
			return "photo"
		}
		if strings.HasSuffix(url, ".jpg") || strings.HasSuffix(url, ".jpeg") {
			return "photo_jpeg"
		}
	}
	// Internet image acquisition currently verifies and persists photographs
	// as JPEG in the live catalog. Keep the fallback explicit and deterministic
	// for legacy bindings that predate media_type.
	return "photo_jpeg"
}

// attachEntityCardAsset resolves the card's canonical_entity_id through the
// EntityMediaResolver and attaches the best content-addressed asset to the
// card item (AssetRefs) plus the canonical identity (EntityRef.CanonicalEntityID).
// The input item is never mutated; an entity without an indexed asset, or
// without a known canonical identity, is returned unchanged (text-only card).
func attachEntityCardAsset(item capabilityoverlay.OverlayItem, media *capabilityentities.EntityMediaResolver, canonicalByStable map[string]string) capabilityoverlay.OverlayItem {
	canonical := canonicalByStable[item.EntityID]
	if canonical == "" {
		return item
	}
	ref, err := media.ResolveBest(canonical)
	if err != nil {
		return item
	}
	// A catalog row without a fetchable URL is not renderable by Chronon.
	// Keep the entity card text-only until the asset binding is complete;
	// emitting a hash-only ref would make RenderingGen fail later while
	// materializing an impossible logical path.
	if strings.TrimSpace(ref.URL) == "" || strings.TrimSpace(ref.SHA256) == "" || strings.HasPrefix(strings.TrimSpace(ref.URL), "assets/semantic/") || strings.HasPrefix(strings.TrimSpace(ref.URL), "semantic/") {
		return item
	}
	item.AssetRefs = []capabilityoverlay.OverlayAssetRef{{
		AssetID: ref.SHA256, URL: ref.URL, SHA256: ref.SHA256, MediaType: ref.MediaType,
	}}
	if item.EntityRef != nil {
		item.EntityRef.CanonicalEntityID = canonical
	}
	return item
}

// imageCandidate projects an entity image binding onto the planner's
// ImageCandidate, timed by the certified occurrence window. The direct
// PreviewURL (when present) is preferred over the Drive view-page link so
// the compiled layer references a fetchable image. The binding's verified
// content address (SHA256) is carried through so the planner's asset ref and
// the queue manifest stay content-addressed (a missing hash would silently
// drop the asset from the render manifest).
func imageCandidate(binding *scriptpkg.EntityImageBinding, occ *capabilityentities.EntityOccurrence, score float64) capabilityoverlay.ImageCandidate {
	return capabilityoverlay.ImageCandidate{
		AssetID:    binding.SHA256,
		URL:        entityImageURL(binding),
		SHA256:     binding.SHA256,
		MediaType:  "image",
		StartMs:    occ.AudioStartUS / 1000,
		EndMs:      (occ.AudioEndUS + 999) / 1000,
		StartUS:    occ.AudioStartUS,
		DurationUS: occ.AudioEndUS - occ.AudioStartUS,
		Score:      score,
	}
}

func entityImageURL(binding *scriptpkg.EntityImageBinding) string {
	if binding == nil {
		return ""
	}
	if strings.TrimSpace(binding.PreviewURL) != "" {
		return binding.PreviewURL
	}
	return binding.DriveLink
}
