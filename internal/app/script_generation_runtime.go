package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// scriptGenerationTranslator adapts the application translation port to the
// capability runtime without leaking provider DTOs into capabilities/scripts.
type scriptGenerationTranslator struct {
	port translation.TranslationPort
}

func (a *scriptGenerationTranslator) Translate(ctx context.Context, input scriptgen.TranslationInput) (string, error) {
	if a == nil || a.port == nil {
		return "", fmt.Errorf("script generation translator is not configured")
	}
	result, err := a.port.Translate(ctx, translation.TranslationCommand{
		SourceLang: string(input.SourceLanguage),
		TargetLang: string(input.TargetLanguage),
		Text:       input.SourceText,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result.TranslatedText) == "" {
		return "", fmt.Errorf("script generation translator returned empty text")
	}
	return result.TranslatedText, nil
}

// scriptGenerationDocumentPublisher adapts Drive's idempotent document
// contract to the durable generation capability. Drive remains the concrete
// provider; the capability sees only its typed port.
type scriptGenerationDocumentPublisher struct {
	client drive.DocClient
}

func (a *scriptGenerationDocumentPublisher) UpsertDocument(ctx context.Context, input scriptgen.DocumentInput) (scriptgen.DocumentReference, error) {
	if a == nil || a.client == nil {
		return scriptgen.DocumentReference{}, fmt.Errorf("script generation document publisher is not configured")
	}
	key := strings.TrimSpace(input.RunID) + ":" + strings.TrimSpace(string(input.Language))
	doc, err := a.client.CreateDocIdempotent(ctx, input.Title, input.Content, input.FolderID, key, false)
	if err != nil {
		return scriptgen.DocumentReference{}, err
	}
	if doc == nil || strings.TrimSpace(doc.ID) == "" || strings.TrimSpace(doc.URL) == "" {
		return scriptgen.DocumentReference{}, fmt.Errorf("drive document publisher returned incomplete reference")
	}
	return scriptgen.DocumentReference{ID: doc.ID, Link: doc.URL}, nil
}

// buildScriptGenerationRuntime creates the worker-owned durable runtime. The
// HTTP starter only creates/correlates the run; this runtime is invoked by the
// script.generate worker after the submission transaction has committed.
func buildScriptGenerationRuntime(cfg *config.Config, root *wiring.ComposeRoot, runRepo scriptgen.RunRepository, log *zap.Logger) (*scriptgen.Runner, error) {
	if cfg == nil || root == nil || runRepo == nil {
		return nil, fmt.Errorf("script generation runtime requires config, composition root, and run repository")
	}
	if root.AI == nil || root.AI.SceneTextGenerator == nil || root.AI.OllamaTranslator == nil {
		return nil, fmt.Errorf("script generation runtime requires scene text and translation adapters")
	}
	if strings.TrimSpace(cfg.External.RustMusclesPath) == "" {
		return nil, fmt.Errorf("script generation runtime requires Rust media executor path")
	}

	executor := rustexec.NewExecutor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	videoProcessor := rustexec.NewConfiguredVideoProcessorWithExecutor(executor, root.MediaExec.Policy, root.MediaExec.Profile, log)
	audioRenderer, err := rustexec.NewCombinedAudioRenderer(videoProcessor)
	if err != nil {
		return nil, fmt.Errorf("build combined audio renderer: %w", err)
	}
	stockRenderer := rustexec.NewStockRendererWithExecutor(executor, root.MediaExec.Policy, root.MediaExec.Profile, log)
	renderEnqueuer, err := scriptgen.NewCanonicalRenderEnqueuer(stockRenderer)
	if err != nil {
		return nil, fmt.Errorf("build canonical render enqueuer: %w", err)
	}

	var docPublisher scriptgen.DocumentPublisher
	if root.Drive != nil && root.Drive.DocClient != nil {
		docPublisher = &scriptGenerationDocumentPublisher{client: root.Drive.DocClient}
	}
	runner := scriptgen.NewRunner(
		runRepo,
		root.AI.SceneTextGenerator,
		&scriptGenerationTranslator{port: root.AI.OllamaTranslator},
		root.AI.ScriptVoiceoverGenerator,
		docPublisher,
		renderEnqueuer,
	)
	runner.SetCombinedAudioRenderer(audioRenderer)
	if root.Jobs != nil && root.Jobs.JobLedger != nil {
		runner.SetExecutionRecorder(scriptjobs.NewJobRegistryExecutionRecorder(root.Jobs.JobLedger))
		log.Info("script generation execution recorder wired to Job Registry")
	} else {
		return nil, fmt.Errorf("build script generation runtime: Job Registry is required for execution lineage")
	}
	return runner, nil
}

var _ scriptgen.Translator = (*scriptGenerationTranslator)(nil)
var _ scriptgen.DocumentPublisher = (*scriptGenerationDocumentPublisher)(nil)
