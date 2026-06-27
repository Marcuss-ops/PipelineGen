// Package scripts — postprocessor_registry.go defines the
// PostProcessor interface and the PostProcessorRegistry that
// runs enabled processors in order. It replaces the monolithic
// Pipeline.Run with individually-testable processors.
//
// Each processor is opt-in: it runs only when its name appears
// in the plan's Postprocessors list. The registry respects the
// list order, which matches buildPostprocessorList ordering:
// entities → metadata → voiceover → images → document → persistence.
//
// PR 8 (June 2026):
//   - `scripts.VideoMetadata` is gone. The domain shape
//     `scriptpkg.VideoMetadata` (defined in
//     internal/domain/script/generation_result.go) is the
//     single canonical VideoMetadata. PostProcessArtifact.Metadata,
//     PostGenFunc's return type, PostGenResult.VideoMetadata,
//     and BuildGenerationDocumentHTML all reference the scriptpkg
//     type directly. The pre-PR-3 in-package alias + the
//     structural-copy bridge in processor_document.go are
//     retired.
//   - `pipeline_impl.go` (pre-PR-3 stub) deleted via `git rm`.
//   - Updated package comment block to reflect single source-of-truth.
//
// PR 7 (June 2026): added Freeze/IsFrozen so composition-time
// registration is rejected after wiring is complete.
//
// PR 5 (June 2026): the Process signature took a flat
// `script string` argument; PR 5.1 promoted it to a ProcessInput
// envelope carrying Text + WordCount + SpecScene + ModelUsed +
// CacheStatus + SourceTrace + PriorArtifacts.
//
// PR 3 (June 2026):
//   - the Process signature uses the canonical typed
//     *scriptpkg.ModelScriptOutputV1 directly (4 args).
//   - processors walk SpecScene.Scenes by reference and write
//     back into scene.Bindings.{Image, Voiceover}.
//   - PostProcessArtifact replaces the pre-PR-3 aggregate shape.
//   - PostGenFunc hoisted to a single canonical location here
//     (was previously duplicated in processor_entities.go and
//     processor_metadata.go).
//   - FolderResolver carried over from the pre-PR-3
//     pipeline_impl.go stub (required by processor_document.go
//   - DocumentsService); VideoMetadata is owned canonically
//     by internal/domain/script.
package scripts

import (
	"context"
	"fmt"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Shared types carried over from the pre-PR-3 pipeline_impl.go
// stub. VideoMetadata, PostProcessArtifact, and PostProcessorRegistry
// live here permanently; FolderResolver is kept on this file as its
// canonical home to avoid scattering (only DocumentProcessor +
// DocumentsService consume it, both in the scripts package).
//
// PR 8: the local VideoMetadata struct was retired. The canonical
// shape is scriptpkg.VideoMetadata (internal/domain/script).

// FolderResolver resolves a folder ID from an input name and a
// default root. Used by processor_document.go + DocumentsService.
type FolderResolver func(ctx context.Context, input, defaultRootID string) (string, error)

// ── PostProcessArtifact ──────────────────────────────────────────

// PostProcessArtifact is the canonical typed result of the
// post-generation phase. It carries the typed outputs of every
// processor in one bundle, consumed by generate_one_usecase.go
// to populate GenerationResult.Artifacts.
//
// Document, Metadata, Entities are populated by their respective
// processors. ScriptID is populated by PersistenceProcessor (and
// remains zero when persistence is disabled).
//
// PR 3 (June 2026): replaces the pre-PR-3 aggregate shape.
// The pre-PR-3 AlreadyPersisted flag (a single-writer replay
// signal) was dropped because downstream consumers do not need
// it — the persistence layer logs INFO on idempotency hit, and
// the postprocessing pipeline returns the canonical ScriptID
// either way.
type PostProcessArtifact struct {
	// Document holds the Google Doc link + ID, populated by
	// DocumentProcessor when "document" runs.
	Document *scriptpkg.DocumentArtifact

	// Metadata holds YouTube-style metadata (title, description,
	// tags per language), populated by MetadataProcessor when
	// "metadata" runs. PR 8: scriptpkg.VideoMetadata is the
	// canonical shape — no in-package alias.
	Metadata []scriptpkg.VideoMetadata

	// Entities holds the typed entity-extraction output, populated
	// by EntitiesProcessor when "entities" runs.
	Entities *scriptpkg.EntityResult

	// ScriptID is the persisted script-row ID, populated by
	// PersistenceProcessor when "persistence" runs.
	ScriptID int64
}

// ── PostProcessor interface ──────────────────────────────────────

// PostProcessor executes one post-generation phase. Each processor
// is opt-in — it only runs when its name is in the plan's
// Postprocessors list.
//
// PR 3 (June 2026): the second argument is the canonical typed
// *scriptpkg.ModelScriptOutputV1 (model). Processors that need to
// mutate scenes walk model.SpecScene.Scenes by reference and write
// directly into scene.Bindings.{Image, Voiceover}.
//
// The third argument is the shared accumulator. processors that
// depend on prior outputs (e.g. DocumentProcessor reads entities
// + metadata accumulated by earlier processors) read from
// accumulator. The accumulator is mutated by the registry into
// the canonical *PostProcessArtifact returned by Run.
//
// Returns this processor's typed *PostProcessArtifact contribution
// (only the fields this processor owns are populated). Returns nil
// when the processor has nothing to contribute (no-op).
type PostProcessor interface {
	// Name returns the processor identifier ("entities", "metadata",
	// "voiceover", "images", "document", "persistence").
	Name() string

	// Process executes the post-generation work.
	Process(
		ctx context.Context,
		plan *scriptpkg.ResolvedGenerationPlan,
		model *scriptpkg.ModelScriptOutputV1,
		accumulator *PostProcessArtifact,
	) (*PostProcessArtifact, error)
}

// ── Registry runtime ─────────────────────────────────────────────

// PostProcessorRegistry runs enabled processors in order.
// After Freeze() is called (post-composition), registration
// is rejected. The composition root calls Freeze() once all
// processors are registered.
type PostProcessorRegistry struct {
	processors map[string]PostProcessor
	frozen     bool
	mu         sync.RWMutex
	log        *zap.Logger
}

// NewPostProcessorRegistry creates an empty, unfrozen registry.
func NewPostProcessorRegistry(log *zap.Logger) *PostProcessorRegistry {
	return &PostProcessorRegistry{
		processors: make(map[string]PostProcessor),
		log:        log,
	}
}

// Register adds a processor. Returns false when proc is nil,
// the registry is nil, a processor with the same Name is already
// registered, or the registry is frozen.
//
// Duplicate registration is rejected (fail-closed).
func (r *PostProcessorRegistry) Register(proc PostProcessor) bool {
	if r == nil || proc == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		if r.log != nil {
			r.log.Warn("postprocessor registry: register called after freeze",
				zap.String("name", proc.Name()))
		}
		return false
	}

	name := proc.Name()
	if _, exists := r.processors[name]; exists {
		if r.log != nil {
			r.log.Warn("postprocessor registry: duplicate registration rejected",
				zap.String("name", name))
		}
		return false
	}

	r.processors[name] = proc
	if r.log != nil {
		r.log.Debug("postprocessor registered", zap.String("name", name))
	}
	return true
}

// Registered returns true when a processor with the given name
// is registered.
func (r *PostProcessorRegistry) Registered(name string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.processors[name]
	return ok
}

// Freeze prevents further registration. After freeze, all
// Register() calls return false. Idempotent.
func (r *PostProcessorRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	if r.log != nil {
		r.log.Debug("postprocessor registry: frozen",
			zap.Int("processors", len(r.processors)))
	}
}

// IsFrozen returns true after Freeze() has been called.
func (r *PostProcessorRegistry) IsFrozen() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// Len returns the number of registered processors.
func (r *PostProcessorRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.processors)
}

// Run executes every processor whose name appears in the plan's
// Postprocessors list, in list order. Each processor is run
// independently; a failure in one processor does not abort the
// remaining processors — errors are collected as warnings and
// the failing processor's output is skipped.
//
// Run returns the aggregate *PostProcessArtifact. Processors that
// mutate scenes (ImageProcessor, VoiceoverProcessor) write to
// model.SpecScene.Scenes[i].Bindings directly because the
// registry passes `model` by pointer. Processors that produce
// typed artefacts (entities, metadata, document, persistence)
// return their contribution through Process, which the registry
// merges into the accumulator.
//
// PR 3 (June 2026): replaces the pre-PR-3 merged-aggregate
// design. The accumulator is shared across processors so that
// processors depending on prior outputs (e.g. DocumentProcessor
// reads prior entities + metadata) see them at the canonical
// *PostProcessArtifact surface.
func (r *PostProcessorRegistry) Run(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
) (*PostProcessArtifact, error) {
	if r == nil {
		return &PostProcessArtifact{}, nil
	}
	r.mu.RLock()
	procs := make(map[string]PostProcessor, len(r.processors))
	for k, v := range r.processors {
		procs[k] = v
	}
	r.mu.RUnlock()

	if len(procs) == 0 {
		return &PostProcessArtifact{}, nil
	}
	if plan == nil {
		return &PostProcessArtifact{}, nil
	}

	result := &PostProcessArtifact{}
	var warnings []string
	failCount := 0
	totalRequested := len(plan.Postprocessors)

	for _, name := range plan.Postprocessors {
		proc, ok := procs[name]
		if !ok || proc == nil {
			failCount++
			warnings = append(warnings, fmt.Sprintf("postprocessor %q not registered", name))
			if r.log != nil {
				r.log.Warn("postprocessor not registered, skipping",
					zap.String("name", name),
					zap.String("item_id", plan.ID))
			}
			continue
		}

		if r.log != nil {
			r.log.Debug("running postprocessor",
				zap.String("name", name),
				zap.String("item_id", plan.ID))
		}

		ppResult, err := proc.Process(ctx, plan, model, result)
		if err != nil {
			failCount++
			warn := fmt.Sprintf("postprocessor %q failed: %v", name, err)
			warnings = append(warnings, warn)
			if r.log != nil {
				r.log.Error("postprocessor failed, continuing",
					zap.String("name", name),
					zap.String("item_id", plan.ID),
					zap.Error(err))
			}
			continue
		}

		// Merge processor contribution into the running accumulator.
		if ppResult != nil {
			mergePostProcessArtifact(result, ppResult)
		}
	}

	if r.log != nil && len(warnings) > 0 {
		r.log.Warn("postprocessors completed with warnings",
			zap.Int("warning_count", len(warnings)),
			zap.Int("failed", failCount),
			zap.Int("total", totalRequested),
			zap.Strings("warnings", warnings))
	}

	return result, nil
}

// mergePostProcessArtifact copies non-zero fields from a processor
// contribution into the aggregate accumulator.
func mergePostProcessArtifact(dst, src *PostProcessArtifact) {
	if src == nil {
		return
	}
	if src.Document != nil {
		dst.Document = src.Document
	}
	if len(src.Metadata) > 0 {
		dst.Metadata = src.Metadata
	}
	if src.Entities != nil {
		dst.Entities = src.Entities
	}
	if src.ScriptID > 0 {
		dst.ScriptID = src.ScriptID
	}
}

// ── PostGenFunc callback signature ────────────────────────────────

// PostGenFunc is the canonical callback signature used by
// EntitiesProcessor and MetadataProcessor to invoke the
// PostGenUseCase. The callback returns (entitiesJSON string,
// videoMetadata []VideoMetadata, err error); the entities/metadata
// processors consume the relevant slots and wrap their output
// into their respective PostProcessArtifact fields.
//
// PR 3 (June 2026): hoisted to a single canonical location here.
// Previously duplicated (with identical signatures) in
// processor_entities.go and processor_metadata.go; that duplication
// made wire-up unclear and obscured PR 3's ownership restructure.
//
// Deprecated-name kept for backward compat with existing
// wire-up names; a follow-up rename to entitiesAndMetadataFn
// is on the post-PR-3 cleanup list.
// PR 8: the return type uses scriptpkg.VideoMetadata directly.
// The pre-PR-8 in-package scripts.VideoMetadata alias is gone.
type PostGenFunc func(ctx context.Context, spec *scriptpkg.GenerationSpec, script string) (entitiesJSON string, videoMetadata []scriptpkg.VideoMetadata, err error)
