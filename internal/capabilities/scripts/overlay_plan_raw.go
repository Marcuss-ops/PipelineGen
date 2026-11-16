package scriptgeneration

import (
	"sort"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"golang.org/x/text/unicode/norm"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// buildSceneImageProjection keeps only the materialized image candidates in
// the request. Text surfaces are sent raw and projected by Rust; this function
// must not call overlaySceneInput or any phrase/entity selector.
func buildSceneImageProjection(
	timedScenes []Scene,
	imagesByScene map[string][]capabilityoverlay.ImageCandidate,
	timelineEndUS map[string]int64,
) []capabilityoverlay.SceneInput {
	scenes := make([]capabilityoverlay.SceneInput, 0, len(timedScenes))
	for _, scene := range timedScenes {
		input := capabilityoverlay.SceneInput{ID: scene.ID, EndUS: timelineEndUS[scene.ID]}
		if images := imagesByScene[scene.ID]; len(images) > 0 {
			input.Images = append([]capabilityoverlay.ImageCandidate(nil), images...)
		}
		scenes = append(scenes, input)
	}
	return scenes
}

// buildRawOverlayScenes is the WP4 producer boundary: it copies grounded
// annotation data and media facts, never a selected/timed overlay candidate.
func buildRawOverlayScenes(
	result *GenerateResult,
	language Language,
	timedScenes []Scene,
	plates capabilityoverlay.PlateResolver,
) []overlayplan.RawScene {
	out := make([]overlayplan.RawScene, 0, len(timedScenes))
	for _, scene := range timedScenes {
		raw := overlayplan.RawScene{
			SceneID:             scene.ID,
			ChartsCertified:     result != nil && result.Editorial != nil && result.Editorial.Certified,
			SourceNumericValues: sourceNumericValuesForScene(scene, result, language),
		}
		if result != nil {
			for _, highlight := range result.SceneHighlights {
				if highlight.SceneID == scene.ID {
					raw.ChartValues = append([]string(nil), highlight.ChartValues...)
					break
				}
			}
		}
		ann := annotationsForLanguage(scene, language, result.SourceLanguage)
		if ann != nil {
			for _, span := range ann.ImportantPhrases {
				text := strings.TrimSpace(span.Text)
				if text == "" {
					continue
				}
				raw.ImportantPhrases = append(raw.ImportantPhrases, overlayplan.RawPhrase{Text: text, Score: span.Score})
			}
			raw.PrimaryEntities = rawEntities(ann.PrimaryEntities, plates)
			raw.SecondaryEntities = rawEntities(ann.SecondaryEntities, plates)
		}
		out = append(out, raw)
	}
	return out
}

func rawEntities(entities []scriptpkg.AnnotatedEntity, plates capabilityoverlay.PlateResolver) []overlayplan.RawEntity {
	if len(entities) == 0 {
		return nil
	}
	out := make([]overlayplan.RawEntity, 0, len(entities))
	for _, entity := range entities {
		name := strings.TrimSpace(entity.CanonicalName)
		if name == "" {
			continue
		}
		raw := overlayplan.RawEntity{
			StableEntityID:    capabilityentities.StableEntityID(entity.Type, name),
			CanonicalEntityID: strings.TrimSpace(entity.CanonicalEntityID),
			Type:              entity.Type,
			CanonicalName:     name,
			Confidence:        entity.Confidence,
		}
		if entity.Geo != nil {
			raw.Geo = &overlayplan.RawGeo{
				Latitude: entity.Geo.Latitude, Longitude: entity.Geo.Longitude,
				Scope: entity.Geo.Scope,
			}
			if plates != nil {
				_, raw.PlateCovered = plates.ResolvePlate(entity.Geo.Latitude, entity.Geo.Longitude)
			}
		}
		if entity.Image != nil {
			raw.Image = &overlayplan.RawEntityImage{
				AssetID: entity.Image.AssetID, URL: entityImageURL(entity.Image),
				SHA256: entity.Image.SHA256, MediaType: entity.Image.MediaType,
			}
		}
		for _, mention := range entity.Mentions {
			raw.Mentions = append(raw.Mentions, overlayplan.RawMention{
				Text: mention.Text, StartRune: mention.StartRune, EndRune: mention.EndRune,
			})
		}
		out = append(out, raw)
	}
	return out
}

func sourceNumericValuesForScene(scene Scene, result *GenerateResult, language Language) []string {
	if result == nil || result.SourceLanguage == "" || result.SourceLanguage == language {
		return nil
	}
	source := annotationsForLanguage(scene, result.SourceLanguage, result.SourceLanguage)
	if source == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	appendEntities := func(entities []scriptpkg.AnnotatedEntity) {
		for _, entity := range entities {
			if capmodel.EntityTypeToKind(entity.Type) != capmodel.KindNumber {
				continue
			}
			name := norm.NFC.String(strings.TrimSpace(entity.CanonicalName))
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, name)
		}
	}
	appendEntities(source.PrimaryEntities)
	appendEntities(source.SecondaryEntities)
	return out
}

func heavySentencesToWire(in []scriptpkg.ImportantSentence) []overlayplan.HeavySentence {
	if len(in) == 0 {
		return nil
	}
	out := make([]overlayplan.HeavySentence, 0, len(in))
	for _, sentence := range in {
		text := strings.TrimSpace(sentence.Text)
		if text == "" || sentence.Importance <= 0 {
			continue
		}
		out = append(out, overlayplan.HeavySentence{Text: text, Importance: sentence.Importance})
	}
	return out
}

func numericLexiconToWire() *overlayplan.NumericLexicon {
	lexicon := linguistics.DefaultLexiconOrNil()
	if lexicon == nil {
		return nil
	}
	return &overlayplan.NumericLexicon{
		Words:   sortedLexiconWords(lexicon.WordSet("fallback", "numeric_overlay_words")),
		Context: sortedLexiconWords(lexicon.WordSet("fallback", "numeric_overlay_context")),
	}
}

func sortedImageHashes(hashes map[string]struct{}) []string {
	if len(hashes) == 0 {
		return nil
	}
	out := make([]string, 0, len(hashes))
	for hash := range hashes {
		hash = strings.ToLower(strings.TrimSpace(hash))
		if hash != "" {
			out = append(out, hash)
		}
	}
	sort.Strings(out)
	return out
}

func sortedLexiconWords(words map[string]struct{}) []string {
	if len(words) == 0 {
		return nil
	}
	out := make([]string, 0, len(words))
	for word := range words {
		out = append(out, word)
	}
	sort.Strings(out)
	return out
}
