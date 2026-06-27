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
// PR 7 (June 2026): added Freeze/IsFrozen so composition-time
// registration is rejected after wiring is complete. The
// composition root calls Freeze() once all processors are
// registered; any subsequent Register() call returns false.
package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Per-stage result types (PR 3 — companions of PostProcessResult) ─

// SceneVoiceover is a single scene-voiceover outcome from
// VoiceoverProcessor. PR 9: voices map to model-defined scenes
// 1:1 with stable indexes (matches engineResult.Output.SpecScene.Scenes).
type SceneVoiceover struct {
	SceneIndex int
	Status     string // "completed" | "failed" | "empty_result"
	Link       string // DriveLink for the produced audio
	LocalPath  string // local on-disk path (debugging)
}

// SceneImage is a single scene-image outcome from ImageProcessor.
// PR 9: images map to model-defined scenes 1:1.
type SceneImage struct {
	Index int
	Text  string // scene text used as the generation prompt
	URL   string // public URL of the generated image
}

// PipelineResult aggregates the postprocessor outputs across the
// full Run sequence. PR 5: it's the typed merged view that
// generation_job.go writes to script/section rows via the
// canonical artifacts contract.
type PipelineResult struct {
	Entities         *scriptpkg.EntityResult
	VideoMetadata    []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	Scenes           []SceneImage
	DocLink          string
	DocID            string
	ScriptID         int64
	AlreadyPersisted bool
	Warnings         []string
}

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
var defaultPolicyByName = map[string]ProcessorPolicy{
	"persistence": ProcessorRequired,
	"document":    ProcessorRequired,
	"images":      ProcessorBestEffort,
	"voiceover":   ProcessorBestEffort,
	"entities":    ProcessorRequired,
	"metadata":    ProcessorRequired,
}

// DefaultPolicyFor returns the canonical default policy for a
// named postprocessor. Returns empty string for unknown names —
// callers MUST treat unknown names as a hard fail or warn per
// their own classification logic.
func DefaultPolicyFor(name string) ProcessorPolicy {
	return defaultPolicyByName[name]
}

// PostProcessor executes one post-generation phase.
//
// PR 5 (June 2026): the second argument changed from `script string`
// to `input ProcessInput`. The envelope carries the canonical
// output text plus typed fields required by individual processors.
type PostProcessor interface {
	Name() string
	Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy
	Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error)
}

// PostProcessResult carries the output of a single processor.
type PostProcessResult struct {
	Entities         *scriptpkg.EntityResult
	Metadata         []scriptpkg.VideoMetadata
	Voiceovers       []SceneVoiceover
	SceneImages      []SceneImage
	DocLink          string
	DocID            string
	ScriptID         int64
	AlreadyPersisted bool
	Warnings         []string `json:"warnings,omitempty"`
}

// IsEmpty reports whether the result carries no observable work.
func (r *PostProcessResult) IsEmpty() bool {
	if r == nil {
		return true
	}
	if r.Entities != nil {
		if len(r.Entities.Persons) > 0 || len(r.Entities.Places) > 0 || len(r.Entities.Concepts) > 0 {
			return false
		}
	}
	if len(r.Metadata) > 0 {
		return false
	}
	if len(r.Voiceovers) > 0 {
		return false
	}
	if len(r.SceneImages) > 0 {
		return false
	}
	if r.DocLink != "" || r.DocID != "" {
		return false
	}
	if r.ScriptID > 0 || r.AlreadyPersisted {
		return false
	}
	return true
}

// ProcessInput is the typed envelope passed to every postprocessor.
type ProcessInput struct {
	Text           string
	WordCount      int
	SpecScene      scriptpkg.SpecSceneOutput
	ModelUsed      string
	CacheStatus    string
	SourceTrace    *scriptpkg.ClipEvidence
	PriorArtifacts map[string]PostProcessResult
}

// PostProcessorRegistry runs enabled processors in order.
type PostProcessorRegistry struct {
	processors map[string]PostProcessor
	policies   map[string]ProcessorPolicy
	frozen     bool
	mu         sync.RWMutex
	log        *zap.Logger
}

// NewPostProcessorRegistry creates an empty, unfrozen registry.
func NewPostProcessorRegistry(log *zap.Logger) *PostProcessorRegistry {
	return &PostProcessorRegistry{
		processors: make(map[string]PostProcessor),
		policies:   make(map[string]ProcessorPolicy),
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

	policy := proc.Policy(nil)
	r.processors[name] = proc
	r.policies[name] = policy
	if r.log != nil {
		r.log.Debug("postprocessor registered",
			zap.String("name", name),
			zap.String("policy", string(policy)))
	}
	return true
}

// Registered returns true when a processor with the given name is registered.
func (r *PostProcessorRegistry) Registered(name string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.processors[name]
	return ok
}

// LookupPolicy returns the registered policy for the named processor.
func (r *PostProcessorRegistry) LookupPolicy(name string) ProcessorPolicy {
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

	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, n := range names {
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
			missing = append(missing, name)
		} else if r.log != nil {
			r.log.Warn("postprocessor best-effort not registered at preflight",
				zap.String("name", name))
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

// Run executes every processor whose name appears in the plan's
// Postprocessors list, in list order.
func (r *PostProcessorRegistry) Run(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	input ProcessInput,
) (*PipelineResult, error) {
	if r == nil {
		return &PipelineResult{}, nil
	}
	r.mu.RLock()
	procs := make(map[string]PostProcessor, len(r.processors))
	for k, v := range r.processors {
		procs[k] = v
	}
	policies := make(map[string]ProcessorPolicy, len(r.policies))
	for k, v := range r.policies {
		policies[k] = v
	}
	r.mu.RUnlock()

	if len(plan.Postprocessors) == 0 {
		return &PipelineResult{}, nil
	}

	result := &PipelineResult{}
	var (
		warnings          []string
		requiredRequested int
		requiredSucceeded int
		requiredFails     []string
	)

	for _, name := range plan.Postprocessors {
		proc, ok := procs[name]
		policy := policies[name]
		if policy == "" {
			policy = DefaultPolicyFor(name)
		}

		if !ok || proc == nil {
			warn := fmt.Sprintf("postprocessor %q not registered", name)
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, name+" (not registered)")
			} else if r.log != nil {
				r.log.Warn("postprocessor not registered, skipping (best-effort)",
					zap.String("name", name),
					zap.String("item_id", plan.ID))
			}
			continue
		}

		ppResult, err := proc.Process(ctx, plan, input)

		if err != nil {
			warn := fmt.Sprintf("postprocessor %q failed: %v", name, err)
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, name+" (failed: "+err.Error()+")")
			}
			if r.log != nil {
				r.log.Warn("postprocessor outcome",
					zap.String("name", name),
					zap.Error(err))
			}
			continue
		}

		if ppResult == nil {
			warn := fmt.Sprintf("postprocessor %q returned nil result", name)
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, name+" (nil result)")
			}
			continue
		}

		if ppResult.IsEmpty() {
			warn := fmt.Sprintf("postprocessor %q returned empty output", name)
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, name+" (empty output)")
			}
			continue
		}

		if policy == ProcessorRequired {
			requiredRequested++
			requiredSucceeded++
		}

		mergePostProcessResult(result, ppResult)

		if len(ppResult.Warnings) > 0 {
			warnings = append(warnings, ppResult.Warnings...)
		}
	}

	result.Warnings = warnings
	if requiredRequested > 0 && requiredSucceeded == 0 {
		return result, fmt.Errorf("%w: all required postprocessor(s) failed (none succeeded): %s",
			scriptpkg.ErrPostprocessFailed, strings.Join(requiredFails, "; "))
	}
	return result, nil
}

// mergePostProcessResult copies non-zero fields from a processor
// result into the aggregate PipelineResult.
func mergePostProcessResult(dst *PipelineResult, src *PostProcessResult) {
	if src.Entities != nil {
		dst.Entities = src.Entities
	}
	if len(src.Metadata) > 0 {
		dst.VideoMetadata = append(dst.VideoMetadata, src.Metadata...)
	}
	if len(src.Voiceovers) > 0 {
		dst.Voiceovers = append(dst.Voiceovers, src.Voiceovers...)
	}
	if len(src.SceneImages) > 0 {
		dst.Scenes = append(dst.Scenes, src.SceneImages...)
	}
	if src.DocLink != "" {
		dst.DocLink = src.DocLink
		dst.DocID = src.DocID
	}
	if src.ScriptID > 0 {
		dst.ScriptID = src.ScriptID
		dst.AlreadyPersisted = src.AlreadyPersisted
	}
}
