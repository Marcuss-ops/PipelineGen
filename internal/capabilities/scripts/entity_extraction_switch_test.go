// Package scriptgeneration — entity_extraction_switch_test.go certifies the
// semantic gate for incremental VidRush enrichment: explicit consumers opt in,
// while plain runs without semantic outputs skip the NLP/provider pipeline.
package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestBuildGenerateRequest_MapsExtractEntitiesToggle certifies that the
// envelope's output.extract_entities toggle reaches GenerateRequest as the
// canonical tri-state: omitted → default, false → disabled, true → enabled.
func TestBuildGenerateRequest_MapsExtractEntitiesToggle(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		want         scriptpkg.Toggle
		wantDisabled bool
	}{
		{
			// A fully omitted output block leaves the zero-value toggle (""),
			// which EntityExtractionDisabled treats exactly like default: not
			// explicitly disabled, so extraction keeps running.
			name:         "omitted keeps extraction on",
			raw:          `{"version":2,"items":[{"title":"t","language":"en","source":{"type":"text","topic":"topic"}}]}`,
			want:         scriptpkg.Toggle(""),
			wantDisabled: false,
		},
		{
			name:         "explicit false disables",
			raw:          `{"version":2,"items":[{"title":"t","language":"en","source":{"type":"text","topic":"topic"},"output":{"extract_entities":false}}]}`,
			want:         scriptpkg.ToggleDisabled,
			wantDisabled: true,
		},
		{
			name:         "explicit true enables",
			raw:          `{"version":2,"items":[{"title":"t","language":"en","source":{"type":"text","topic":"topic"},"output":{"extract_entities":true}}]}`,
			want:         scriptpkg.ToggleEnabled,
			wantDisabled: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var env scriptpkg.GenerationEnvelopeV2
			if err := json.Unmarshal([]byte(tc.raw), &env); err != nil {
				t.Fatal(err)
			}
			got, err := BuildGenerateRequest(&env, "extract-entities-key")
			if err != nil {
				t.Fatal(err)
			}
			if got.ExtractEntities != tc.want {
				t.Fatalf("ExtractEntities = %q, want %q", got.ExtractEntities, tc.want)
			}
			if got.EntityExtractionDisabled() != tc.wantDisabled {
				t.Fatalf("EntityExtractionDisabled() = %v, want %v", got.EntityExtractionDisabled(), tc.wantDisabled)
			}
		})
	}
}

// entitySwitchEnricher is a SegmentEnricher that counts invocations and
// returns a single entity-bearing segment, so tests can assert both that the
// enricher runs (default) and that it is skipped (extract_entities disabled).
type entitySwitchEnricher struct {
	mu              sync.Mutex
	calls           int
	translatedCalls int
}

func (e *entitySwitchEnricher) Extract(_ context.Context, _ string, text string, _ int) ([]VisualEntity, error) {
	e.mu.Lock()
	if strings.HasPrefix(text, "[TRANSLATED]") {
		e.translatedCalls++
	} else {
		e.calls++
	}
	e.mu.Unlock()
	if text == "" {
		return nil, nil
	}
	return []VisualEntity{{
		Text:     text,
		Type:     scriptpkg.EntityTypePerson,
		Score:    0.9,
		Start:    0,
		End:      len(text),
		Evidence: text,
	}}, nil
}

func (e *entitySwitchEnricher) Enrich(_ context.Context, _ *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	return scriptpkg.VidRushSegmentResult{
		SegmentID: scene.ID,
		SceneID:   scene.ID,
		Position:  scene.Index,
		Text:      scene.Text,
		TextHash:  SceneTextHash(scene.Text),
		Insights: scriptpkg.SegmentInsights{SegmentID: scene.ID, TextHash: SceneTextHash(scene.Text), Entities: []scriptpkg.ExtractedEntity{
			{Value: "Jackie Chan", Type: "PERSON", Confidence: 0.9},
		}},
	}, nil
}

func (e *entitySwitchEnricher) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func entitySwitchPipeline(enricher VisualNERPort) *VidRushPipeline {
	return &VidRushPipeline{
		NERPort: enricher,
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return &scriptpkg.ResolvedGenerationPlan{Language: "en"}, nil
		}),
	}
}

// TestRunner_BeginVidRushSkippedWhenEntityExtractionDisabled certifies the
// gate site: a disabled request builds no coordinator, while an explicit
// entity request still builds one.
func TestRunner_BeginVidRushSkippedWhenEntityExtractionDisabled(t *testing.T) {
	runner, _, _, _, _, _, _ := newTestRunner()
	runner.SetVidRushPipeline(entitySwitchPipeline(&entitySwitchEnricher{}))

	disabled := defaultTestRequest()
	disabled.ExtractEntities = scriptpkg.ToggleDisabled
	coord, err := runner.beginVidRush(context.Background(), "run-disabled", disabled)
	require.NoError(t, err)
	assert.Nil(t, coord, "extract_entities=disabled must skip the VidRush coordinator")

	enabled := defaultTestRequest()
	coord, err = runner.beginVidRush(context.Background(), "run-enabled", enabled)
	require.NoError(t, err)
	require.NotNil(t, coord, "default request must still build the VidRush coordinator")
	runner.endVidRush("run-enabled")
}

// TestRunner_ExtractEntitiesDisabledLeavesResultWithoutEntities certifies the
// end-to-end contract: a run with extract_entities=disabled completes without
// a durable entity aggregate, per-scene entities, or any enricher invocation.
func TestRunner_ExtractEntitiesDisabledLeavesResultWithoutEntities(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enricher := &entitySwitchEnricher{}
	runner.SetVidRushPipeline(entitySwitchPipeline(enricher))

	req := defaultTestRequest()
	req.ExtractEntities = scriptpkg.ToggleDisabled
	runID := "run-extract-disabled-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	assert.Nil(t, final.Result.Entities, "disabled extraction must leave the durable entity aggregate empty")
	assert.Zero(t, enricher.callCount(), "the VidRush enricher must not run when extraction is disabled")
	for _, scene := range final.Result.Scenes {
		assert.Nil(t, scene.Entities, "per-scene entities must not be populated when extraction is disabled")
	}
}

// TestRunner_ExplicitExtractEntitiesStillRunsVidRush guards the opt-in side:
// an explicit entity consumer runs the enricher exactly once per committed
// scene and persists the aggregate.
func TestRunner_SegmentOverridesControlEnrichmentAndProjection(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enricher := &entitySwitchEnricher{}
	runner.SetVidRushPipeline(entitySwitchPipeline(enricher))

	req := defaultTestRequest()
	req.ExtractEntities = scriptpkg.ToggleDisabled
	req.ScriptParams.Segments = []scriptpkg.ScriptSegment{
		{ID: "scene-0", Topic: "enabled scene", ExtractEntities: scriptpkg.ToggleEnabled},
		{ID: "scene-1", Topic: "disabled scene", ExtractEntities: scriptpkg.ToggleDisabled},
	}
	runID := "run-segment-extraction-overrides"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	require.NotNil(t, final.Result.Entities, "the explicitly enabled scene must produce an entity aggregate")
	assert.Equal(t, 1, enricher.callCount(), "only the enabled scene should enter entity enrichment")
	require.Len(t, final.Result.Scenes, 3)
	assert.NotNil(t, final.Result.Scenes[0].Entities, "enabled scene must receive entity results")
	assert.Nil(t, final.Result.Scenes[1].Entities, "disabled scene must not receive entity results")
	assert.Nil(t, final.Result.Scenes[1].Annotations, "disabled scene must not retain entity annotations")
	assert.Nil(t, final.Result.Scenes[1].VidRush, "disabled scene must not expose its internal barrier placeholder")
	assert.Nil(t, final.Result.Scenes[2].Entities, "unmatched scene must inherit the disabled run policy")
	assert.Nil(t, final.Result.Scenes[2].Annotations, "unmatched scene must not retain entity annotations")
	assert.Nil(t, final.Result.Scenes[2].VidRush, "unmatched scene must not expose its internal barrier placeholder")
	assert.Len(t, final.Result.Segments, 1, "only the enabled segment's semantic result should be durable")
	assert.Equal(t, "scene-0", final.Result.Segments[0].SceneID)
}

func TestRunner_ExplicitExtractEntitiesStillRunsVidRush(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enricher := &entitySwitchEnricher{}
	runner.SetVidRushPipeline(entitySwitchPipeline(enricher))

	req := defaultTestRequest()
	runID := "run-extract-enabled-001"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.NotNil(t, final)
	require.Equal(t, RunStatusCompleted, final.Status)
	require.NotNil(t, final.Result)
	require.NotNil(t, final.Result.Entities, "default request must still extract the entity aggregate")
	assert.Equal(t, len(defaultTestScenes()), enricher.callCount(), "each committed scene enriched exactly once")
}

func TestGenerateRequest_SceneExtractionToggleUsesStableIdentity(t *testing.T) {
	req := GenerateRequest{
		ExtractEntities: scriptpkg.ToggleDisabled,
		ScriptParams: scriptpkg.ScriptSpec{Segments: []scriptpkg.ScriptSegment{
			{ID: "scene-1", Topic: "first", ExtractEntities: scriptpkg.ToggleEnabled},
			{ID: "scene-2", Topic: "second", ExtractEntities: scriptpkg.ToggleDisabled},
		}},
	}
	assert.Equal(t, scriptpkg.ToggleEnabled, sceneExtractionToggle(req, Scene{ID: "scene-1", Index: 1}), "stable matching ID wins over an unrelated positional segment")
	assert.Equal(t, scriptpkg.ToggleDisabled, sceneExtractionToggle(req, Scene{ID: "scene-generated", Index: 0}), "a non-empty mismatched positional ID cannot leak another segment policy")

	legacy := GenerateRequest{
		ExtractEntities: scriptpkg.ToggleDisabled,
		ScriptParams:    scriptpkg.ScriptSpec{Segments: []scriptpkg.ScriptSegment{{Topic: "legacy", ExtractEntities: scriptpkg.ToggleEnabled}}},
	}
	assert.Equal(t, scriptpkg.ToggleEnabled, sceneExtractionToggle(legacy, Scene{ID: "scene-0", Index: 0}), "ID-less legacy segment resolves by position")
}

func TestGenerateRequest_NeedsSemanticEnrichment(t *testing.T) {
	plain := GenerateRequest{}
	assert.False(t, plain.NeedsSemanticEnrichment(), "no semantic consumer must skip VidRush")

	for name, req := range map[string]GenerateRequest{
		"explicit entities":     {ExtractEntities: scriptpkg.ToggleEnabled},
		"explicit scene images": {GenerateSceneImages: scriptpkg.ToggleEnabled},
		"active media plan":     {MediaPlan: mediadomain.MediaPlanSpec{Mode: mediadomain.MediaPlanModeAuto}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, req.NeedsSemanticEnrichment())
		})
	}

	assert.False(t, GenerateRequest{
		ExtractEntities:     scriptpkg.ToggleDisabled,
		GenerateSceneImages: scriptpkg.ToggleEnabled,
	}.NeedsSemanticEnrichment(), "explicit disable must win unless an individual segment opts in")

	segmentOverride := GenerateRequest{
		ExtractEntities: scriptpkg.ToggleDisabled,
		ScriptParams: scriptpkg.ScriptSpec{Segments: []scriptpkg.ScriptSegment{{
			ID: "scene-on", Topic: "enabled scene", ExtractEntities: scriptpkg.ToggleEnabled,
		}}},
	}
	assert.True(t, segmentOverride.NeedsSemanticEnrichment(), "enabled segment must opt back in against run-level disable")
	assert.Equal(t, scriptpkg.ToggleEnabled, sceneExtractionToggle(segmentOverride, Scene{ID: "scene-on", Index: 0}), "scene ID selector preserves enabled override")
	assert.Equal(t, scriptpkg.ToggleDisabled, sceneExtractionToggle(segmentOverride, Scene{ID: "scene-off", Index: 1}), "unmatched scene inherits the disabled run default")
}
