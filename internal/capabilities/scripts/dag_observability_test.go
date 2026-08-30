package scriptgeneration

import (
	"context"
	"testing"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/stretchr/testify/require"
)

func TestDAGObservability_SeparatesPipelineBoundaries(t *testing.T) {
	run := kernobs.NewRunObserver(nil).StartRun(context.Background(), kernobs.RunInfo{
		JobID: "job-dag-observability", AttemptID: "attempt-1",
	})
	ctx := kernobs.WithRun(context.Background(), run)

	measure := func(stage kernobs.StageName, component kernobs.ComponentName, operation kernobs.OperationName) {
		err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage: stage, Component: component, Operation: operation,
		}, func(context.Context) error {
			time.Sleep(time.Millisecond)
			return nil
		})
		require.NoError(t, err)
	}

	measure(kernobs.StageGenerate, kernobs.ComponentOllama, kernobs.OperationGenerate)
	measure(voiceoverStage, kernobs.ComponentTTS, kernobs.OperationSynthesize)
	measure(StageSceneAnalysis, kernobs.ComponentNLP, kernobs.OperationExtract)
	measure(StageSceneAnalysis, kernobs.ComponentNLP, kernobs.OperationSearch)
	measure(StageSceneAnalysis, kernobs.ComponentArtlist, kernobs.OperationResolve)
	measure(StageOverlayPrepare, kernobs.ComponentRenderQueue, kernobs.OperationPlan)
	measure(audioCompileStage, kernobs.ComponentChronon, kernobs.OperationRender)
	measure(StageDocumentPrepare, kernobs.ComponentGoogleDocs, kernobs.OperationRender)
	measure(StageDocumentPublish, kernobs.ComponentGoogleDocs, kernobs.OperationPublish)
	run.Finish()

	report := run.Report()
	mustHaveOperation := func(stage string, component string, operation string) {
		for _, observed := range report.Operations {
			if observed.Stage == stage && observed.Component == component && observed.Operation == operation {
				return
			}
		}
		t.Fatalf("missing operation stage=%q component=%q operation=%q", stage, component, operation)
	}

	mustHaveOperation(string(kernobs.StageGenerate), string(kernobs.ComponentOllama), string(kernobs.OperationGenerate))
	mustHaveOperation(string(voiceoverStage), string(kernobs.ComponentTTS), string(kernobs.OperationSynthesize))
	mustHaveOperation(string(StageSceneAnalysis), string(kernobs.ComponentNLP), string(kernobs.OperationExtract))
	mustHaveOperation(string(StageSceneAnalysis), string(kernobs.ComponentNLP), string(kernobs.OperationSearch))
	mustHaveOperation(string(StageSceneAnalysis), string(kernobs.ComponentArtlist), string(kernobs.OperationResolve))
	mustHaveOperation(string(StageOverlayPrepare), string(kernobs.ComponentRenderQueue), string(kernobs.OperationPlan))
	mustHaveOperation(audioCompileStage, string(kernobs.ComponentChronon), string(kernobs.OperationRender))
	mustHaveOperation(string(StageDocumentPrepare), string(kernobs.ComponentGoogleDocs), string(kernobs.OperationRender))
	mustHaveOperation(string(StageDocumentPublish), string(kernobs.ComponentGoogleDocs), string(kernobs.OperationPublish))
}
