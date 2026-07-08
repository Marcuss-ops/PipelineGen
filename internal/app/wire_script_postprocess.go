// Package app — wire_script_postprocess.go.
//
// FASE 2.A PR3 (June 2026) split: the post-processor registration block
// moved out of wire_script.go. The block was previously inline in
// wireScriptFlow (pre-PR3 lines ~218-353), interleaving ppReg
// construction + 7 sequential Register calls + the freeze step into
// the orchestrator. Extracting it to a dedicated helper function
// returns the orchestrator to a pure-routing shape (use cases →
// job handler → handler → module.Register) and groups the canonical
// 5 postprocessors (persistence, entities, metadata,
// clip_bindings, stock_association) into a single
// testable seam.
//
// Package boundary: same `package app` as wire_script.go, exactly
// mirroring the wire_script_sources.go / wire_script_curation.go
// precedent. Caller is wireScriptFlow; the function takes the minimal
// 6-parameter contract.
//
// Cross-references:
//   - internal/app/wire_script.go: the caller (wireScriptFlow
//     invokes registerScriptPostProcessors immediately after ppReg
//     construction).
//   - internal/application/scripts/adapters: NewPostProcessorRegistry
//   - 7 New*Processor constructors + ProcessorRequired /
//     ProcessorBestEffort policy classification.
//   - internal/application/scripts/usecase: NewDocumentsService
//     (per processor's service-side collaborator).
//   - internal/infrastructure/qdrant: NewTextEmbedderAdapter +
//     NewStockSearchAdapter (stock_association processor wiring).
//   - internal/app/wire_script_adapters.go: composition-time
//     validators that operate on the post-freeze ppReg.
//   - internal/app/wire_script_curation.go: imageGenSvcAdapter
//     (composition-root-local adapter the image processor wraps).
package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/prometheus/client_golang/prometheus"

	"go.uber.org/zap"
)

// registerScriptPostProcessors initialises and registers every
// canonical postprocessor on the supplied registry. Each registration
// is gated on its required infrastructure dependency (DocClient for
// document, ImageService for images, VoiceoverService for voiceover,
// QdrantSearcher + OllamaClient for stock_association) — when the dep
// is absent at the call site the registration is silently skipped
// and the composition-time validator in wire_script_adapters.go
// (validateRequiredProcessors) surfaces the gap after the freeze.
//
// SCRIPTCONTRACT-2026-07-08 PR-1 canonical order (godlike/06 SSOT):
//
//	Persistence → Document → Image → Voiceover → Entities → Metadata →
//	ClipBindings → StockAssociation → ClipSearch
//
// Persistence is FIRST so no Drive-write side effect runs before the
// local SQLite row is locked. Pre-PR-1 order was Document →
// Persistence → ... which caused RETRY_WAIT 90% on a Drive-write-
// success + DB-persist-failure cascade (job retries re-triggered
// Drive writes on already-created artifacts; idempotency was only
// event-outbox, not artifact-publish). Forward-prevention gate:
// scripts/ci-architectural-checks.sh `Check 62` lands in PR-3 to
// enforce this order.
// The freeze step remains in wireScriptFlow so the orchestrator owns
// the freeze + post-freeze invariant ordering (matches the
// postProcessorRegistry precedent in the canonical pipeline composer).
//
// Returns the FIRST error encountered on a Register call so the
// orchestrator can wrap it with the "wireScriptFlow:" prefix and
// fail-closed per AGENTS.md Pattern 8. Composition-bug errors
// (duplicate name, malformed processor-ctor) propagate unchanged;
// caller-supplied dependencies that are missing at composition are
// silently skipped (graceful-degradation per spec).
func registerScriptPostProcessors(
	ppReg *adapters.PostProcessorRegistry,
	root *ComposeRoot,
	cfg *config.Config,
	log *zap.Logger,
	scriptsRepoAdapter adapters.ScriptRepository,
	metaModel string,
) error {
	if ppReg == nil {
		return fmt.Errorf("registerScriptPostProcessors: ppReg is nil (composition bug)")
	}

	// SCRIPTCONTRACT-2026-07-08 PR-1: PersistenceProcessor moved to
	// FIRST slot in this function body. The canonical reasoning is
	// that NO Drive-write side effect may run before the local
	// SQLite row is persisted — this prevents RETRY_WAIT-90% on a
	// Drive-write-success + DB-persist-failure cascade (post-fix,
	// retries on transient persistence failure leave the (now empty)
	// Drive side-effect space clean; idempotency is replay-safe via
	// text-hash triple per docs in this package's canonical
	// handover).
	//
	// Owner: PersistenceProcessor is the SOLE canonical owner of
	// scripts/script_assets SQLite row writes post FASE PR-5. The
	// engine layer was previously a second writer and was retired in
	// favor of this single-seam persistence. Constructor takes the
	// logger for idempotency-hit / replay diagnostics.
	//
	// Conditional on scriptsRepoAdapter != nil (composition caller
	// wires this from root.Repos.ScriptsRepo; missing-Repo is caught
	// earlier in wireScriptFlow via the godlike/07 typed-error
	// ErrRepoRequired envelope).
	if scriptsRepoAdapter != nil {
		if !ppReg.Register(adapters.NewPersistenceProcessor(scriptsRepoAdapter, log)) {
			return fmt.Errorf("register persistence processor: composition bug or duplicate name")
		}
	}

	// Google Doc creation inline registration (preserved: post-Persistence).
	if root.Drive != nil && root.Drive.DocClient != nil {
		docsSvc := usecase.NewDocumentsService(root.Drive.DocClient, log, cfg.Drive.ScriptsGenFolder())
		resolveFolder := func(ctx context.Context, input, defaultRootID string) (string, error) {
			input = strings.TrimSpace(input)
			if input == "" {
				return defaultRootID, nil
			}
			if len(input) >= 19 && len(input) <= 45 {
				isRawID := true
				for _, r := range input {
					if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
						isRawID = false
						break
					}
				}
				if isRawID {
					return input, nil
				}
			}
			if root.Drive.Publisher == nil {
				return defaultRootID, nil
			}
			parts := strings.FieldsFunc(input, func(r rune) bool {
				return r == '/' || r == '\\'
			})
			clean := make([]string, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					clean = append(clean, p)
				}
			}
			if len(clean) == 0 {
				return defaultRootID, nil
			}
			group := clean[0]
			subject := "_script"
			if len(clean) > 1 {
				group = strings.Join(clean[:len(clean)-1], "/")
				subject = clean[len(clean)-1]
			}
			folderID, err := root.Drive.Publisher.ResolveFolder(ctx, delivery.PublishRequest{
				Destination:        delivery.DestinationScript,
				Group:              group,
				Subject:            subject,
				RootFolderOverride: defaultRootID,
			})
			if err != nil {
				return "", err
			}
			return folderID, nil
		}
		if !ppReg.Register(adapters.NewDocumentProcessor(docsSvc, resolveFolder)) {
			return fmt.Errorf("register document processor: composition bug or duplicate name")
		}
		log.Info("DocumentProcessor (inline Google Docs) successfully registered")
	}

	// Inline Image generation processor (temporarily restored).
	if root.Domains != nil && root.Domains.ImageService != nil {
		imgGenSvc := &imageGenSvcAdapter{svc: root.Domains.ImageService}
		if !ppReg.Register(adapters.NewImageProcessor(imgGenSvc, log)) {
			return fmt.Errorf("register image processor: composition bug or duplicate name")
		}
		log.Info("ImageProcessor (inline scene images) successfully registered")
	}

	// Fase 2 Spina Dorsale (July 2026) — re-enabled 2026-07-08: the
	// inline voiceover postprocessor is RE-REGISTERED alongside the
	// separate voiceover.generate downstream job (Catena A P0 +
	// BLOC5.3 parent-child fanout, internal/domain/job/job.go
	// TypeVoiceoverGenerate). The two surfaces coexist: the inline
	// postprocessor runs inside the script.generate flow when
	// output.generate_voiceover=true + scenes are present; the
	// downstream voiceover.generate job handles explicit /api/media/
	// voiceover/generate invocations + the per-language fanout.
	//
	// The composition-time nil-guard mirrors the images/persistence
	// pattern: a missing voiceover service degrades to "postprocessor
	// not registered" warning at runtime (ProcessorBestEffort), NOT
	// a hard preflight rejection (the Fase 2 default policy).
	// godlike/06 SSOT: *voiceover.Service already satisfies the
	// VoiceoverService interface via the compile-time pin in
	// internal/application/scripts/adapters/processor_voiceover.go:193.
	if root.Domains != nil && root.Domains.VoiceoverService != nil {
		voProc := adapters.NewVoiceoverProcessor(root.Domains.VoiceoverService, log)
		if !ppReg.Register(voProc) {
			return fmt.Errorf("register voiceover processor: composition bug or duplicate name")
		}
		log.Info("VoiceoverProcessor (inline scene voiceovers) successfully registered")
	}

	// PR-ENTITY-EXTRACTOR-WIRING (July 2026): Entities backend wired
	// through the real ollama.Client via OllamaEntityExtractorAdapter.
	// When the Ollama client is available (root.AI.ScriptGen), the
	// adapter calls ExtractEntitiesFromScriptWithModel and populates
	// EntityResult with Persons/Concepts + Raw JSON (which carries
	// ArtlistPhrases for the InsightBuilder→SearchArtlistClips path).
	// Fallback to unavailable adapter when root.AI is not wired.
	var entityAdapter adapters.EntityExtractor
	if root.AI != nil && root.AI.ScriptGen != nil {
		if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
			entityAdapter = ollamaadapters.NewOllamaEntityExtractorAdapter(ollamaClient)
			log.Info("EntitiesProcessor wired with real Ollama backend (ollama.Client)")
		}
	}
	if entityAdapter == nil {
		entityAdapter = adapters.NewUnavailableEntityExtractionAdapter()
		log.Warn("EntitiesProcessor: Ollama backend not available; falling back to unavailable adapter (entities will produce warnings)")
	}
	if !ppReg.Register(adapters.NewEntitiesProcessor(entityAdapter)) {
		return fmt.Errorf("register entities processor: composition bug")
	}
	// PR-METADATA-GENERATOR-WIRING (July 2026): Metadata backend wired
	// through the real ollama.Generator via OllamaMetadataGeneratorAdapter.
	// When the Ollama generator is available (root.AI.ScriptGen), the
	// adapter calls GenerateVideoMetadataWithModel and populates
	// VideoMetadata with Title/Description/Tags.
	// Fallback to unavailable adapter when root.AI is not wired.
	var metadataAdapter adapters.MetadataGenerator
	if root.AI != nil && root.AI.ScriptGen != nil {
		metadataAdapter = ollamaadapters.NewOllamaMetadataGeneratorAdapter(root.AI.ScriptGen)
		log.Info("MetadataProcessor wired with real Ollama backend (ollama.Generator)")
	}
	if metadataAdapter == nil {
		metadataAdapter = adapters.NewUnavailableMetadataGenerationAdapter()
		log.Warn("MetadataProcessor: Ollama backend not available; falling back to unavailable adapter (metadata will produce warnings)")
	}
	if !ppReg.Register(adapters.NewMetadataProcessor(metadataAdapter)) {
		return fmt.Errorf("register metadata processor: composition bug")
	}

	// PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP2 (2026-08-08):
	// TranslationProcessor wires the typed ports.ScriptTranslator
	// (canonical Italian→English LLM translator) + the typed
	// ports.TranslationMetricsRecorder (canonical Prometheus
	// counter adapter). Both are nil-tolerant: a missing translator
	// degrades to "translator_missing" warning + bounded-reason
	// metric (BestEffort policy); the metrics adapter is constructed
	// inline as a zero-size struct (no per-instance state needed).
	//
	// The translator source is root.AI.OllamaTranslator (the
	// canonical translation.TranslatorFunc already wired in
	// build_bundles_voiceover.go for the voiceover.Promo surface).
	// When the Ollama backend is not available, the processor is
	// skipped (composition caller contract: BestEffort
	// postprocessors may be absent without failing composition).
	//
	// DI pattern: the canonical TranslateScriptSpec + ClassifyReason
	// functions live in the usecase package (adapters → usecase is
	// a forbidden import edge per the documents_usecase.go cycle).
	// The composition root — which already imports both adapters
	// AND usecase — passes the function values at wiring time. This
	// breaks the cycle while preserving the pure-function contract.
	//
	// Registration slot: between metadata and clip_bindings per the
	// canonical EXECUTION order in CanonicalProcessorNames (so
	// downstream ClipBindings sees the translated SpecScene).
	if root.AI != nil && root.AI.OllamaTranslator != nil {
		// Adapter closure 1: *translation.OllamaTranslator → ports.ScriptTranslator.
		// The canonical method signature is (ctx, text, targetLanguage) →
		// (string, error) per translation.TranslatorFunc; the port's
		// NewScriptTranslatorFromFunc adapter wraps it byte-equivalently.
		translatorPort := ports.NewScriptTranslatorFromFunc(root.AI.OllamaTranslator.TranslateText)
		// PR-TRANSLATE-SCRIPT-SPEC CR#1+#2+#3 review-fix (2026-08-08):
		// the metrics adapter ctor now takes a prometheus.Registerer
		// (per-adapter registry) + returns (*Adapter, error). The
		// production composition root uses prometheus.DefaultRegisterer
		// so the counter is scrapeable on /metrics; the typed-error
		// return surfaces a fail-closed composition bug at boot
		// (e.g. nil-registry or double-registration) instead of
		// silently degrading to a no-op counter.
		metricsPort, mErr := observability.NewTranslationMetricsAdapter(prometheus.DefaultRegisterer)
		if mErr != nil {
			return fmt.Errorf("register translation processor: metrics adapter: %w", mErr)
		}
		// CR#2 review-fix: the processor now consumes typed
		// ports.TranslationUseCase + ports.TranslationReasonClassifier
		// (NOT bare function values) per godlike/06 Pattern 0. The
		// composition root — which already imports both adapters AND
		// usecase — wires the thin struct adapters via
		// NewTranslationUseCaseAdapter + NewTranslationReasonClassifierAdapter
		// (declared in usecase/translation.go). This breaks the
		// adapters → usecase import cycle while preserving the
		// canonical 1-method Pattern 0 port convention.
		transProc := adapters.NewTranslationProcessor(
			translatorPort,
			metricsPort,
			usecase.NewTranslationUseCaseAdapter(),
			usecase.NewTranslationReasonClassifierAdapter(),
			log,
		)
		if !ppReg.Register(transProc) {
			return fmt.Errorf("register translation processor: composition bug")
		}
		log.Info("TranslationProcessor wired (OllamaTranslator + Prometheus metrics adapter)")
	} else {
		log.Warn("TranslationProcessor: OllamaTranslator not available; postprocessor not registered (translation requests will produce warnings)")
	}

	// PR 7 (June 2026): register ClipBindingsProcessor so the
	// postprocessor walk produces ONE canonical set of scene-clip
	// bindings consumed by both the Google Doc builder (via
	// DocumentProcessor) AND the JSON response writer (via
	// result.Output.SpecScene.Scenes). BestEffort policy.
	if !ppReg.Register(adapters.NewClipBindingsProcessor(log)) {
		return fmt.Errorf("register clip_bindings processor: composition bug")
	}

	// Stock association processor — wraps Qdrant searcher for
	// per-scene vector search over stock-indexed assets. BestEffort
	// policy: a missing or failing stock search does not block the
	// pipeline. Falls back to the scene's Clip.DriveLink when no
	// stock match is found. Reuses the ollama client getter shape
	// from the SourceSearch wiring above (root.AI.ScriptGen.GetClient()).
	if root.AI != nil && root.AI.ScriptGen != nil &&
		root.Process != nil && root.Process.QdrantSearcher != nil {
		if ollamaClient := root.AI.ScriptGen.GetClient(); ollamaClient != nil {
			embedder := search.NewTextEmbedderAdapter(embeddings.NewOllamaEmbedderAdapter(ollamaClient))
			stockSearchPort := search.NewStockSearchAdapter(root.Process.QdrantSearcher, embedder, "text", log)
			if !ppReg.Register(adapters.NewStockAssociationProcessor(stockSearchPort, log)) {
				return fmt.Errorf("register stock_association processor: composition bug")
			}
			log.Info("StockAssociationProcessor wired (Qdrant + Ollama embedder)")
		}
	}

	// PR-CLIP-SEARCH-WIRING (July 2026): ClipSearchProcessor wired
	// through the real OllamaTranslator (TranslationPort) so that
	// artlist_phrases extracted by the EntitiesProcessor trigger
	// actual Artlist clip searches (Italian→English translation +
	// Qdrant hybrid search). The adapter wraps usecase.SearchArtlistClips
	// with a rich ClipServices:
	//
	//   - TranslationPort (load-bearing): Italian→English translation
	//   - DriveSvc: validates Drive folder existence via FileIsNotTrashed
	//   - JobsSvc: enqueues background media.artlist jobs for unmatched phrases
	//   - AssocSvc: resolves Drive folders for artlist phrases (nil-safe —
	//     currently unavailable post-package-removal)
	//   - ArtlistFolder: root folder for background artlist job enqueue
	//   - MetadataModel + Logger: for translation model policy + diagnostics
	//
	// Fallback to unavailable adapter when root.AI.OllamaTranslator
	// is not wired.
	var clipSearchAdapter adapters.ArtlistClipSearcher
	if root.AI != nil && root.AI.OllamaTranslator != nil {
		clipSvc := usecase.ClipServices{
			TranslationPort: root.AI.OllamaTranslator,
			MetadataModel:   metaModel,
			Logger:          log,
			ArtlistFolder:   cfg.Drive.ArtlistFolder(),
		}

		// DriveSvc: wrap driveUploader.FileIsNotTrashed for
		// per-folder validation in resolveArtlistFolderForPhrase.
		// nil is safe — the function skips the validation step
		// when DriveSvc is nil.
		if root.Drive != nil && root.Drive.driveUploader != nil {
			clipSvc.DriveSvc = &driveCheckServiceAdapter{
				up: root.Drive.driveUploader,
			}
		}

		// JobsSvc: wrap root.Jobs.Service.Enqueue so unmatched
		// artlist phrases trigger background media.artlist jobs.
		// nil is safe — enqueueArtlistBackgroundJob returns early
		// when JobsSvc is nil.
		if root.Jobs != nil && root.Jobs.Service != nil {
			clipSvc.JobsSvc = &jobsEnqueueServiceAdapter{
				svc: root.Jobs.Service,
			}
		}

		// AssocSvc: the canonical association service is currently
		// unavailable (package removed from remote, per
		// build_bundles_domain.go). Nil is safe —
		// resolveArtlistFolderForPhrase returns empty
		// FolderLink/Name/ID when AssocSvc is nil.
		if root.Domains != nil && root.Domains.AssocService != nil {
			clipSvc.AssocSvc = root.Domains.AssocService
		}

		clipSearchAdapter = &artlistClipSearchAdapter{
			svc: clipSvc,
		}
		log.Info("ClipSearchProcessor wired with rich ClipServices",
			zap.Bool("drive_svc", clipSvc.DriveSvc != nil),
			zap.Bool("jobs_svc", clipSvc.JobsSvc != nil),
			zap.Bool("assoc_svc", clipSvc.AssocSvc != nil),
			zap.Bool("artlist_folder", clipSvc.ArtlistFolder != ""),
		)
	}
	if clipSearchAdapter == nil {
		clipSearchAdapter = adapters.NewUnavailableArtlistClipSearcher()
		log.Warn("ClipSearchProcessor: OllamaTranslator not available; falling back to unavailable adapter (clip_search will produce empty results)")
	}
	if !ppReg.Register(adapters.NewClipSearchProcessor(clipSearchAdapter)) {
		return fmt.Errorf("register clip_search processor: composition bug")
	}

	return nil
}

// artlistClipSearchAdapter wraps usecase.SearchArtlistClips into the
// adapters.ArtlistClipSearcher port. This adapter lives in the
// composition root (NOT in the adapters package) to avoid a circular
// import: adapters cannot import usecase.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this is the canonical
// SOLE adapter between ArtlistClipSearcher and SearchArtlistClips.
type artlistClipSearchAdapter struct {
	svc usecase.ClipServices
}

// SearchClips satisfies adapters.ArtlistClipSearcher.
func (a *artlistClipSearchAdapter) SearchClips(ctx context.Context, title string, phrases []string) []adapters.ArtlistClipMatch {
	if a == nil {
		return nil
	}
	suggestions := usecase.SearchArtlistClips(ctx, a.svc, title, phrases)
	if len(suggestions) == 0 {
		return nil
	}

	// Convert usecase.ScriptArtlistClipSuggestion → adapters.ArtlistClipMatch.
	// adapters cannot import usecase types directly, so we convert at
	// the composition-root boundary.
	matches := make([]adapters.ArtlistClipMatch, 0, len(suggestions))
	for _, s := range suggestions {
		m := adapters.ArtlistClipMatch{
			Phrase:           s.Phrase,
			FolderLink:       s.FolderLink,
			FolderName:       s.FolderName,
			FolderID:         s.FolderID,
			TranslationError: s.TranslationError,
		}
		for _, c := range s.Clips {
			m.ClipNames = append(m.ClipNames, c.Name)
			m.ClipDriveLinks = append(m.ClipDriveLinks, c.DriveLink)
		}
		matches = append(matches, m)
	}
	return matches
}

var _ adapters.ArtlistClipSearcher = (*artlistClipSearchAdapter)(nil)

// ── ClipServices adapter structs (composition-root-local) ─────────────────

// driveCheckServiceAdapter wraps drive.Uploader.FileIsNotTrashed into
// usecase.DriveCheckService. This adapter lives in the composition
// root (NOT in the adapters or usecase packages) to avoid import cycles.
//
// godlike/06 SSOT: this is the canonical SOLE adapter between the
// drive.Uploader and the usecase.DriveCheckService port.
type driveCheckServiceAdapter struct {
	up interface {
		FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
	}
}

// FileIsNotTrashed satisfies usecase.DriveCheckService.
func (a *driveCheckServiceAdapter) FileIsNotTrashed(ctx context.Context, fileID string) (bool, error) {
	if a == nil || a.up == nil {
		return false, fmt.Errorf("driveCheckServiceAdapter: drive uploader not wired")
	}
	return a.up.FileIsNotTrashed(ctx, fileID)
}

var _ usecase.DriveCheckService = (*driveCheckServiceAdapter)(nil)

// jobsEnqueueServiceAdapter wraps appjobs.Service.Enqueue into
// usecase.JobEnqueueService. This adapter lives in the composition
// root (NOT in the adapters or usecase packages) to avoid import cycles.
//
// The adapter bridges the typed appjobs.Service.Enqueue(ctx,
// *job.EnqueueRequest) (*job.Job, error) to the interface-based
// usecase.JobEnqueueService.Enqueue(ctx, interface{}) (interface{}, error)
// expected by the artlist background job enqueue path.
//
// godlike/06 SSOT: this is the canonical SOLE adapter between the
// jobs.Service and the usecase.JobEnqueueService port.
type jobsEnqueueServiceAdapter struct {
	svc interface {
		Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
	}
}

// Enqueue satisfies usecase.JobEnqueueService.
func (a *jobsEnqueueServiceAdapter) Enqueue(ctx context.Context, req interface{}) (interface{}, error) {
	if a == nil || a.svc == nil {
		return nil, fmt.Errorf("jobsEnqueueServiceAdapter: jobs service not wired")
	}
	typedReq, ok := req.(*job.EnqueueRequest)
	if !ok {
		return nil, fmt.Errorf("jobsEnqueueServiceAdapter: req is %T, want *job.EnqueueRequest", req)
	}
	return a.svc.Enqueue(ctx, typedReq)
}

var _ usecase.JobEnqueueService = (*jobsEnqueueServiceAdapter)(nil)
