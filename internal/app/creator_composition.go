// Package app — creator_composition.go (Creator Blocco 3.1, July 2026).
//
// InitCreatorComposition builds a minimal ComposeRoot-like graph for the
// Creator worker profile. Unlike InitWorkerComposition, this path:
//
//   - Does NOT open databases (no SQLite, no migrations).
//   - Does NOT construct Drive, Qdrant, Repos, or the full ComposeRoot.
//   - Builds ONLY the services the Creator needs: Ollama client → script
//     engine → script.generate handler + voiceover.generate_item handler.
//   - Uses a temporary workspace under /tmp/pipelinegen/creator/.
//   - Wires a remote asset client (veloxclient) for talking to the Sender.
//
// The returned CreatorRoot carries a pre-built worker.Registry so run.go
// can feed it directly to worker.NewRunner without building a registry
// from ComposeRoot.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/jobs"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"

	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// CreatorRoot is the minimal assembled graph for the Creator worker.
// It carries only the services the Creator needs — no DB, Drive, Qdrant,
// or Repos.
type CreatorRoot struct {
	// ScriptEngine is the canonical script generation engine backed by Ollama.
	ScriptEngine *usecase.Engine

	// VoiceoverEngine generates per-scene voiceover audio via Ollama TTS.
	// Typed-nil when the TTS backend is not configured.
	VoiceoverEngine interface{} // TODO (Blocco 3.x): wire real voiceover.Service or narrow port

	// ImageGenerator creates AI images for script scenes.
	// Typed-nil when image generation is not configured.
	ImageGenerator interface{} // TODO (Blocco 3.x): wire real image generator or narrow port

	// OllamaClient is the raw HTTP client for Ollama (used by voiceover
	// and image generation engines when they are wired).
	OllamaClient *client.Client

	// Workspace is the temporary job workspace. The Creator does not
	// share a filesystem with the Sender; workspace cleanup is owned
	// by the CleanupFunc returned by InitCreatorComposition.
	Workspace *worker.Workspace

	// AssetClient is the HTTP client for talking to the Sender broker.
	// Jobs are claimed and results are submitted through this client.
	AssetClient *veloxclient.Client

	// Registry is the pre-built worker.Registry with Creator job handlers
	// (script.generate + voiceover.generate_item) already registered.
	Registry *worker.Registry

	// Caps are derived from the Registry (single source of truth).
	Caps appjobs.WorkerCapabilities

	Log *zap.Logger
}

// InitCreatorComposition builds the Creator service graph from config.
// Returns the assembled CreatorRoot, a CleanupFunc that removes the
// temporary workspace, and an error if any required service fails to
// initialise.
//
// The returned CleanupFunc must be called exactly once when the worker
// shuts down (typically via defer in run.go).
func InitCreatorComposition(cfg *config.Config, log *zap.Logger) (*CreatorRoot, CleanupFunc, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("creator composition: config is nil")
	}
	if log == nil {
		return nil, nil, fmt.Errorf("creator composition: logger is nil")
	}

	// ── Workspace ─────────────────────────────────────────────────
	workspaceRoot := filepath.Join(os.TempDir(), "pipelinegen", "creator")
	ws, err := worker.NewWorkspace(workspaceRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("creator workspace: %w", err)
	}
	cleanup := func() {
		if rmErr := os.RemoveAll(workspaceRoot); rmErr != nil {
			log.Warn("creator workspace cleanup failed", zap.Error(rmErr))
		}
	}

	// ── Ollama client ─────────────────────────────────────────────
	ollamaClient := client.NewClient(
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		cfg.External.OllamaTimeoutSeconds,
	)
	ollamaClient.SetNvidiaConfig(
		cfg.External.UseNvidiaForLLM,
		cfg.External.NvidiaAPIKey,
		cfg.External.NvidiaLLMModel,
	)

	log.Info("creator: Ollama client constructed",
		zap.String("ollama_url", cfg.External.OllamaURL),
		zap.String("ollama_model", cfg.External.OllamaModel),
	)

	// ── Script engine ─────────────────────────────────────────────
	scriptGen := ollama.NewGenerator(ollamaClient)
	engine := usecase.NewEngine(scriptGen, nil, log)

	log.Info("creator: script engine constructed")

	// ── Script generate handler ───────────────────────────────────
	// Build the minimal dependency chain for script.generate:
	//   Engine → GenerateOneUseCase → GenerateManyUseCase → GenerateJobHandler
	normCfg := adapters.NormalizationConfig{
		DefaultLanguage:        "en",
		DefaultTone:            "professional",
		DefaultDurationSeconds: 600,
		OllamaModel:            cfg.External.OllamaModel,
		MinWordFloor:           200,
		DefaultSentencesPerImage: 10,
		DefaultImagesPerScene:    2,
		MaxBatchWorkers:          4,
	}
	sourceReg := adapters.NewSourceRegistry(log)
	generateOne := usecase.NewGenerateOneUseCase(normCfg, sourceReg, engine, nil, log)
	generateMany := usecase.NewGenerateManyUseCase(generateOne, log)
	genJobHandler := scriptjobs.NewGenerateJobHandler(generateOne, generateMany, normCfg, log)

	// ── Build dispatcher + registry ───────────────────────────────
	dispatcher := appjobs.NewDispatcher()
	if err := genJobHandler.RegisterJobs(dispatcher); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("creator: register script.generate handler: %w", err)
	}

	// TODO (Blocco 3.x): register real voiceover.generate_item handler.
	// The placeholder returns a clear "not yet implemented" error so the
	// Creator never silently drops voiceover jobs on an unsigned dispatcher.
	placeholderVO := func(ctx context.Context, j *domainjob.Job, tools *appjobs.JobTools) (map[string]any, error) {
		return nil, fmt.Errorf("voiceover.generate_item: not yet implemented in Creator composition (Blocco 3.x)")
	}
	if err := dispatcher.Register(domainjob.TypeVoiceoverGenerateItem, placeholderVO); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("creator: register voiceover.generate_item placeholder: %w", err)
	}
	log.Info("creator: voiceover.generate_item placeholder registered (TODO Blocco 3.x — wire real engine)")

	dispatcher.Freeze()

	reg := worker.NewRegistry()
	for jobType, handler := range dispatcher.AllHandlers() {
		if err := reg.Register(jobType, handler); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("creator: register handler %q in worker registry: %w", jobType, err)
		}
	}
	reg.Freeze()

	caps := appjobs.WorkerCapabilities{JobTypes: reg.Capabilities()}

	log.Info("creator: worker registry built",
		zap.Int("handlers", reg.Len()),
		zap.Strings("capabilities", caps.JobTypes),
	)

	// ── Asset client ──────────────────────────────────────────────
	masterURL := cfg.External.ResolvedMasterURL()
	workerToken := cfg.WorkerToken()
	assetClient := veloxclient.New(masterURL, workerToken)

	log.Info("creator: asset client constructed",
		zap.String("master_url", masterURL),
	)

	root := &CreatorRoot{
		ScriptEngine: engine,
		OllamaClient: ollamaClient,
		Workspace:    ws,
		AssetClient:  assetClient,
		Registry:     reg,
		Caps:         caps,
		Log:          log,
	}
	return root, cleanup, nil
}
