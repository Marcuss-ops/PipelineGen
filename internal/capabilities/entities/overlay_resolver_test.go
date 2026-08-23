package entities

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// entityTimelineFixture returns a two-scene EntityTimeline with known global
// audio positions:
//
//	scene-0 (offset 0s):       "Tom Hanks"   0.000–0.200s   → card at 0–200ms
//	scene-3 (offset 45.000s):  "Tom Hanks"   48.240–48.360s → card at 48240–48360ms
//	scene-3 (offset 45.000s):  "Los Angeles" 48.460–48.660s → card at 48460–48660ms
func entityTimelineFixture(t *testing.T) EntityTimeline {
	t.Helper()
	scene0 := "Tom Hanks is an actor"
	words := strings.Fields(scene0)
	t0 := wordTimingFor(words, nil, "hash-0")

	filler := make([]string, 32)
	for i := range filler {
		filler[i] = "word"
	}
	scene3 := strings.Join(append(append(append([]string{}, filler...), "Tom", "Hanks", "then", "Los", "Angeles", "appears"), "final"), " ")
	words3 := strings.Fields(scene3)
	t3 := wordTimingFor(words3, map[int]int{30: 120, 31: 120, 32: 60, 33: 60}, "hash-3")

	timeline, err := BuildEntityTimeline(BuildInput{
		ProjectID:  "cert-project",
		Language:   "en",
		DurationUS: 50_000_000,
		Scenes: []SceneInput{
			{SceneID: "scene-0", SceneIndex: 0, Text: scene0, VoiceoverAssetID: "vo-0", TimelineStartUS: 0, Timing: t0, Entities: []EntitySource{{Name: "Tom Hanks", Type: "PERSON", Confidence: 0.98}}},
			{SceneID: "scene-3", SceneIndex: 3, Text: scene3, VoiceoverAssetID: "vo-3", TimelineStartUS: 45_000_000, Timing: t3, Entities: []EntitySource{
				{Name: "Tom Hanks", Type: "PERSON", Confidence: 0.98},
				{Name: "Los Angeles", Type: "GPE", Confidence: 0.9},
			}},
		},
	})
	require.NoError(t, err)
	return timeline
}

func TestResolveEntityOverlayPlan_EveryOccurrenceBecomesAnEntityCard(t *testing.T) {
	timeline := entityTimelineFixture(t)
	plan, err := ResolveEntityOverlayPlan(timeline, "plan-entity-001", "video-001", "cert-project", 1920, 1080, 30, 1)
	require.NoError(t, err)
	require.NoError(t, plan.Validate())
	require.Equal(t, capabilityoverlay.SchemaVersionPlan, plan.SchemaVersion)
	require.Equal(t, "plan-entity-001", plan.PlanID)
	require.Equal(t, "video-001", plan.VideoID)
	require.Equal(t, 1920, plan.Width)
	require.Equal(t, 1080, plan.Height)
	require.Equal(t, 30, plan.FPSNum)
	require.Equal(t, 1, plan.FPSDen)
	require.Len(t, plan.Items, 3)

	// The plan is sealed: every item carries a render key and the plan a
	// fingerprint, so the caller can enqueue it directly.
	require.NotEmpty(t, plan.Fingerprint)
	for _, item := range plan.Items {
		require.NotEmpty(t, item.RenderKey)
	}

	byID := map[string]capabilityoverlay.OverlayItem{}
	for _, item := range plan.Items {
		byID[item.ID] = item
	}

	// scene-0 Tom Hanks → overlay-scene-0-tom-hanks, 0–200ms.
	first := byID["overlay-scene-0-tom-hanks"]
	require.Equal(t, "scene-0", first.SceneID)
	require.Equal(t, StableEntityID("PERSON", "Tom Hanks"), first.EntityID)
	require.Equal(t, string(capabilityoverlay.KindEntityCard), first.Kind)
	require.Equal(t, "person_default", first.TemplateID)
	require.Equal(t, "Tom Hanks", first.Text)
	require.Equal(t, int64(0), first.StartMs)
	require.Equal(t, int64(200), first.EndMs)

	// scene-3 Tom Hanks → global 48.240–48.360s → 48240–48360ms.
	second := byID["overlay-scene-3-tom-hanks"]
	require.Equal(t, "person_default", second.TemplateID)
	require.Equal(t, int64(48_240), second.StartMs)
	require.Equal(t, int64(48_360), second.EndMs)

	// scene-3 Los Angeles → location kind / GPE template; global
	// 48.460–48.660s (local 3.460–3.660s inside the VO) → 48460–48660ms.
	third := byID["overlay-scene-3-los-angeles"]
	require.Equal(t, "gpe_default", third.TemplateID)
	require.Equal(t, string(capabilityoverlay.KindLocation), third.Kind)
	require.Equal(t, "Los Angeles", third.Text)
	require.Equal(t, int64(48_460), third.StartMs)
	require.Equal(t, int64(48_660), third.EndMs)
}

// TestResolveEntityOverlayPlan_CompilesToChronon certifies the full chain the
// spec asks for BEFORE Chronon: EntityTimeline → OverlayPlan → concrete
// chronon.render-plan.v1 layers. The entity card must appear exactly on the
// frames where the entity is spoken: 48.240s @ 30fps = frame 1447, duration
// 0.120s = 4 frames.
func TestResolveEntityOverlayPlan_CompilesToChronon(t *testing.T) {
	timeline := entityTimelineFixture(t)
	plan, err := ResolveEntityOverlayPlan(timeline, "plan-entity-002", "video-002", "", 1280, 720, 30, 1)
	require.NoError(t, err)

	compiled, err := capabilityoverlay.CompileChrononPlan(plan)
	require.NoError(t, err)
	require.Equal(t, capabilityoverlay.ChrononSchema, compiled.Plan.Schema)
	require.Equal(t, 1, compiled.Plan.Version)
	require.Equal(t, "plan-entity-002", compiled.Plan.JobID)
	require.Len(t, compiled.Plan.Layers, 3)

	layerByID := map[string]capabilityoverlay.ChrononLayer{}
	for _, layer := range compiled.Plan.Layers {
		layerByID[layer.ID] = layer
	}

	// scene-3 Tom Hanks: frame(48240ms * 30 / 1000) = round(1447.2) = 1447;
	// duration = frame(48360) - frame(48240) = round(1450.8) - 1447 = 1451-1447 = 4.
	tom := layerByID["overlay-scene-3-tom-hanks"]
	require.Equal(t, "", tom.Type)
	require.Equal(t, findItem(t, plan, tom.ID).PresetID, tom.Preset)
	require.Equal(t, "Tom Hanks", tom.Text)
	require.Equal(t, int64(1447), tom.StartFrame)
	require.Equal(t, int64(4), tom.DurationFrames)

	// scene-0 Tom Hanks at frame 0.
	first := layerByID["overlay-scene-0-tom-hanks"]
	require.Equal(t, int64(0), first.StartFrame)
	require.Equal(t, int64(6), first.DurationFrames)
}

// TestResolveEntityOverlayPlan_MichaelJordanReplayDeterministic repeats the
// same live-job identity (job, scene and semantic item IDs) twice and compares
// both the sealed semantic plan and the effective Chronon layers. A retry of
// the same job must not select a different preset or render key.
func TestResolveEntityOverlayPlan_MichaelJordanReplayDeterministic(t *testing.T) {
	jobID := "job_1787213417635353178_0b88fd56"
	timeline := EntityTimeline{
		Version:    EntityTimelineVersion,
		Language:   "en",
		DurationUS: 18_816_000,
		Scenes: []SceneEntityTimeline{{
			SceneID:         "scene-0",
			SceneIndex:      0,
			TimelineStartUS: 0,
			Entities: []EntityOccurrence{
				{EntityID: StableEntityID("PERSON", "Michael Jordan"), Name: "Michael Jordan", Type: "PERSON", SceneID: "scene-0", SceneIndex: 0, TextStart: 0, TextEnd: 14, WordStart: 0, WordEnd: 1, LocalStartUS: 50_000, LocalEndUS: 950_000, TimelineStartUS: 0, AudioStartUS: 50_000, AudioEndUS: 950_000, Confidence: 0.9},
				{EntityID: StableEntityID("PERSON", "Air Jordan"), Name: "Air Jordan", Type: "PERSON", SceneID: "scene-0", SceneIndex: 0, TextStart: 66, TextEnd: 76, WordStart: 11, WordEnd: 12, LocalStartUS: 4_600_000, LocalEndUS: 5_087_500, TimelineStartUS: 0, AudioStartUS: 4_600_000, AudioEndUS: 5_087_500, Confidence: 0.9},
				{EntityID: StableEntityID("CONCEPT", "Nike"), Name: "Nike", Type: "CONCEPT", SceneID: "scene-0", SceneIndex: 0, TextStart: 27, TextEnd: 31, WordStart: 4, WordEnd: 4, LocalStartUS: 1_425_000, LocalEndUS: 1_875_000, TimelineStartUS: 0, AudioStartUS: 1_425_000, AudioEndUS: 1_875_000, Confidence: 0.95},
				{EntityID: StableEntityID("CONCEPT", "Chicago"), Name: "Chicago", Type: "CONCEPT", SceneID: "scene-0", SceneIndex: 0, TextStart: 35, TextEnd: 42, WordStart: 6, WordEnd: 6, LocalStartUS: 2_012_500, LocalEndUS: 2_737_500, TimelineStartUS: 0, AudioStartUS: 2_012_500, AudioEndUS: 2_737_500, Confidence: 0.95},
			},
		}},
	}

	first, err := ResolveEntityOverlayPlan(timeline, jobID, "video-"+jobID, "", 1920, 1080, 30, 1)
	require.NoError(t, err)
	second, err := ResolveEntityOverlayPlan(timeline, jobID, "video-"+jobID, "", 1920, 1080, 30, 1)
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(first, second), "same job/scene/item identities must produce identical OverlayPlans")

	firstChronon, err := capabilityoverlay.CompileChrononPlan(first)
	require.NoError(t, err)
	secondChronon, err := capabilityoverlay.CompileChrononPlan(second)
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(firstChronon.Plan, secondChronon.Plan), "same job/scene/item identities must produce identical Chronon plans")

	for _, item := range first.Items {
		t.Logf("PRESET_REPLAY_TABLE job=%s scene=%s item=%s preset=%s render_key=%s", jobID, item.SceneID, item.ID, item.PresetID, item.RenderKey)
	}
	for i, layer := range firstChronon.Plan.Layers {
		require.Equal(t, first.Items[i].PresetID, layer.Preset, "layer %q preset must match semantic plan", layer.ID)
	}
}

func TestSpecialNamePresetsFollowEntityType(t *testing.T) {
	for _, entityType := range []string{"PERSON", "ORGANIZATION", "LOCATION", "UNKNOWN"} {
		got := capabilityoverlay.SelectEntityNamePreset("test-job", "scene", "entity-"+entityType, entityType)
		require.Contains(t, []string{"name_glow_typewriter", "name_glow_slide", "name_glow_pop"}, got, entityType)
	}
}

// TestResolveEntityOverlayPlan_TypeKindMapping pins the NLP-type → overlay-kind
// translation: PERSON→entity_card, ORG→organization, GPE/LOCATION→location,
// NUMBER→number, QUOTE→quote, PRODUCT→product, LOGO→logo, everything
// else→concept. The mapping has ONE owner (overlays.EntityTypeToKind); the
// kind→template mapping itself is owned by ChrononOverlayRegistry (resolved
// below, not hard-coded here).
func TestResolveEntityOverlayPlan_TypeKindMapping(t *testing.T) {
	require.Equal(t, capabilityoverlay.KindEntityCard, capabilityoverlay.EntityTypeToKind("PERSON"))
	require.Equal(t, capabilityoverlay.KindOrganization, capabilityoverlay.EntityTypeToKind("ORG"))
	require.Equal(t, capabilityoverlay.KindOrganization, capabilityoverlay.EntityTypeToKind("ORGANIZATION"))
	require.Equal(t, capabilityoverlay.KindLocation, capabilityoverlay.EntityTypeToKind("GPE"))
	require.Equal(t, capabilityoverlay.KindLocation, capabilityoverlay.EntityTypeToKind("LOCATION"))
	require.Equal(t, capabilityoverlay.KindNumber, capabilityoverlay.EntityTypeToKind("NUMBER"))
	require.Equal(t, capabilityoverlay.KindNumber, capabilityoverlay.EntityTypeToKind("NUM"))
	require.Equal(t, capabilityoverlay.KindQuote, capabilityoverlay.EntityTypeToKind("QUOTE"))
	require.Equal(t, capabilityoverlay.KindProduct, capabilityoverlay.EntityTypeToKind("PRODUCT"))
	require.Equal(t, capabilityoverlay.KindLogo, capabilityoverlay.EntityTypeToKind("LOGO"))
	require.Equal(t, capabilityoverlay.KindConcept, capabilityoverlay.EntityTypeToKind("EVENT"))
	require.Equal(t, capabilityoverlay.KindConcept, capabilityoverlay.EntityTypeToKind(""))
}

// TestResolveEntityOverlayPlan_TypeTemplateViaRegistry pins that the template
// id comes from the registry (single owner), not a hard-coded switch here.
func TestResolveEntityOverlayPlan_TypeTemplateViaRegistry(t *testing.T) {
	cases := map[string]string{
		"PERSON":    "person_default",
		"ORG":       "org_default",
		"GPE":       "gpe_default",
		"LOCATION":  "gpe_default",
		"NUMBER":    "NUMBER",
		"QUOTE":     "quote",
		"PRODUCT":   "PRODUCT",
		"LOGO":      "LOGO",
		"EVENT":     "concept_default",
		"something": "concept_default",
	}
	for entityType, wantTemplate := range cases {
		kind := capabilityoverlay.EntityTypeToKind(entityType)
		tmpl, err := capabilityoverlay.DefaultChrononOverlayRegistry.ResolveTemplate(string(kind))
		require.NoError(t, err)
		require.Equal(t, wantTemplate, tmpl, "entity type %q", entityType)
	}
}

// TestResolveEntityOverlayPlan_EmptyTimelineFailsClosed certifies that a
// timeline without occurrences cannot produce an empty plan.
func TestResolveEntityOverlayPlan_EmptyTimelineFailsClosed(t *testing.T) {
	_, err := ResolveEntityOverlayPlan(EntityTimeline{Version: EntityTimelineVersion, DurationUS: 1_000_000}, "p", "v", "", 1920, 1080, 30, 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no entity occurrences")
}
