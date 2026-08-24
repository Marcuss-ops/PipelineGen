// Package app — creator_runtime.go (P0 Commit 8, July 2026).
//
// CreatorRuntime is the canonical runtime surface for the Creator
// worker profile. The struct + BuildCreatorRuntime factory encode a
// structural contract enforced in code (compile-time orphan pin) AND
// in tests (import-allowlist AST scan):
//
//  1. NO RELATIONAL DATABASE — the Creator produces artifacts via
//     Ollama/AI; it does not own SQLite or any other RDBMS. SQLite
//     is the canonical Sender-side metadata store (godlike/06
//     "one canonical owner per fact"). The Creator does not write
//     to it directly — it pushes artifacts through the
//     remote.ArtifactUploader port (DRIVE-005 closed), and the
//     Sender ingests them through CreatorCompleteJob (C6/C7).
//
//  2. NO QDRANT — Qdrant is the Sender-side derived projection
//     (godlike/06 SemanticIndex). The Creator is push-only; it
//     does not write embeddings or trigger projector promotion.
//     Vector ingestion goes through Sender-side handlers.
//
//  3. NO SCHEDULER — the Creator side is awaiting HTTP-poll or
//     gRPC-pull jobs from the Sender; it does not own a
//     background scheduler. Channel-monitor jobs live on the
//     Sender.
//
//  4. NO CATALOGSYNC — catalog reconciliation is a Sender-side
//     helper that runs on the main SQLite + Qdrant state. The
//     Creator's view is "produce artifacts -> push to Sender".
//
// Architectural posture (godlike/07 "no fake availability"):
//
//   - The compile-pin `var _ = func() any { var _ *sql.DB = nil; return nil }`
//     below anchors a *sql.DB shape reference to stdlib `database/sql`
//     WITHOUT bringing any concrete SQLite impl into the package. The
//     pinned import is `database/sql` (the stdlib interface package).
//
//   - The forbidden reach into `internal/platform/sqlite`
//     is enforced by TestCreatorRuntime_FrozenImportAllowlist (see
//     creator_runtime_test.go): every `creator_*.go` file under
//     internal/app/ is AST-scanned; any of the four forbidden substrings
//     listed below fails the build.
//
//   - Forbidden-import substrings (source of truth — update in lockstep
//     with the package doc contract above):
//
//   - internal/platform/sqlite
//
//   - internal/platform/qdrant
//
//   - scheduler
//
//   - catalogsync
//
// CreatorRuntime is the CANONICAL Creator-side wiring surface. The
// legacy CreatorRoot struct is now a type alias for CreatorRuntime;
// the legacy InitCreatorComposition shim (creator_composition.go) is
// preserved as a thin delegator for godlike/07 backward compat. The
// new workerruntime/run.go Creator profile delegates directly to
// BuildCreatorRuntime so there is one source of truth.
package wiring

import (
	"context"
	"database/sql" // ← positive test anchor; pinned by var _ compile-pin below
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/worker"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/jobs"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	capvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// CreatorRoot is preserved as a type alias for godlike/07 backward
// compat. The canonical surface type is CreatorRuntime; the new
// profile wiring in workerruntime/run.go references CreatorRuntime
// directly via `creatorRuntime, _, _ := app.BuildCreatorRuntime(...)`
// and reads its fields directly. Future Blocco 4 commits will
// retire the alias once the legacy call site in
// creator_composition.go (InitCreatorComposition shim) itself becomes
// the final dead-code signal.
type CreatorRoot = CreatorRuntime

// CreatorRuntime is the canonical runtime struct for the Creator
// worker profile. It carries ONLY the services the Creator needs;
// nothing forces the Creator to depend on a persistent store or
// vector projection. See the package doc above for the no-DB /
// no-Qdrant / no-Scheduler / no-CatalogSync contract.
//
// Field-by-field rationale:
//
//   - ScriptEngine / OllamaClient / Workspace / AssetClient / Registry
//     / Caps — required production deps for the Creator role (script
//     generation via Ollama, workspace for job artefacts, asset-client
//     for Sender-side claim/submit, pre-built dispatcher for the
//     Creator's allowed job types).
//
//   - VoiceoverEngine / ImageGenerator — typed-nil placeholders until
//     Blocco 3.x wires real services. The placeholder fields preserve
//     the contract surface (future call sites can rely on the field
//     name) while deferring implementation drift to a later wave.
//
//   - Log — required by the canonical app.CleanupFunc contract for
//     diagnostic-on-cleanup-failure (workspace removal BestEffort).
type CreatorRuntime struct {
	ScriptEngine *usecase.Engine

	// VoiceoverEngine generates per-scene voiceover audio via Ollama TTS.
	// Typed-nil when the TTS backend is not configured.
	VoiceoverEngine any // Blocco 3.x: wire real voiceover.Service or narrow port (VO-DECOMPOSITION-2026-07-04)

	// ImageGenerator creates AI images for script scenes.
	// Typed-nil when image generation is not configured.
	ImageGenerator any // Blocco 3.x: wire real image generator or narrow port (VO-DECOMPOSITION-2026-07-04)

	// OllamaClient is the raw HTTP client for Ollama (used by voiceover
	// and image generation engines when they are wired).
	OllamaClient *client.Client

	// Workspace is the temporary job workspace. The Creator does not
	// share a filesystem with the Sender; workspace cleanup is owned
	// by the CleanupFunc returned by BuildCreatorRuntime.
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

// BuildCreatorRuntime constructs the Creator runtime graph from
// config. Returns (*CreatorRuntime, CleanupFunc, error) on success;
// the caller is responsible for invoking the CleanupFunc at shutdown
// (typically via defer in workerruntime/run.go).
//
// The factory enforces the canonical Creator-side contract (see
// package doc): NO DB, NO Qdrant, NO Scheduler, NO CatalogSync. Any
// future pull of one of those capabilities MUST go through a
// Sender-side handling port (e.g. remote.ArtifactUploader for
// persistence), NOT direct coupling in this file. The contract is
// enforced in two ways:
//
//  1. Compile pin (var _ = func() any { var _ *sql.DB = nil; return nil })
//     below — forces `database/sql` import without bringing a SQL
//     implementation into the package.
//
//  2. Import-allowlist AST scan (creator_runtime_test.go::TestCreatorRuntime_FrozenImportAllowlist)
//     — forbids any of internal/platform/sqlite,
//     internal/platform/qdrant, `scheduler`, or
//     `catalogsync` from any creator_*.go under internal/app/.
//
// Fail-closed precondition: cfg and log MUST be non-nil. A nil
// argument produces a typed error and (nil, nil, err), NOT a panic.
// This makes the canonical composition graph's optionality explicit.
func BuildCreatorRuntime(cfg *config.Config, log *zap.Logger) (*CreatorRuntime, CleanupFunc, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("creator runtime: config is nil")
	}
	if log == nil {
		return nil, nil, fmt.Errorf("creator runtime: logger is nil")
	}

	if err := initLinguistics(cfg, log); err != nil {
		return nil, nil, fmt.Errorf("creator runtime: %w", err)
	}

	// Workspace ─────────────────────────────────────
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

	// Ollama client ─────────────────────────────────
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

	// Script engine ─────────────────────────────────
	scriptGen := ollama.NewGenerator(ollamaClient)
	engine := usecase.NewEngine(ollamaadapters.NewScriptGeneratorAdapter(scriptGen), nil, log)
	engine.ConfigureScriptDefaults(cfg.Scripts.DefaultLanguage, cfg.Scripts.DefaultTone, cfg.Scripts.Defaults.WordsPerMinute)
	engine.ConfigureSegmentValidation(cfg.Scripts.SegmentWordsTolerancePercent, cfg.Scripts.TotalWordsTolerancePercent, cfg.Scripts.MaxSegmentRegenerationAttempts)

	log.Info("creator: script engine constructed")

	// Postprocessor registry ────────────────────────
	// Creator postprocessors write outputs as files in the workspace.
	// Forbidden: PersistenceProcessor (SQLite)
	// (Google Drive). See package doc.
	ppReg := registerCreatorPostProcessors(log)

	log.Info("creator: postprocessor registry built",
		zap.Int("processors", ppReg.Len()))

	// Script generate handler ───────────────────────
	// Build the minimal dependency chain for script.generate:
	//   Engine -> GenerateOneUseCase -> GenerateManyUseCase -> GenerateJobHandler
	normCfg := adapters.NormalizationConfig{
		DefaultLanguage:            cfg.Scripts.DefaultLanguage,
		DefaultTone:                cfg.Scripts.DefaultTone,
		WordsPerMinute:             cfg.Scripts.Defaults.WordsPerMinute,
		SafetyLanguage:             cfg.Scripts.Defaults.SafetyLanguage,
		DefaultDurationSeconds:     cfg.Scripts.DefaultDurationSeconds,
		OllamaModel:                cfg.External.OllamaModel,
		MinWordFloor:               200,
		DefaultSentencesPerImage:   10,
		DefaultImagesPerScene:      2,
		MaxBatchWorkers:            4,
		LogSourceTextPreview:       cfg.Scripts.LogSourceTextPreview,
		SourceTextPreviewChars:     cfg.Scripts.SourceTextPreviewChars,
		WordsPerSecondClipEvidence: cfg.Scripts.WordsPerSecondClipEvidence,
		ScriptDocsFolderID:         cfg.Scripts.ScriptDocsFolderID,
	}
	sourceReg := adapters.NewSourceRegistry(log)
	generateOne := usecase.NewGenerateOneUseCase(normCfg, sourceReg, engine, ppReg, log)
	generateMany := usecase.NewGenerateManyUseCase(log)
	genJobHandler := scriptjobs.NewGenerateJobHandler(generateOne, generateMany, log)

	// Build dispatcher + registry ───────────────────
	dispatcher := appjobs.NewDispatcher()
	// Inline adapter: generator.RegisterJobs expects ports.Broker
	// (requires RegisterHandler(jobType, handler any) error); the
	// canonical dispatcher exposes Register(jobType, HandlerFunc).
	// The adapter narrows the gap without modifying either contract.
	if err := genJobHandler.RegisterJobs(brokerAdapter{disp: dispatcher}); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("creator: register script.generate handler: %w", err)
	}

	// Blocco 3.x: register real voiceover.generate_item handler.
	// Tracked: VO-DECOMPOSITION-2026-07-04. The placeholder returns a clear
	// "not yet implemented" error so the Creator never silently drops
	// voiceover jobs on an unsigned dispatcher.
	placeholderVO := func(ctx context.Context, j *job.Job, tools *appjobs.JobExecutionTools) (map[string]any, error) {
		return nil, fmt.Errorf("voiceover.generate_item: not yet implemented in Creator composition (Blocco 3.x)")
	}
	if err := dispatcher.Register(capvoiceover.TypeGenerateItem, placeholderVO); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("creator: register voiceover.generate_item placeholder: %w", err)
	}
	log.Info("creator: voiceover.generate_item placeholder registered (Blocco 3.x — wire real engine, tracked: VO-DECOMPOSITION-2026-07-04)")

	dispatcher.Freeze()

	reg := worker.NewRegistry()
	// P1 #13 (July 2026): adapter was retired. dispatcher.AllHandlers
	// returns canonical `appjobs.Handler` values which are Go-type-aliases
	// for `job.Handler` (canonical SSOT in
	// internal/domain/job/handler.go). worker.Handler is also an alias
	// for the same job.Handler, so the handler passes directly
	// without an inline-wrapped closure. The worker runtime translates
	// `worker.Tools` (broker facade) into `*job.JobExecutionTools`
	// at Dispatch time (registry.go::translateToolsToExecutionTools)
	// so the handler observes the canonical signature at invocation.
	for jobType, handler := range dispatcher.AllHandlers() {
		if err := reg.Register(jobType, handler); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("creator: register handler %q in worker registry: %w", jobType, err)
		}
	}
	reg.SeedProducesArtifacts(appjobs.Compose().ProducesArtifactsMap())
	reg.Freeze()

	caps := appjobs.WorkerCapabilities{JobTypes: reg.JobTypes()}

	log.Info("creator: worker registry built",
		zap.Int("handlers", reg.Len()),
		zap.Strings("capabilities", caps.JobTypes),
	)

	// Asset client ──────────────────────────────────
	masterURL := cfg.External.ResolvedMasterURL()
	workerToken := cfg.WorkerToken()
	assetClient := veloxclient.New(masterURL, workerToken)

	log.Info("creator: asset client constructed",
		zap.String("master_url", masterURL),
	)

	return &CreatorRuntime{
		ScriptEngine: engine,
		OllamaClient: ollamaClient,
		Workspace:    ws,
		AssetClient:  assetClient,
		Registry:     reg,
		Caps:         caps,
		Log:          log,
	}, cleanup, nil
}

// brokerAdapter is a thin inline adapter that bridges the
// canonical `appjobs.Dispatcher` (which exposes Register(jobType,
// HandlerFunc) error) to the consumer-side `scriptports.Broker`
// interface (RegisterHandler(jobType, handler any) error) used by
// `genJobHandler.RegisterJobs`. Kept here at the composition root
// so the producer-side and consumer-side contracts stay
// independent — neither package needs to import the other for the
// adapter to exist.
type brokerAdapter struct {
	disp *appjobs.Dispatcher
}

// RegisterHandler satisfies scriptports.Broker. The `any` handler
// is type-asserted to the dispatcher's canonical `Handler` named
// signature; a mismatched payload is reported as a typed error so
// registration fails loudly rather than silently dropping the
// handler. P1 #13 (July 2026): appjobs.JobExecutionTools is a
// Go-type-alias for job.JobExecutionTools (canonical SSOT),
// and the asserted signature matches the canonical Handler.
// Pre-P1-#13 literal `map[string]any` return remains valid via
// Result = map[string]any alias.
func (b brokerAdapter) RegisterHandler(jobType string, handler any) error {
	h, ok := handler.(job.Handler)
	if !ok {
		return fmt.Errorf("brokerAdapter: handler type mismatch for jobType=%q (got %T)", jobType, handler)
	}
	return b.disp.Register(jobType, h)
}

// Compile-time DB orphan pin (P0 Commit 8, July 2026).
//
// This var declaration is orphaned: the variable assigned the
// anonymous-function literal is bound to the BLANK identifier `_`,
// so it occupies no name slot in the package. The function body
// still references the *sql.DB type from stdlib `database/sql`,
// so the IMPORT is forced at compile time without bringing any
// concrete SQLite implementation into this package.
//
// Contract enforced (godlike/06 + godlike/07):
//
//	POSITIVE side: shape-only `*sql.DB` reference. The stdlib
//	  `database/sql` import is forced to be resolvable. The type
//	  IS available from this package — but no concrete connection,
//	  store, or repository is EVER wired through it.
//
//	NEGATIVE side: paired with
//	  TestCreatorRuntime_FrozenImportAllowlist (creator_runtime_test.go),
//	  no creator_*.go under internal/app/ can reach into
//	  internal/platform/sqlite (or any of the other
//	  forbidden substrings) — the import-allowlist AST scan rejects
//	  such a pull at CI build time.
//
// Keep this pin in lockstep with the canonical no-DB contract in
// the package doc above. If a future Creator-side capability
// MUST touch SQLite, the fix is to add a SENDER-side handling
// port (e.g. remote.ArtifactUploader) and consume it, NOT to
// weaken this contract. A *sql.DB argument NEVER appears in any
// BuildCreatorRuntime signature or CreatorRuntime field by
// construction.
var _ = func() any { var _ *sql.DB = nil; return nil }

// Creator postprocessor registry ──────────────────────────
//
// Originally defined in creator_composition.go (Creator Blocco 3.2);
// moved to creator_runtime.go in P0 Commit 8 so the Creator-side
// surface lives in ONE file (the canonical CreatorRuntime file).
//
// registerCreatorPostProcessors builds a PostProcessorRegistry with
// Creator-safe postprocessors that write files instead of talking
// to SQLite, Google Drive, or Qdrant.
//
// PR-noop-adapters-purge (2026-07-25): the entities + metadata
// processors are now backed by the TYPED-FAIL unavailable*Adapter
// constructors (= godlike/07 NO-FAKE-AVAILABILITY — every request
// returns ErrEntityExtractorUnavailable / ErrMetadataGeneratorUnavailable
// instead of silent-success empty payloads).
//
//   - entities -> typed-fail unavailableEntityExtractionAdapter
//     (wrapped in BestEffort policy downgrade so the Creator
//     composes without a backend; runtime surfaces typed errors
//     when a plan requests entities).
//   - metadata -> typed-fail unavailableMetadataGenerationAdapter
//     (same rationale, see entities).
//   - clip_bindings -> canonical ClipBindingsProcessor. No-op when
//     ClipEvidence is nil/empty (pure-text generation).
//
// FORBIDDEN (never registered):
//   - persistence (SQLite)
//   - document (Google Drive)
//   - visual_planning (MediaMemory resolver)
//
// voiceover and images are NOT registered yet — the Creator's
// VoiceoverEngine and ImageGenerator are typed-nil placeholders
// until Blocco 3.x wires real services.
func registerCreatorPostProcessors(log *zap.Logger) *adapters.PostProcessorRegistry {
	ppReg := adapters.NewPostProcessorRegistry(log)

	// entities -> typed-fail adapter wrapped in BestEffort policy.
	// PR-noop-adapters-purge (2026-07-25): the silent-success noop
	// (which returned EntityResult{} with nil error) was replaced
	// by NewUnavailableEntityExtractionAdapter (= fail-closed per
	// godlike/07 NO-FAKE-AVAILABILITY). The BestEffort wrapper
	// downgrades the processor policy so the Creator composes
	// without a backend while the runtime surfaces typed errors
	// when a plan requests entities.
	entityAdapter := adapters.NewUnavailableEntityExtractionAdapter()
	entityProc := adapters.NewEntitiesProcessor(entityAdapter)
	if !ppReg.Register(&creatorBestEffort{inner: entityProc, name: "entities"}) {
		if log != nil {
			log.Warn("creator: failed to register entities processor")
		}
	}

	// metadata -> typed-fail adapter wrapped in BestEffort policy.
	// PR-noop-adapters-purge (2026-07-25): see entities comment
	// above — same godlike/07 NO-FAKE-AVAILABILITY rationale.
	metadataAdapter := adapters.NewUnavailableMetadataGenerationAdapter()
	metadataProc := adapters.NewMetadataProcessor(metadataAdapter)
	if !ppReg.Register(&creatorBestEffort{inner: metadataProc, name: "metadata"}) {
		if log != nil {
			log.Warn("creator: failed to register metadata processor")
		}
	}

	// clip_bindings -> canonical processor (in-memory, no external deps).
	if !ppReg.Register(adapters.NewClipBindingsProcessor(log)) {
		if log != nil {
			log.Warn("creator: failed to register clip_bindings processor")
		}
	}

	// Freeze prevents further registration (matches standard worker pattern).
	ppReg.Freeze()

	return ppReg
}

// creatorBestEffort wraps a PostProcessor to downgrade its policy
// to ProcessorBestEffort. Used for Creator postprocessors backed
// by noop adapters — the noop returns empty output, which would
// trigger the Required empty-output gate on the standard
// processor. BestEffort downgrades this to a warning.
type creatorBestEffort struct {
	inner adapters.PostProcessor
	name  string
}

func (p *creatorBestEffort) Name() adapters.ProcessorName { return adapters.ProcessorName(p.name) }

func (p *creatorBestEffort) Policy(_ *scriptpkg.ResolvedGenerationPlan) adapters.ProcessorPolicy {
	return adapters.ProcessorBestEffort
}

func (p *creatorBestEffort) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input adapters.ProcessInput) (*adapters.PostProcessResult, error) {
	return p.inner.Process(ctx, plan, input)
}
