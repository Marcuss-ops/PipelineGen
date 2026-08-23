// Package scriptgeneration — entity_extraction_switch_test.go certifies the
// request-level kill switch that disables incremental VidRush entity
// extraction: BuildGenerateRequest carries output.extract_entities into the
// durable request, and beginVidRush returns no coordinator when the caller
// explicitly disabled extraction, so the completed run carries no entity
// aggregate and the enricher is never invoked.
package scriptgeneration

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	mu    sync.Mutex
	calls int
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
		Insights: scriptpkg.SegmentInsights{Entities: []scriptpkg.ExtractedEntity{
			{Value: "Jackie Chan", Type: "PERSON", Confidence: 0.9},
		}},
	}, nil
}

func (e *entitySwitchEnricher) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func entitySwitchPipeline(enricher SegmentEnricher) *VidRushPipeline {
	return &VidRushPipeline{
		Enricher: enricher,
		PlanResolver: VidRushPlanResolverFunc(func(_ context.Context, _ GenerateRequest) (*scriptpkg.ResolvedGenerationPlan, error) {
			return &scriptpkg.ResolvedGenerationPlan{}, nil
		}),
	}
}

// TestRunner_BeginVidRushSkippedWhenEntityExtractionDisabled certifies the
// gate site: a disabled request builds no coordinator, while a default
// request still builds one.
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

// TestRunner_DefaultExtractEntitiesStillRunsVidRush guards the non-regression
// side: an omitted toggle keeps the canonical always-extract behavior, so the
// enricher runs exactly once per committed scene and the aggregate is present.
func TestRunner_DefaultExtractEntitiesStillRunsVidRush(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	enricher := &entitySwitchEnricher{}
	runner.SetVidRushPipeline(entitySwitchPipeline(enricher))

	req := defaultTestRequest()
	runID := "run-extract-default-001"
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
