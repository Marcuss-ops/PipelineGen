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
// (PERSON / ORGANIZATION / LOCATION / CONCEPT) — the kinds that resolve from
// the EntityTimeline; a bound image is promoted to the image-only capability
// later in the plan projection.
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
// EntityMediaResolver and promotes the item to the image-only entity
// capability when a verified asset exists. The name remains available in the
// identity/provenance fields, but it is no longer rendered as a second lower
// third below the portrait. An entity without an indexed asset is returned
// unchanged (text-only card).
func attachEntityCardAsset(item capabilityoverlay.OverlayItem, media *capabilityentities.EntityMediaResolver, canonicalByStable map[string]string, planID string) capabilityoverlay.OverlayItem {
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
	// A resolved portrait is a distinct visual capability: use the official
	// image preset/motion path and do not compile the text-card layer as well.
	item.Kind = string(capabilityoverlay.KindEntityImage)
	item.TemplateID = "image_popup"
	item.PresetID = capabilityoverlay.SelectEntityImagePreset(planID, item.SceneID, item.ID)
	item.ImagePresetID = ""
	item.Text = ""
	item.Params = nil
	if item.EntityRef != nil {
		item.EntityRef.CanonicalEntityID = canonical
	}
	return item
}

// capEntityImageOverlays keeps at most max distinct identity-image overlays in
// one run. Items are already in canonical timeline order, so retaining the
// first occurrence is deterministic and preserves the earliest spoken
// identities. Later occurrences of a retained identity do not consume a
// second image slot.
func capEntityImageOverlays(items []capabilityoverlay.OverlayItem, max int) []capabilityoverlay.OverlayItem {
	if max <= 0 || len(items) == 0 {
		return items
	}
	seen := make(map[string]struct{}, max)
	out := make([]capabilityoverlay.OverlayItem, 0, len(items))
	for _, item := range items {
		if item.Kind != string(capabilityoverlay.KindEntityImage) {
			out = append(out, item)
			continue
		}
		// EntityID is occurrence-scoped in some planner paths. For the
		// run-level image budget the identity must be semantic, otherwise the
		// same person mentioned in multiple scenes consumes multiple image
		// slots and the same portrait is rendered repeatedly.
		identity := ""
		if item.EntityRef != nil {
			identity = strings.TrimSpace(item.EntityRef.CanonicalEntityID)
			if identity == "" {
				identity = strings.TrimSpace(item.EntityRef.EntityID)
			}
		}
		if identity == "" && len(item.AssetRefs) > 0 {
			identity = strings.TrimSpace(item.AssetRefs[0].SHA256)
		}
		if identity == "" {
			identity = strings.TrimSpace(item.EntityID)
		}
		if identity == "" {
			identity = strings.TrimSpace(item.ID)
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		if len(seen) >= max {
			continue
		}
		seen[identity] = struct{}{}
		out = append(out, item)
	}
	return out
}

// imageCandidate projects an entity image binding onto the planner's
// ImageCandidate, anchored at the certified occurrence start. Entity images
// use the same fixed five-second display window as PERSON/ORG/GPE image cards;
// the spoken occurrence remains the timing authority for when the animation
// enters, but a short spoken name must not collapse the image to a sub-second
// flash. The direct
// PreviewURL (when present) is preferred over the Drive view-page link so
// the compiled layer references a fetchable image. The binding's verified
// content address (SHA256) is carried through so the planner's asset ref and
// the queue manifest stay content-addressed (a missing hash would silently
// drop the asset from the render manifest).
func imageCandidate(binding *scriptpkg.EntityImageBinding, occ *capabilityentities.EntityOccurrence, score float64) capabilityoverlay.ImageCandidate {
	startUS := (occ.AudioStartUS / 1000) * 1000
	durationUS := capabilityentities.MinEntityOverlayDurationUS
	return capabilityoverlay.ImageCandidate{
		AssetID:    binding.SHA256,
		URL:        entityImageURL(binding),
		SHA256:     binding.SHA256,
		MediaType:  "image",
		StartMs:    startUS / 1000,
		EndMs:      startUS/1000 + durationUS/1000,
		StartUS:    startUS,
		DurationUS: durationUS,
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
