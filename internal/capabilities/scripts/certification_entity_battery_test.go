// Package scriptgeneration — certification_entity_battery_test.go is the
// executable Goal 4 definition of done.
//
// It proves, over the 24-script / 12-category ground-truth corpus, that the
// system keeps recognising entities on different topics, different arguments
// and different linguistic structures. Final overlays follow the production
// contract: up to five grounded phrases and five materialized images per run.
//
//	LEVEL 1 — SEMANTIC
//	  generate endpoint accepts every topic
//	  every script reaches the extraction plane automatically
//	  PERSON / ORG / GPE recognised and typed correctly
//	  no entity is invented (everything detected is grounded + spoken)
//	  the same entity is not duplicated under a slightly different name
//	  secondary entities never contaminate the primaries
//
//	LEVEL 2 — VISUAL (editorial budget)
//	  only grounded phrases and materialized images survive
//	  each category is capped at five overlays across the run
//	  entity extraction and timing remain available as semantic data
package scriptgeneration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestEntityBattery_CorpusIsHeterogeneous(t *testing.T) {
	corpus := entityBatteryCorpus()
	require.Len(t, corpus, 24, "the battery must keep its 24-script breadth")

	categories := map[string]int{}
	ids := map[string]bool{}
	for _, s := range corpus {
		categories[s.Category]++
		require.False(t, ids[s.ID], "script id %q must be unique", s.ID)
		ids[s.ID] = true
		require.NotEmpty(t, s.Scenes, "%s must declare scenes", s.ID)
		require.NotEmpty(t, s.Expected, "%s must declare ground truth", s.ID)
	}
	require.Len(t, categories, 12, "the corpus must span 12 topic categories, got %v", categories)
	for category, count := range categories {
		require.GreaterOrEqual(t, count, 2, "category %q must carry at least 2 scripts", category)
	}
}

// TestEntityBattery_GenerateEndpointAcceptsEveryTopic certifies the first link
// of the chain: the generate endpoint accepts every corpus topic and every
// request reaches the entity-extraction plane without manual intervention.
func TestEntityBattery_GenerateEndpointAcceptsEveryTopic(t *testing.T) {
	for _, s := range entityBatteryCorpus() {
		t.Run(s.ID, func(t *testing.T) {
			req := buildBatteryRequest(t, s)
			require.Equal(t, scriptpkg.ToggleEnabled, req.ExtractEntities)
			require.True(t, req.NeedsSemanticEnrichment(),
				"%s: an explicit entity request must reach the extraction plane", s.ID)
			require.Equal(t, SourceText, req.Source.Type)
			require.Equal(t, s.Topic, req.Source.Topic)
			require.False(t, req.EntityExtractionDisabled())
		})
	}
}

// TestCertification_EntityBattery_EndToEnd is the goal's definition of done: it
// runs every script through the REAL extraction chain and certifies semantic
// grounding, then certifies the global precision/recall/F1.
func TestCertification_EntityBattery_EndToEnd(t *testing.T) {
	corpus := entityBatteryCorpus()
	outcomes := make([]batteryScriptOutcome, 0, len(corpus))
	for _, s := range corpus {
		result := runEntityBatteryScript(t, s)
		outcome := measureBatteryScript(s, result)
		certifyBatteryScript(t, s, result, outcome)
		outcomes = append(outcomes, outcome)
	}

	t.Log(renderBatteryReport(outcomes))

	total := aggregateBatteryMetrics(outcomes)
	require.Equal(t, 0, total.Missed, "every expected entity must be recognised")
	require.Equal(t, 0, total.False, "no entity may be invented")
	require.Equal(t, 0, total.Duplicates, "no entity may be duplicated under a variant name")
	require.Equal(t, 1.0, total.Precision(), "NER precision must be 100%%")
	require.Equal(t, 1.0, total.Recall(), "NER recall must be 100%%")
	require.Equal(t, 1.0, total.F1(), "NER F1 must be 100%%")
}

// certifyBatteryScript enforces the level-1 and level-2 gates for one script.
func certifyBatteryScript(t *testing.T, s batteryScript, result *GenerateResult, outcome batteryScriptOutcome) {
	t.Helper()
	require.NotNil(t, result.EntityTimeline, "%s must project an EntityTimeline", s.ID)
	require.NoError(t, result.EntityTimeline.Validate(), "%s entity timeline must be valid", s.ID)

	// ── per-script metrics ────────────────────────────────────────────
	require.Zero(t, outcome.Metrics.Missed, "%s missed: %v", s.ID, outcome.Missed)
	require.Zero(t, outcome.Metrics.False, "%s false: %v", s.ID, outcome.False)
	require.Zero(t, outcome.Metrics.Duplicates, "%s duplicates: %v", s.ID, outcome.VariantDuplicates)

	textByScene, indexByScene, primaryByScene := batterySceneIndex(s, result)

	// ── LEVEL 1 — grounding: nothing is invented ──────────────────────
	for _, d := range outcome.Detected {
		text := textByScene[d.Scene]
		require.NotEmpty(t, text, "%s: detection %q references an unknown scene", s.ID, d.Name)
		require.Contains(t, strings.ToLower(text), strings.ToLower(d.Name),
			"%s: %q@%s must occur verbatim in the narration", s.ID, d.Name, d.Scene)
		require.GreaterOrEqual(t, d.EndUS, d.StartUS, "%s: %q must carry a valid certified window", s.ID, d.Name)
	}

	// ── LEVEL 1 — primary/secondary separation ────────────────────────
	for _, e := range s.Expected {
		idx, ok := indexByScene[e.Scene]
		require.True(t, ok, "%s: unknown expected scene %q", s.ID, e.Scene)
		annotation := result.Scenes[idx].Annotations
		require.NotNil(t, annotation, "%s: scene %s must carry annotations", s.ID, e.Scene)
		if scriptpkg.IsAnnotationEntityKind(e.Type) {
			require.True(t, primaryByScene[e.Scene][strings.ToLower(e.Name)],
				"%s: %s/%s must be a PRIMARY entity", s.ID, e.Type, e.Name)
			require.False(t, secondaryContains(annotation, e.Name),
				"%s: %s/%s must not appear among the secondary entities", s.ID, e.Type, e.Name)
		} else {
			require.True(t, secondaryContains(annotation, e.Name),
				"%s: %s/%s must be a SECONDARY entity", s.ID, e.Type, e.Name)
			require.False(t, primaryContains(annotation, e.Name),
				"%s: %s/%s must not appear among the primary entities", s.ID, e.Type, e.Name)
		}
	}

	// ── LEVEL 1 — rejected noise never survives ───────────────────────
	for _, rejected := range s.Rejected {
		require.False(t, detectionContains(outcome.Detected, rejected),
			"%s: rejected value %q must not become an entity", s.ID, rejected)
	}

	// ── LEVEL 2 — final editorial overlay contract ────────────────────
	// Entity cards and other semantic categories are outside final render
	// plans. Runs without grounded phrases or materialized images can have no
	// visual plan at all.
	if result.OverlayPlan == nil {
		return
	}
	phrases, images := 0, 0
	for _, item := range result.OverlayPlan.Items {
		switch item.Kind {
		case "text_phrase":
			phrases++
			require.NotEmpty(t, strings.TrimSpace(item.Text), "%s: phrase overlay must contain grounded text", s.ID)
		case "image", "entity_image":
			images++
			require.NotEmpty(t, item.AssetRefs, "%s: image overlay must be materialized", s.ID)
		default:
			t.Fatalf("%s: overlay kind %q is outside the 5+5 editorial contract", s.ID, item.Kind)
		}
	}
	require.LessOrEqual(t, phrases, capabilityoverlay.MaxPhraseOverlaysPerRun, "%s: phrase budget exceeded", s.ID)
	require.LessOrEqual(t, images, capabilityoverlay.MaxImageOverlaysPerRun, "%s: image budget exceeded", s.ID)
	require.LessOrEqual(t, len(result.OverlayPlan.Items), 10, "%s: total editorial overlay budget exceeded", s.ID)
}

// batterySceneIndex builds the per-scene lookups the gates need: narration
// text, the scene's index in the durable result, and the set of primary-entity
// names the durable annotations actually carry.
func batterySceneIndex(s batteryScript, result *GenerateResult) (map[string]string, map[string]int, map[string]map[string]bool) {
	textByScene := map[string]string{}
	indexByScene := map[string]int{}
	primaryByScene := map[string]map[string]bool{}
	for i, scene := range s.Scenes {
		textByScene[scene.ID] = scene.Text
		indexByScene[scene.ID] = i
		names := map[string]bool{}
		if i < len(result.Scenes) && result.Scenes[i].Annotations != nil {
			for _, entity := range result.Scenes[i].Annotations.PrimaryEntities {
				names[strings.ToLower(strings.TrimSpace(entity.CanonicalName))] = true
			}
		}
		primaryByScene[scene.ID] = names
	}
	return textByScene, indexByScene, primaryByScene
}

func detectionContains(detections []batteryDetection, name string) bool {
	want := strings.ToLower(strings.TrimSpace(name))
	for _, d := range detections {
		if strings.ToLower(strings.TrimSpace(d.Name)) == want {
			return true
		}
	}
	return false
}

func primaryContains(annotation *scriptpkg.SceneAnnotations, name string) bool {
	for _, entity := range annotation.PrimaryEntities {
		if strings.EqualFold(strings.TrimSpace(entity.CanonicalName), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func secondaryContains(annotation *scriptpkg.SceneAnnotations, name string) bool {
	for _, entity := range annotation.SecondaryEntities {
		if strings.EqualFold(strings.TrimSpace(entity.CanonicalName), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}
