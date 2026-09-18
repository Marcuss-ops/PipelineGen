package scriptgeneration

import (
	"fmt"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BuildSemanticRenderBundleFromResult projects the already-certified result
// surfaces into the single cross-stage bundle. It intentionally consumes the
// existing EntityTimeline, OverlayIntents and OverlayPlan; it never reruns
// extraction, asset search or timing.
func BuildSemanticRenderBundleFromResult(result *GenerateResult, language Language, runID, videoID string) (*capabilityoverlay.SemanticRenderBundleV1, error) {
	if result == nil || result.EntityTimeline == nil {
		return nil, nil
	}
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("semantic bundle: run id is required")
	}
	var source strings.Builder
	offsets := make(map[string]int, len(result.Scenes))
	for _, candidate := range result.Scenes {
		text := candidate.Text[language]
		if strings.TrimSpace(text) == "" {
			continue
		}
		if source.Len() > 0 {
			source.WriteByte('\n')
		}
		offsets[candidate.ID] = source.Len()
		source.WriteString(text)
	}
	scene := capabilityoverlay.NewSceneIR(runID, 0, source.String(), "", capabilityoverlay.SegmentSemanticProfile{})
	if scene.SourceText == "" {
		return nil, fmt.Errorf("semantic bundle: no source scene for %q", language)
	}
	bundle := &capabilityoverlay.SemanticRenderBundleV1{
		Version:        capabilityoverlay.SemanticRenderBundleVersion,
		RunID:          runID,
		Scene:          scene,
		OverlayIntents: append([]capabilityoverlay.OverlayIntent(nil), result.OverlayIntents...),
	}
	for _, timelineScene := range result.EntityTimeline.Scenes {
		for _, occurrence := range timelineScene.Entities {
			text := strings.TrimSpace(occurrence.Name)
			if text == "" {
				continue
			}
			entityID := occurrence.EntityID
			start := -1
			for _, candidate := range result.Scenes {
				if candidate.ID == occurrence.SceneID {
					local := runeSpanBytes(candidate.Text[language], occurrence.TextStart, occurrence.TextEnd)
					if local >= 0 && (local+len(text) > len(candidate.Text[language]) || candidate.Text[language][local:local+len(text)] != text) {
						local = -1
					}
					if local < 0 {
						local = strings.Index(candidate.Text[language], text)
					}
					if local >= 0 {
						start = offsets[candidate.ID] + local
					}
					break
				}
			}
			if start < 0 {
				continue
			}
			bundle.Entities = append(bundle.Entities, capabilityoverlay.ResolvedEntity{
				EntityID: entityID, Type: occurrence.Type, Text: text, CanonicalText: text,
				Evidence: text, Start: start, End: start + len(text), Confidence: occurrence.Confidence,
				SceneID: occurrence.SceneID,
			})
			preset := bundlePresetForType(occurrence.Type)
			bundle.Timeline = append(bundle.Timeline, capabilityoverlay.TimelineEvent{
				EntityID: entityID, StartMs: occurrence.AudioStartUS / 1000,
				EndMs: (occurrence.AudioEndUS + 999) / 1000, PresetID: preset,
			})
		}
	}
	// Assets are read from the existing annotation bindings. A binding is
	// marked verified only when it carries the full content-addressed tuple.
	//
	// The asset↔entity join is by CONTENT-ADDRESSED IDENTITY, never by display
	// text: bundle.Entities are keyed by the stable entity id the canonical
	// entity timeline stamped (StableEntityID(type, canonical name)), and the
	// annotation is projected through the same single owner. Comparing the two
	// canonical names byte-for-byte used to be the join, which silently dropped
	// an asset whenever the surfaces differed (a possessive, a normalized
	// spelling, a localized name) and downgraded the card to text-only.
	bundleEntityIDs := make(map[string]struct{}, len(bundle.Entities))
	for _, resolved := range bundle.Entities {
		bundleEntityIDs[resolved.EntityID] = struct{}{}
	}
	for _, s := range result.Scenes {
		if s.Annotations == nil {
			continue
		}
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), s.Annotations.PrimaryEntities...), s.Annotations.SecondaryEntities...) {
			// An annotation may legally remain text-only while an asset is
			// pending. Never emit a half-bound BoundAsset: only a committed,
			// content-addressed binding crosses the semantic contract.
			//
			// The identity comes from the canonical kernel type, so the digest is
			// validated AND spelled by its one owner instead of by this call site
			// (the local validator this replaced accepted a differently-cased
			// digest but copied it through uncanonicalised, which is how one set of
			// bytes ends up with two join keys).
			if entity.Image == nil {
				continue
			}
			identity := entity.Image.Ref()
			if identity.AssetID == "" || !identity.IsCanonicalDigest() {
				continue
			}
			url := entity.Image.PreviewURL
			if url == "" {
				url = entity.Image.DriveLink
			}
			// The asset's join key is the entity's StableEntityID as projected
			// into bundle.Entities (the EntityTimeline occurrence id), derived
			// here through the ONE identity owner (annotationStableEntityID) so
			// the two surfaces can never disagree. The stamped CanonicalEntityID
			// ("person:slug") stays provenance: bundle.Entities live in the
			// StableEntityID space ("ent_<hex>"), and Validate fails closed on a
			// dangling asset join.
			entityID := annotationStableEntityID(entity)
			if _, ok := bundleEntityIDs[entityID]; !ok {
				// The entity has no certified timeline occurrence (not
				// grounded or not spoken verbatim), so no bundle entity can
				// claim this asset — it cannot be part of the render contract.
				continue
			}
			bundle.Assets = append(bundle.Assets, capabilityoverlay.BoundAsset{
				EntityID: entityID,
				AssetID:  identity.AssetID, ContentHash: identity.SHA256,
				DriveFileID: entity.Image.DriveFileID, SourceURL: url,
				Verified: url != "" || entity.Image.DriveFileID != "",
			})
		}
	}
	if result.OverlayPlan != nil {
		bundle.OverlayPlanHash = result.OverlayPlan.Fingerprint
	}
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	_ = videoID // retained in the caller's OverlayPlan identity.
	return bundle, nil
}

// runeSpanBytes converts the entity contract's Unicode-rune span into the
// byte offsets required by Go slicing. It prevents non-ASCII source text from
// passing the wrong evidence into the bundle.
func runeSpanBytes(source string, startRune, endRune int) int {
	if startRune < 0 || endRune < startRune {
		return -1
	}
	runes := []rune(source)
	if endRune > len(runes) {
		return -1
	}
	return len(string(runes[:startRune]))
}

func bundlePresetForType(entityType string) string {
	role := strings.ToUpper(strings.TrimSpace(entityType))
	switch role {
	case "GPE", "PLACE", "CITY", "COUNTRY":
		role = "LOCATION"
	case "ORG":
		role = "ORGANIZATION"
	case "LOGO", "PRODUCT":
		role = "IMAGE_ENTITY"
	case "CONCEPT", "VISUAL_CONCEPT":
		role = "IMPORTANT_PHRASE"
	}
	if preset, ok := capabilityoverlay.DefaultSemanticOverlayResolver.PresetFor(role); ok {
		return preset
	}
	return string(capabilityoverlay.PresetModernPhrase)
}
