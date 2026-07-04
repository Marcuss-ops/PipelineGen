// Package scripts — postprocessor_registry.go defines the
// PostProcessor interface and the PostProcessorRegistry that
// runs enabled processors in order. It replaces the monolithic
// Pipeline.Run with individually-testable processors.
//
// Each processor is opt-in: it runs only when its name appears
// in the plan's Postprocessors list. The registry respects the
// list order, which matches buildPostprocessorList ordering:
// entities → metadata → clip_bindings → stock_association →
// voiceover → images → document → persistence.
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
	"time"

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
	// StageDurations maps processor name → wall-clock milliseconds
	// consumed. Populated by Run() before merge. P1 #10 (June 2026).
	StageDurations map[string]int64 `json:"stage_durations,omitempty"`
	// SynthesizedScenes mirrors PostProcessResult.SynthesizedScenes
	// after mergePostProcessResult — the canonical pipeline-level
	// surface for processors that reconstructed scenes from prose.
	// FASE 3 (June 2026): added for the clip-bindings prose-fallback
	// heuristic. omitempty keeps the JSON envelope stable for
	// callers that did not opt into the heuristic.
	SynthesizedScenes []scriptpkg.SpecScene `json:"synthesized_scenes,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
	// FinalSpecScene (Issue #1, June 2026) is the canonical
	// post-walk SpecScene surface consumed by buildGenerationResult.
	// Pre-fix: buildGenerationResult read from engineResult.Output
	// .SpecScene (the pre-walk view). The clip_bindings prose-fallback
	// synthesised scenes into PipelineResult.SynthesizedScenes, but
	// the synthesised bundle never reached GenerationResult.Output
	// .SpecScene — the JSON envelope went out with empty scenes even
	// when the heuristic engaged. Post-fix: mergePostProcessResult
	// writes SynthesizedScenes back into the registry-local
	// ProcessInput.SpecScene.Scenes (so document/persistence see
	// populated scenes during the same Run) AND captures the
	// post-walk envelope here. buildGenerationResult prefers
	// postResult.FinalSpecScene with the empty-aware fallback so
	// the normal-model-output path is unaffected. omitempty keeps
	// the JSON envelope stable for calls that did not exercise any
	// postprocessor.
	FinalSpecScene scriptpkg.SpecSceneOutput `json:"final_specscene,omitempty"`
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
	ProcessorPersistence: ProcessorRequired,
	ProcessorDocument:    ProcessorBestEffort, // Fase 2: downgraded (→ document.generate job)
	ProcessorImages:      ProcessorBestEffort, // Fase 2: downgraded (→ images.generate job)
	ProcessorVoiceover:   ProcessorBestEffort, // Fase 2: downgraded (→ voiceover.generate job)
	ProcessorEntities:    ProcessorRequired,
	ProcessorMetadata:    ProcessorRequired,
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
	// Changed is set by mutative processors (e.g. ClipBindingsProcessor)
	// that modify input state but don't produce canonical output fields.
	// When true, IsEmpty() returns false even if all output fields
	// are zero. P1 #10 (June 2026).
	Changed bool `json:"changed,omitempty"`
	// DurationMs is the wall-clock time this processor consumed, set
	// by the registry's Run() method before merge. P1 #10 (June 2026).
	DurationMs int64 `json:"duration_ms,omitempty"`
	// SynthesizedScenes carries scene bundles constructed by an
	// individual processor when the canonical SpecScene pipeline
	// could not produce them. The clip-bindings prose-fallback
	// heuristic (FASE 3, June 2026) is the canonical emitter —
	// small local models (gemma2:2b / gemma4:e4b) commonly return
	// prose without SpecScene.scenes, so the binder synthesises N
	// scenes from input.Text and binds clips 1:1. Without this
	// field the binder would be flagged "returned empty output" by
	// the registry's IsEmpty check, even though meaningful work
	// happened. omitempty so existing emitters (entities /
	// metadata / voiceover / images / document / persistence) do
	// not see a serialisation diff.
	SynthesizedScenes []scriptpkg.SpecScene `json:"synthesized_scenes,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
}

// IsEmpty reports whether the result carries no observable work.
func (r *PostProcessResult) IsEmpty() bool {
	if r == nil {
		return true
	}
	// P1 #10 (June 2026): Changed flag lets mutative processors
	// (e.g. ClipBindingsProcessor) signal "I did real work" without
	// populating canonical output fields. Prevents false "empty
	// output" warnings.
	if r.Changed {
		return false
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
	// FASE 3 (June 2026): SynthesizedScenes counts as observable
	// work. Without this, the clip_bindings prose-fallback heuristic
	// is functionally complete but the registry still complains
	// "returned empty output" — choking the job on a false-positive.
	if len(r.SynthesizedScenes) > 0 {
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
	procs := make(map[ProcessorName]PostProcessor, len(r.processors))
	for k, v := range r.processors {
		procs[k] = v
	}
	policies := make(map[ProcessorName]ProcessorPolicy, len(r.policies))
	for k, v := range r.policies {
		policies[k] = v
	}
	r.mu.RUnlock()

	if len(plan.Postprocessors) == 0 {
		return &PipelineResult{FinalSpecScene: input.SpecScene}, nil
	}

	result := &PipelineResult{
		StageDurations: make(map[string]int64),
	}
	// Issue #1 (June 2026): seed FinalSpecScene with the
	// pre-walk envelope so buildGenerationResult's empty-aware
	// fallback sees a populated surface even when the loop
	// short-circuits before calling mergePostProcessResult
	// (empty-plan early return already covered above; processor
	// outcomes that IsEmpty()==true also skip merge here). The
	// mergePostProcessResult hook below overwrites this seed
	// with the post-walk envelope whenever a processor
	// successfully returns a non-empty result, so capturing
	// currentInput.SpecScene acts as the canonical "last writer
	// wins" snapshot at the post-walk time.
	result.FinalSpecScene = input.SpecScene
	var (
		warnings          []string
		requiredRequested int
		requiredSucceeded int
		requiredFails     []string
	)

	for _, rawName := range plan.Postprocessors {
		name := ProcessorName(rawName)
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

		start := time.Now()
		ppResult, err := proc.Process(ctx, plan, input)
		elapsed := time.Since(start).Milliseconds()

		if err != nil {
			result.StageDurations[name] = elapsed
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
			result.StageDurations[name] = elapsed
			warn := fmt.Sprintf("postprocessor %q returned nil result", name)
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, name+" (nil result)")
			}
			continue
		}

		ppResult.DurationMs = elapsed
		result.StageDurations[name] = elapsed

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

		mergePostProcessResult(result, ppResult, &input)

		if len(ppResult.Warnings) > 0 {
			warnings = append(warnings, ppResult.Warnings...)
		}
	}

	result.Warnings = warnings
	// Issue 3 / P0 (June 2026): the gate flipped.
	//
	// Pre-fix: a partial-success pattern (one Required processor
	// succeeds + another Required processor fails) was reported as
	// success because the gate was `requiredRequested > 0 &&
	// requiredSucceeded == 0`. This violated the ProcessorRequired
	// contract — any Required-class failure must abort the
	// pipeline, regardless of how many other Required processors
	// succeeded.
	//
	// The new gate is `len(requiredFails) > 0`: ANY Required-class
	// failure (err / nil-result / empty-output / missing-registry)
	// surfaces as a Go error wrapping
	// scriptpkg.ErrPostprocessFailed. The pre-fix "all required
	// failed" semantic is preserved as a strict subset (k-of-n
	// failures now fire the gate just as well as n-of-n failures).
	if len(requiredFails) > 0 {
		return result, fmt.Errorf("%w: required postprocessor failure: %s",
			scriptpkg.ErrPostprocessFailed, strings.Join(requiredFails, "; "))
	}
	return result, nil
}

// mergePostProcessResult copies non-zero fields from a processor
// mergePostProcessResult copies non-zero fields from a processor
// result into the aggregate PipelineResult, and writes back the
// synthesised Scene slice into the registry-local ProcessInput so
// subsequent postprocessors see the populated input.SpecScene.Scenes
// (document/persistence stop reading empty scenes downstream of
// the prose-fallback clip-bindings heuristic).
//
// Issue #1 (June 2026): the canonical pipeline-level SpecScene
// surface lives on PipelineResult.FinalSpecScene. mergePostProcessResult
// captures the post-walk SpecScene after every processor (in
// last-writer-wins order — there's only ever one synthesizer at a
// time so a copy is sufficient) so buildGenerationResult reads the
// post-walk envelope via the empty-aware fallback in
// generate_one_usecase.go.
//
// P1 #10 (June 2026) wall-clock timing — keep the per-processor
// StageDurations map hot so the outer use case can stream it into
// GenerationTimings.PostprocessMs (canonical Issue #3 plumbing).
//
// currentInput is the by-value copy of the ProcessInput that Run()
// passes to processors; nil-safe so callers that pre-Issue-1 wiring
// (eg. in older tests) keep working.
func mergePostProcessResult(dst *PipelineResult, src *PostProcessResult, currentInput *ProcessInput) {
	// P1 #10 (June 2026): record per-processor wall-clock timing.
	if dst.StageDurations == nil {
		dst.StageDurations = make(map[string]int64)
	}
	if src.Entities != nil {
		dst.Entities = src.Entities
	}
	if len(src.Metadata) > 0 {
		dst.VideoMetadata = append(dst.VideoMetadata, src.Metadata...)
	}
	if len(src.Voiceovers) > 0 {
		dst.Voiceovers = append(dst.Voiceovers, src.Voiceovers...)
		if currentInput != nil {
			for _, v := range src.Voiceovers {
				if v.SceneIndex < 0 || v.SceneIndex >= len(currentInput.SpecScene.Scenes) {
					continue
				}
				sc := &currentInput.SpecScene.Scenes[v.SceneIndex]
				if sc.Bindings.Voiceover == nil {
					sc.Bindings.Voiceover = &scriptpkg.VoiceoverBinding{}
				}
				sc.Bindings.Voiceover.Status = v.Status
				sc.Bindings.Voiceover.Link = v.Link
				sc.Bindings.Voiceover.LocalPath = v.LocalPath
			}
		}
	}
	if len(src.SceneImages) > 0 {
		dst.Scenes = append(dst.Scenes, src.SceneImages...)
		if currentInput != nil {
			for _, s := range src.SceneImages {
				if s.Index < 0 || s.Index >= len(currentInput.SpecScene.Scenes) {
					continue
				}
				sc := &currentInput.SpecScene.Scenes[s.Index]
				if sc.Bindings.Image == nil {
					sc.Bindings.Image = &scriptpkg.ImageBinding{}
				}
				sc.Bindings.Image.URL = s.URL
				sc.Bindings.Image.Status = "generated"
			}
		}
	}
	if src.DocLink != "" {
		dst.DocLink = src.DocLink
		dst.DocID = src.DocID
	}
	if src.ScriptID > 0 {
		dst.ScriptID = src.ScriptID
		dst.AlreadyPersisted = src.AlreadyPersisted
	}
	// FASE 3 (June 2026): prose-fallback clip_bindings emits
	// SynthesizedScenes. Last-wins semantics: only one processor
	// synthesises scenes at a time, so a simple overwrite keeps the
	// invariant simple.
	if len(src.SynthesizedScenes) > 0 {
		dst.SynthesizedScenes = src.SynthesizedScenes
		// Issue #1 (June 2026) WRITE-BACK. The registry passes
		// the same `input` ProcessInput to every processor in
		// the loop, so updating its SpecScene.Scenes here means
		// every subsequent processor (document, persistence,
		// voiceover, images) sees the synthesised bundle instead
		// of the original empty specscene. Without this the
		// prose-fallback heuristic could declare success
		// (PipelineResult.SynthesizedScenes populated +
		// IsEmpty == false) while downstream processors still
		// received an envelope with empty SpecScene.Scenes —
		// document got an empty storyboard, persistence stored
		// an empty SpecScene row.
		if currentInput != nil {
			currentInput.SpecScene.Scenes = src.SynthesizedScenes
		}
	}
	// Issue #1 (June 2026) FINAL SURFACE. Capture the post-walk
	// SpecScene envelope so buildGenerationResult can read it
	// instead of the pre-walk engineResult.Output.SpecScene.
	// Set unconditionally (NOT inside the SynthesizedScenes
	// branch) because the post-walk envelope is meaningful even
	// when no synthesizer ran: in that case currentInput.SpecScene
	// already mirrors engineResult.Output.SpecScene and the
	// downstream consumer's empty-aware fallback decides whether
	// to use it.
	if currentInput != nil {
		dst.FinalSpecScene = currentInput.SpecScene
	}
}
