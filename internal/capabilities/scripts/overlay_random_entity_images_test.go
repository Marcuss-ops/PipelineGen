package scriptgeneration

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRandomImageEffectsReachMultipleEntityImagesAtFullHD(t *testing.T) {
	text := "Ada Lovelace met Grace Hopper."
	timing := speechTimingForWords([]string{"Ada", "Lovelace", "met", "Grace", "Hopper"})
	adaSHA, graceSHA := strings.Repeat("a", 64), strings.Repeat("b", 64)
	annotations := &scriptpkg.SceneAnnotations{
		Version: 1, Language: "en", Status: "completed",
		PrimaryEntities: []scriptpkg.AnnotatedEntity{
			{ID: "ada", Type: "PERSON", CanonicalName: "Ada Lovelace", Confidence: 0.95,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "ada-image", SHA256: adaSHA, PreviewURL: "https://images.example/ada.jpg", MediaType: "image/jpeg"}},
			{ID: "grace", Type: "PERSON", CanonicalName: "Grace Hopper", Confidence: 0.95,
				Image: &scriptpkg.EntityImageBinding{Status: "bound", AssetID: "grace-image", SHA256: graceSHA, PreviewURL: "https://images.example/grace.jpg", MediaType: "image/jpeg"}},
		},
	}
	adaID := capabilityentities.StableEntityID("PERSON", "Ada Lovelace")
	graceID := capabilityentities.StableEntityID("PERSON", "Grace Hopper")
	result := &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-0", Index: 0, Text: map[Language]string{"en": text}, Annotations: annotations,
			Voiceover: map[Language]AudioReference{"en": {ID: "voiceover-0", Duration: 0.5, Timing: &timing}},
		}},
		ResolvedScenes: []ResolvedScene{{ID: "scene-0", Index: 0, TimelineStartUS: 0, DurationUS: timing.DurationUS}},
		EntityTimeline: &capabilityentities.EntityTimeline{
			Version: capabilityentities.EntityTimelineVersion, Language: "en", DurationUS: timing.DurationUS,
			Scenes: []capabilityentities.SceneEntityTimeline{{SceneID: "scene-0", SceneIndex: 0, TimelineStartUS: 0, Entities: []capabilityentities.EntityOccurrence{
				{EntityID: adaID, Name: "Ada Lovelace", Type: "PERSON", SceneID: "scene-0", SceneIndex: 0, TextStart: 0, TextEnd: 12, WordStart: 0, WordEnd: 1, LocalStartUS: 0, LocalEndUS: 200_000, AudioStartUS: 0, AudioEndUS: 200_000, Confidence: 0.95},
				{EntityID: graceID, Name: "Grace Hopper", Type: "PERSON", SceneID: "scene-0", SceneIndex: 0, TextStart: 17, TextEnd: 29, WordStart: 3, WordEnd: 4, LocalStartUS: 300_000, LocalEndUS: 500_000, AudioStartUS: 300_000, AudioEndUS: 500_000, Confidence: 0.95},
			}}},
		},
	}

	canvas := GoldenOverlayCanvas
	canvas.Width, canvas.Height = 1920, 1080
	canvas.RandomImageEffects = true
	planID := ""
	for attempt := 0; attempt < 100; attempt++ {
		candidatePlanID := "random-entity-images-fullhd-" + strconv.Itoa(attempt)
		if capabilityoverlay.SelectImageEffectDecorationEnabled(candidatePlanID, "overlay-scene-0-ada-lovelace") ||
			capabilityoverlay.SelectImageEffectDecorationEnabled(candidatePlanID, "overlay-scene-0-grace-hopper") {
			planID = candidatePlanID
			break
		}
	}
	require.NotEmpty(t, planID, "a deterministic plan seed must select at least one of the two image effects")
	plan, err := CompileOverlayPlan(result, "en", canvas, planID, "video-fullhd", "project-fullhd")
	require.NoError(t, err)
	require.NotNil(t, plan)
	require.Equal(t, 1920, plan.Width)
	require.Equal(t, 1080, plan.Height)
	require.NoError(t, plan.Validate())

	var composite *capabilityoverlay.OverlayItem
	for index := range plan.Items {
		if len(plan.Items[index].ImageLayers) > 0 {
			composite = &plan.Items[index]
			break
		}
	}
	require.NotNil(t, composite, "the two entity portraits must reach one composite render item")
	require.Equal(t, "entity_image", composite.Kind)
	require.Len(t, composite.AssetRefs, 2)
	require.Len(t, composite.ImageLayers, 2)
	require.NotContains(t, composite.Params, "effects", "random decisions belong to individual photo surfaces")

	assets := map[string]bool{}
	for _, asset := range composite.AssetRefs {
		assets[asset.SHA256] = true
	}
	require.True(t, assets[adaSHA], "Ada's verified image must be carried to rendering")
	require.True(t, assets[graceSHA], "Grace's verified image must be carried to rendering")

	wire, err := json.Marshal(plan)
	require.NoError(t, err)
	wireText := string(wire)
	require.Contains(t, wireText, `"width":1920`)
	require.Contains(t, wireText, `"height":1080`)
	require.Contains(t, wireText, `"image_layers"`)
	selectedEffects := 0
	for _, layer := range composite.ImageLayers {
		if capabilityoverlay.SelectImageEffectDecorationEnabled(plan.PlanID, layer.ID) {
			selectedEffects++
			require.Contains(t, layer.Params, "effects", "selected random image treatment must survive on layer %q", layer.ID)
			require.Contains(t, wireText, `"effects"`, "random image treatment must be present on the render wire")
		}
		if layer.AssetID == "" || layer.Caption == "" {
			t.Fatalf("entity-image layer lost its asset or identity caption: %+v", layer)
		}
	}
	require.Positive(t, selectedEffects, "the full-HD render plan should exercise at least one random image effect")
}
