package overlayplan

import (
	"encoding/json"
	"sort"
	"strings"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"golang.org/x/text/unicode/norm"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	overlayplan "github.com/Marcuss-ops/PipelineGen/internal/kernel/overlayplan"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func buildSceneImageProjection(
	timedScenes []contract.Scene,
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
	result *contract.GenerateResult,
	language contract.Language,
	timedScenes []contract.Scene,
	plates capabilityoverlay.PlateResolver,
) []overlayplan.RawScene {
	out := make([]overlayplan.RawScene, 0, len(timedScenes))
	for _, scene := range timedScenes {
		raw := overlayplan.RawScene{
			SceneID:             scene.ID,
			ChartsCertified:     result != nil && result.Editorial != nil && result.Editorial.Certified,
			SourceNumericValues: sourceNumericValuesForScene(scene, result, language),
		}
		raw.VisualRecipes = VisualRecipesForScene(result, scene.ID)
		if len(raw.VisualRecipes) > 0 {
			raw.VisualRecipe = raw.VisualRecipes[0].Recipe
			raw.VisualPayload = append(json.RawMessage(nil), raw.VisualRecipes[0].Payload...)
		}
		if result != nil {
			for _, highlight := range result.SceneHighlights {
				if highlight.SceneID == scene.ID {
					raw.ChartValues = append([]string(nil), highlight.ChartValues...)
					break
				}
			}
		}
		// `ranking_bar` and `delta_comparison` are production candidates, not
		// proposal types, so their grounded values never entered the certified
		// chart lane. Project them here without choosing a layout: the chart
		// planner still decides whether the numbers form a readable chart.
		raw.ChartValues = mergeProductionChartValues(
			raw.ChartValues,
			productionChartValuesForScene(result, scene.ID),
		)
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

// overlayVisualRecipes is the set overlay-compiler actually lowers today. A
// producer hint outside this set (relationship_graph, choropleth_value_map,
// lifespan_timeline) would be validated and then ignored, so it is not shipped:
// an empty scene costs nothing and an unused payload is dead wire data.
// TICKET-48 kept those three validated-but-inert by decision, so this filter
// stays; reopening it means certifying a layout first.
var overlayVisualRecipes = map[string]bool{
	"counter_odometer":      true,
	"role_lower_third":      true,
	"route_map":             true,
	"quote_attribution":     true,
	"source_highlight":      true,
	"relative_time_jump":    true,
	"cause_effect_card":     true,
	"process_stepper":       true,
	"entity_attribute_list": true,
}

// VisualRecipesForScene forwards every supported grounded recipe of one scene,
// ordered by the engine's priority. Go never inspects a payload beyond checking
// that it is a JSON object and never chooses a layout: the planner applies each
// recipe once. Duplicate `(recipe, payload)` pairs are dropped so the same fact
// does not travel twice.
func VisualRecipesForScene(result *contract.GenerateResult, sceneID string) []overlayplan.VisualRecipeSpec {
	if result == nil || result.SemanticConsolidation == nil {
		return nil
	}
	type ranked struct {
		spec     overlayplan.VisualRecipeSpec
		priority float64
		rank     int
	}
	var ordered []ranked
	seen := make(map[string]bool)
	for _, candidate := range result.SemanticConsolidation.ProductionCandidates {
		if strings.TrimSpace(candidate.SceneID) != sceneID || !overlayVisualRecipes[candidate.Recipe] {
			continue
		}
		if len(candidate.VisualPayload) == 0 {
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(candidate.VisualPayload, &object); err != nil || len(object) == 0 {
			continue
		}
		key := candidate.Recipe + "\x00" + string(candidate.VisualPayload)
		if seen[key] {
			continue
		}
		seen[key] = true
		ordered = append(ordered, ranked{
			spec: overlayplan.VisualRecipeSpec{
				Recipe:  candidate.Recipe,
				Payload: append(json.RawMessage(nil), candidate.VisualPayload...),
			},
			priority: candidate.Priority,
			rank:     candidate.Rank,
		})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].priority != ordered[j].priority {
			return ordered[i].priority > ordered[j].priority
		}
		return ordered[i].rank < ordered[j].rank
	})
	out := make([]overlayplan.VisualRecipeSpec, 0, len(ordered))
	for _, entry := range ordered {
		out = append(out, entry.spec)
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
				AssetID: entity.Image.AssetID, URL: EntityImageURL(entity.Image),
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

// maxSemanticChartValues mirrors the editorial manifest limit: a chart with
// more than six bars is not readable.
const maxSemanticChartValues = 6

// productionChartValuesForScene projects the grounded chart items of the
// semantic production candidates into the certified chart lane. Only the two
// recipes whose values are a real numeric series qualify; an entity card or a
// phrase never contributes a chart value.
func productionChartValuesForScene(result *contract.GenerateResult, sceneID string) []string {
	if result == nil || result.SemanticConsolidation == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, candidate := range result.SemanticConsolidation.ProductionCandidates {
		if strings.TrimSpace(candidate.SceneID) != sceneID {
			continue
		}
		if candidate.Recipe != "ranking_bar" && candidate.Recipe != "delta_comparison" {
			continue
		}
		for _, item := range candidate.ChartItems {
			text := strings.TrimSpace(item.Text)
			if text == "" {
				continue
			}
			if _, duplicate := seen[text]; duplicate {
				continue
			}
			seen[text] = struct{}{}
			out = append(out, text)
			if len(out) >= maxSemanticChartValues {
				return out
			}
		}
	}
	return out
}

// mergeProductionChartValues keeps the already-authorized highlight values and
// appends the production values once, capped at the manifest limit.
func mergeProductionChartValues(existing, extra []string) []string {
	if len(existing) == 0 && len(extra) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(existing)+len(extra))
	out := make([]string, 0, len(existing)+len(extra))
	for _, value := range append(append([]string(nil), existing...), extra...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= maxSemanticChartValues {
			break
		}
	}
	return out
}

func sourceNumericValuesForScene(scene contract.Scene, result *contract.GenerateResult, language contract.Language) []string {
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
	lexicon := linguistics.DefaultProfilesOrNil()
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
