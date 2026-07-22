// Package adapters — postprocessor_composite.go: core registry infrastructure.
//
// Extracted from postprocessor_registry.go (July 2026).
// Owns: PostProcessor interface, PostProcessorRegistry struct + all methods,
// ProcessorPolicy, defaultPolicyByName, DefaultPolicyFor.
//
// PR-COMPOSITE-SPLIT (July 2026): decomposed into 3 files per AGENTS.md
// Pattern 5:
//
//	postprocessor_composite.go       — types + constructor + simple methods
//	                                    (this file)
//	postprocessor_composite_run.go   — Run method
//	postprocessor_composite_merge.go — mergePostProcessResult helper
package adapters

import (
	"context"
	"strings"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ProcessorPolicy classifies a postprocessor's failure mode for
// composition and preflight decisions.
//
// PR 2 (June 2026): introduced alongside the preflight gate so the
// pipeline reflects on which processors MUST succeed (Required) and
// which can degrade gracefully (BestEffort). The composition root
// reads each processor's policy via PostProcessor.Policy(plan); the
// registry stores the mapping at Register time so missing-registered
// cases are assessable at preflight without needing the proc itself.
type ProcessorPolicy string

const (
	// ProcessorRequired marks a processor whose missing-registered
	// status is a composition-level failure (composition refuses
	// to start) AND whose runtime error or empty output causes the
	// overall Run to return a non-nil error. Used by persistence
	// (the canonical source of ScriptID) and document (a deliverable
	// requested by callers).
	ProcessorRequired ProcessorPolicy = "required"

	// ProcessorBestEffort marks a processor whose missing-registered
	// status is a non-fatal warning (composition continues) AND
	// whose runtime error or empty output is a warning rather than
	// a hard failure. Used by images / voiceover / entities /
	// metadata — Callers can opt in via OutputSpec but a missing
	// service must not abort the script generation.
	ProcessorBestEffort ProcessorPolicy = "best_effort"
)

// defaultPolicyByName is the canonical static mapping from a
// postprocessor name to its policy. Both ValidateRequested (for
// missing-registered names) and validateRequiredProcessors (in
// wire_script.go) consult this map so the runtime gate matches
// the composition-time classification. A successful Register()
// call overrides the default by recording the proc.Policy(nil)
// value into `r.policies[name]` at register time.
//
// PR 2 (June 2026): Persistence and Document are Required —
// the canonical script-table writer and the doc-creation
// deliverable. Images / Voiceover are BestEffort per the user
// spec ("configurabile").
//
// PR 3 (June 2026): Entities and Metadata are promoted to
// ProcessorRequired. The PR 3 spec mandates that composition-
// time wiring must surface a hard failure when either processor
// is requested but the corresponding service is unavailable, and
// that the runtime preflight must reject plans requesting them
// without a registered adapter. The "best_effort or required
// based on payload" future work is resolved statically for now —
// both processors are unconditionally Required. Future PRs may
// restore payload-conditional semantics by overriding the
// processor's Policy(plan) method to inspect plan.OutputFmt or
// related fields; until that lands, this map is the source of
// truth.
//
// Future PRs promoting a BestEffort to Required (or vice versa)
// MUST update both `defaultPolicyByName` and `requiredProcessorNames`
// in wire_script.go so the two stay in sync.
// defaultPolicyByName is the canonical static mapping from a
// postprocessor name to its policy.
//
// Fase 2 Spina Dorsale (July 2026): "document", "images", and
// "voiceover" are downgraded to BestEffort. These processors are
// being removed from the script pipeline — they will become
// independent downstream jobs (document.generate, images.generate,
// voiceover.generate). Until the full CUTOVER phase, they remain
// registered but with BestEffort policy so legacy callers that
// still request them do not trigger a hard preflight failure.
var defaultPolicyByName = map[ProcessorName]ProcessorPolicy{
	ProcessorPersistence:    ProcessorRequired,
	ProcessorImages:         ProcessorBestEffort, // Fase 2: downgraded (→ images.generate job)
	ProcessorVoiceover:      ProcessorBestEffort, // Fase 2: downgraded (→ voiceover.generate job)
	ProcessorEntities:       ProcessorRequired,
	ProcessorMetadata:       ProcessorRequired,
	ProcessorClipSearch:     ProcessorBestEffort, // PR-CLIP-SEARCH-WIRING (July 2026): enrichment, not a hard gate
	ProcessorTranslation:    ProcessorBestEffort, // SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-2: translation enrichment, not a hard gate; defaults to BestEffort because the canonical composition root at internal/app/wire_script_postprocess.go::registerScriptPostProcessors silently skips registration when OllamaTranslator is nil
	ProcessorClipBindings:   ProcessorBestEffort, // SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-2: clip-binding enrichment, not a hard gate; same silent-skip-on-missing-wiring pattern as Translation
	ProcessorVisualPlanning: ProcessorBestEffort, // SCRIPT-PIPELINE-DECOUPLING-2026-07-09 PR-2: visual-planning lookup, not a hard gate; nil MediaMemory resolver fails-open per the canonical composition root
}

// DefaultPolicyFor returns the canonical default policy for a
// named postprocessor. Returns empty string for unknown names —
// callers MUST treat unknown names as a hard fail or warn per
// their own classification logic.
func DefaultPolicyFor(name ProcessorName) ProcessorPolicy {
	return defaultPolicyByName[name]
}

// PostProcessor executes one post-generation phase.
//
// PR 5 (June 2026): the second argument changed from `script string`
// to `input ProcessInput`. The envelope carries the canonical
// output text plus typed fields required by individual processors.
type PostProcessor interface {
	Name() ProcessorName
	Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy
	Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error)
}

// PostProcessorRegistry runs enabled processors in order.
type PostProcessorRegistry struct {
	processors map[ProcessorName]PostProcessor
	policies   map[ProcessorName]ProcessorPolicy
	frozen     bool
	mu         sync.RWMutex
	log        *zap.Logger
}

// NewPostProcessorRegistry creates an empty, unfrozen registry.
func NewPostProcessorRegistry(log *zap.Logger) *PostProcessorRegistry {
	return &PostProcessorRegistry{
		processors: make(map[ProcessorName]PostProcessor),
		policies:   make(map[ProcessorName]ProcessorPolicy),
		log:        log,
	}
}

// Register adds a processor.
func (r *PostProcessorRegistry) Register(proc PostProcessor) bool {
	if r == nil || proc == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		if r.log != nil {
			r.log.Warn("postprocessor registry: register called after freeze",
				zap.String("name", string(proc.Name())))
		}
		return false
	}

	name := proc.Name()
	if _, exists := r.processors[name]; exists {
		if r.log != nil {
			r.log.Warn("postprocessor registry: duplicate registration rejected",
				zap.String("name", string(name)))
		}
		return false
	}

	policy := proc.Policy(nil)
	r.processors[name] = proc
	r.policies[name] = policy
	if r.log != nil {
		r.log.Debug("postprocessor registered",
			zap.String("name", string(name)),
			zap.String("policy", string(policy)))
	}
	return true
}

// Registered returns true when a processor with the given name is registered.
func (r *PostProcessorRegistry) Registered(name ProcessorName) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.processors[name]
	return ok
}

// LookupPolicy returns the registered policy for the named processor.
func (r *PostProcessorRegistry) LookupPolicy(name ProcessorName) ProcessorPolicy {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policies[name]
}

// Freeze prevents further registration.
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
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.processors)
}

// ValidateRequested checks every name in the supplied list against the registry.
func (r *PostProcessorRegistry) ValidateRequested(names []string) error {
	if r == nil {
		return nil
	}
	if len(names) == 0 {
		return nil
	}

	// Convert from plan's []string to typed ProcessorName slice.
	typed := make([]ProcessorName, len(names))
	for i, n := range names {
		typed[i] = ProcessorName(n)
	}

	seen := make(map[ProcessorName]struct{}, len(typed))
	unique := make([]ProcessorName, 0, len(typed))
	for _, n := range typed {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		unique = append(unique, n)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var missing []string
	for _, name := range unique {
		if _, ok := r.processors[name]; ok {
			continue
		}
		policy := r.policies[name]
		if policy == "" {
			policy = DefaultPolicyFor(name)
		}
		if policy == ProcessorRequired {
			missing = append(missing, string(name))
		} else if r.log != nil {
			r.log.Warn("postprocessor best-effort not registered at preflight",
				zap.String("name", string(name)))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &scriptpkg.PlanInvalidError{
		ItemID:  names[0],
		Details: []string{"preflight: required postprocessor(s) not registered: " + strings.Join(missing, ", ")},
	}
}
