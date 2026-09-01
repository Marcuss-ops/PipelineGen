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
	for _, s := range result.Scenes {
		if s.Annotations == nil {
			continue
		}
		for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), s.Annotations.PrimaryEntities...), s.Annotations.SecondaryEntities...) {
			// An annotation may legally remain text-only while an asset is
			// pending. Never emit a half-bound BoundAsset: only a committed,
			// content-addressed binding crosses the semantic contract.
			if entity.Image == nil || entity.Image.AssetID == "" || !validContentHash(entity.Image.SHA256) {
				continue
			}
			url := entity.Image.PreviewURL
			if url == "" {
				url = entity.Image.DriveLink
			}
			entityID := firstNonEmpty(entity.CanonicalEntityID, entity.ID)
			if entityID == "" {
				for _, resolved := range bundle.Entities {
					if strings.EqualFold(strings.TrimSpace(resolved.CanonicalText), strings.TrimSpace(entity.CanonicalName)) {
						entityID = resolved.EntityID
						break
					}
				}
			}
			if entityID == "" {
				continue
			}
			bundle.Assets = append(bundle.Assets, capabilityoverlay.BoundAsset{
				EntityID: entityID,
				AssetID:  entity.Image.AssetID, ContentHash: entity.Image.SHA256,
				DriveFileID: entity.Image.DriveFileID, SourceURL: entity.Image.PreviewURL,
				Verified: entity.Image.PreviewURL != "" || entity.Image.DriveFileID != "",
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

func validContentHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, r := range strings.ToLower(hash) {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
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
