package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	documentadapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	caprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	videojob "github.com/Marcuss-ops/PipelineGen/internal/domain/video"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/renderinggen"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobregistry"
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

// scriptGenerationDocumentRenderer is the composition-root adapter from the
// capability renderer port to the canonical application renderer. The
// capability runner never owns HTML formatting or a second document builder.
type scriptGenerationDocumentRenderer struct{}

// finalAudioArtifactForDocument converts the capability-owned master
// reference into the domain artifact consumed by the document renderer. It
// never invents fields: unavailable values remain their zero value and are
// omitted from the projected JSON.
func finalAudioArtifactForDocument(ref *scriptgen.FinalAudioReference) *scriptpkg.FinalAudioArtifact {
	if ref == nil {
		return nil
	}
	return &scriptpkg.FinalAudioArtifact{
		AssetID:              ref.AssetID,
		Path:                 ref.Path,
		DriveLink:            ref.DriveLink,
		Container:            ref.Container,
		AudioContractVersion: ref.AudioContractVersion,
		AudioPlanVersion:     ref.AudioPlanVersion,
		AudioPlanSHA256:      ref.PlanSHA256,
		FinalAudioSHA256:     ref.FinalAudioSHA256,
		Codec:                ref.Codec,
		Profile:              ref.Profile,
		SampleRate:           ref.SampleRate,
		Channels:             ref.Channels,
		ChannelLayout:        ref.ChannelLayout,
		Bitrate:              ref.Bitrate,
		DurationUS:           ref.DurationUS,
		DurationMS:           ref.DurationMS,
		StartPTS:             ref.StartPTS,
		SizeBytes:            ref.SizeBytes,
		FinalMix:             ref.FinalMix,
		CopyEligible:         ref.CopyEligible,
	}
}

func (scriptGenerationDocumentRenderer) DocumentRendererID() string {
	return documentadapters.CanonicalDocumentRendererID
}

func (scriptGenerationDocumentRenderer) RenderDocument(model *scriptpkg.ModelScriptOutputV1, opts scriptgen.DocumentRenderOptions) (string, error) {
	return documentadapters.BuildSpecSceneDocumentHTML(model, documentadapters.SpecSceneDocumentOptions{
		Title:           opts.Title,
		Language:        string(opts.Language),
		DefaultLanguage: string(opts.DefaultLanguage),
		FullAudio:       opts.FullAudio,
		FinalAudio:      finalAudioArtifactForDocument(opts.FinalAudio),
		AudioTimeline:   opts.AudioTimeline,
		Overlay:         opts.Overlay,
	}), nil
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
func BuildScriptGenerationRuntime(cfg *config.Config, root *wiring.ComposeRoot, runRepo scriptgen.RunRepository, log *zap.Logger) (*scriptgen.Runner, error) {
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
	if root.Jobs == nil || root.Jobs.Service == nil {
		return nil, fmt.Errorf("build script generation runtime requires jobs service")
	}
	renderHandler := appjobs.HandlerFunc(func(ctx context.Context, j *job.Job, _ *appjobs.JobTools) (map[string]any, error) {
		var plan caprender.RenderPlan
		if err := json.Unmarshal(j.Payload, &plan); err != nil {
			return nil, fmt.Errorf("decode render.video payload: %w", err)
		}
		validated, err := caprender.ValidateRenderPlan(plan, filesystem.NewOS())
		if err != nil {
			return nil, err
		}
		if err := stockRenderer.RenderCanonicalPlan(ctx, validated); err != nil {
			return nil, err
		}
		return map[string]any{"render_job_id": j.ID, "output_path": plan.OutputPath}, nil
	})
	for _, jobType := range []string{videojob.TypeRender, videojob.TypeGenerate} {
		if err := root.Jobs.Service.RegisterHandler(jobType, renderHandler); err != nil {
			return nil, fmt.Errorf("register %s handler: %w", jobType, err)
		}
	}
	var renderEnqueuer scriptgen.RenderEnqueuer
	if strings.TrimSpace(cfg.External.RenderingGenQueueURL) != "" {
		queueEnqueuer, err := scriptgen.NewQueueRenderEnqueuer(renderinggen.New(cfg.External.RenderingGenQueueURL), filesystem.NewOS())
		if err != nil {
			return nil, fmt.Errorf("build renderinggen queue enqueuer: %w", err)
		}
		renderEnqueuer = queueEnqueuer
		log.Info("script generation render enqueuer wired to central RenderingGen queue", zap.String("endpoint", cfg.External.RenderingGenQueueURL))
	} else {
		jobEnqueuer, err := scriptgen.NewJobRenderEnqueuer(root.Jobs.Service, filesystem.NewOS())
		if err != nil {
			return nil, fmt.Errorf("build canonical render enqueuer: %w", err)
		}
		renderEnqueuer = jobEnqueuer
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
		scriptGenerationDocumentRenderer{},
	)
	runner.SetCombinedAudioRenderer(audioRenderer)
	runner.SetFinalAudioPublisher(newFinalAudioPublisher(root, log))
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
