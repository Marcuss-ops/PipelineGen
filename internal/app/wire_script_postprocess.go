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
	"fmt"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

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
// Order follows the pre-PR3 block in wireScriptFlow. The freeze step
// remains in wireScriptFlow so the orchestrator owns the freeze +
// post-freeze invariant ordering (matches the postProcessorRegistry
// precedent in the canonical pipeline composer).
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

	// Fase 2 Spina Dorsale (July 2026): document processor removed.
	// Google Doc creation is now a separate document.generate
	// downstream job (see internal/domain/job/job.go TypeDocumentGenerate).

	// Persistence processor (PR 5: now the single persistence
	// owner; engine no longer writes to SQLite). Constructor takes
	// the logger for idempotency-hit / replay diagnostics.
	// Conditional on scriptsRepoAdapter != nil (composition caller
	// wires this from root.Repos.ScriptsRepo; missing-Repo is
	// caught earlier in wireScriptFlow).
	if scriptsRepoAdapter != nil {
		if !ppReg.Register(adapters.NewPersistenceProcessor(scriptsRepoAdapter, log)) {
			return fmt.Errorf("register persistence processor: composition bug or duplicate name")
		}
	}

	// Fase 2 Spina Dorsale (July 2026): image processor removed.
	// Scene images are now produced by a separate images.generate
	// downstream job (see internal/domain/job/job.go TypeImagesGenerate).

	// Fase 2 Spina Dorsale (July 2026): voiceover processor removed.
	// Voiceovers are now produced by a separate voiceover.generate
	// downstream job (see internal/domain/job/job.go TypeVoiceoverGenerate).

	// PR 3 (June 2026): Entities + Metadata are now
	// ProcessorRequired per the user spec. Adapters are nil-
	// tolerant at runtime (graceful-degradation) and the runtime
	// preflight will fail-fast when a plan requests these
	// processors without a real service wired through the
	// composition root. Always register here — the validator in
	// wire_script_adapters.go checks Required classification on
	// the post-freeze registry.
	entityAdapter := adapters.NewEntityExtractionAdapter(nil)
	if !ppReg.Register(adapters.NewEntitiesProcessor(entityAdapter)) {
		return fmt.Errorf("register entities processor: composition bug")
	}
	metadataAdapter := adapters.NewMetadataGenerationAdapter(nil, metaModel)
	if !ppReg.Register(adapters.NewMetadataProcessor(metadataAdapter)) {
		return fmt.Errorf("register metadata processor: composition bug")
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
			embedder := qdrant.NewTextEmbedderAdapter(embeddings.NewOllamaEmbedderAdapter(ollamaClient))
			stockSearchPort := qdrant.NewStockSearchAdapter(root.Process.QdrantSearcher, embedder, "text", log)
			if !ppReg.Register(adapters.NewStockAssociationProcessor(stockSearchPort, log)) {
				return fmt.Errorf("register stock_association processor: composition bug")
			}
			log.Info("StockAssociationProcessor wired (Qdrant + Ollama embedder)")
		}
	}

	return nil
}
