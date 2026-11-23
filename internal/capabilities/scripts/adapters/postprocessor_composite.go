package adapters

import (
	"context"
	"strings"
	"sync"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

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
	processors    map[ProcessorName]PostProcessor
	policies      map[ProcessorName]ProcessorPolicy
	frozen        bool
	mu            sync.RWMutex
	log           *zap.Logger
	timing        VidRushTimingMetrics
	timingAdapter *CanonicalTimingAdapter
}

// SetVidRushTimingMetrics attaches the optional latency recorder used by the
// canonical postprocessor walk. Counter-only metrics implementations remain
// valid and simply do not receive duration observations.
func (r *PostProcessorRegistry) SetVidRushTimingMetrics(metrics VidRushTimingMetrics) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timing = metrics
	if r.timingAdapter == nil {
		r.timingAdapter = &CanonicalTimingAdapter{}
	}
	r.timingAdapter.VidRush = metrics
}

// SetCanonicalTimingAdapter installs the compatibility projection used by
// canonical Run-backed registry executions. The adapter is optional so old
// composition fixtures remain source-compatible.
func (r *PostProcessorRegistry) SetCanonicalTimingAdapter(adapter *CanonicalTimingAdapter) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timingAdapter = adapter
	if adapter != nil {
		r.timing = adapter.VidRush
	}
}

func (r *PostProcessorRegistry) vidRushTimingMetrics() VidRushTimingMetrics {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.timing
}

func (r *PostProcessorRegistry) canonicalTimingAdapter() *CanonicalTimingAdapter {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.timingAdapter
}

// TimingMetrics returns the optional VidRush latency recorder so composition
// can pass the same recorder to processors registered after the AI-backed set.
func (r *PostProcessorRegistry) TimingMetrics() VidRushTimingMetrics {
	return r.vidRushTimingMetrics()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.processors)
}

// ValidateRequested checks every name in the supplied list against the registry.
func (r *PostProcessorRegistry) ValidateRequested(names []ProcessorName) error {
	if r == nil || len(names) == 0 {
		return nil
	}

	seen := make(map[ProcessorName]struct{}, len(names))
	unique := make([]ProcessorName, 0, len(names))
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
		ItemID:  string(names[0]),
		Details: []string{"preflight: required postprocessor(s) not registered: " + strings.Join(missing, ", ")},
	}
}

// stageForProcessor is the canonical mapping from a postprocessor to the
// workflow stage it reports to the parent job. It is the producer side of
// job.CanonicalStageOrder(): every stage listed there must be produced here,
// and a stage with no producer must not be listed there (godlike/07
// no-fake-availability).
//
// Exactly ONE processor is mapped per non-language stage. Two processors
// mapped onto the same stage would upsert the same (language, unit, job_id)
// observation, so the later one would silently overwrite the earlier one's
// failure with its own success — the mapping would then report the last
// processor to run, not whether the stage actually completed.
func stageForProcessor(name ProcessorName) job.StageName {
	switch name {
	case ProcessorClipBindings:
		return job.StageClips
	case ProcessorStockBindings:
		return job.StageStock
	case ProcessorTranslation:
		return job.StageTranslation
	case ProcessorVoiceover:
		return job.StageVoiceover
	case ProcessorVisualSlots:
		return job.StageOverlay
	case ProcessorPersistence:
		return job.StagePersistence
	default:
		return ""
	}
}

func recordProcessorProgress(result *PipelineResult, name ProcessorName, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, status job.StageStatus, jobID, errMsg string) {
	if result == nil {
		return
	}
	stage := stageForProcessor(name)
	if stage == "" {
		return
	}
	language := strings.TrimSpace(input.EffectiveLanguage)
	if language == "" && plan != nil {
		language = strings.TrimSpace(plan.Language)
	}
	if result.StageProgress == nil {
		result.StageProgress = make(map[string]job.StageProgress)
	}
	progress := result.StageProgress[string(stage)]
	progress.Stage = stage
	observation := job.StageLanguageStatus{
		Stage: stage, Language: language, Status: status, JobID: jobID, Error: errMsg,
	}
	found := false
	for i := range progress.Languages {
		if progress.Languages[i].Language == language &&
			progress.Languages[i].Unit == "" &&
			(jobID == "" || progress.Languages[i].JobID == jobID) {
			progress.Languages[i] = observation
			found = true
			break
		}
	}
	if !found {
		progress.Languages = append(progress.Languages, observation)
	}
	progress.Total = len(progress.Languages)
	progress.Completed = 0
	for _, item := range progress.Languages {
		if item.Status == job.StageCompleted {
			progress.Completed++
		}
	}
	result.StageProgress[string(stage)] = progress
}

// PostProcessResult carries the output of a single processor.
type PostProcessResult struct {
	VisualPlans       []mediamemory.SceneVisualPlan
	VisualAssignments []mediadomain.VisualAssignment `json:"visual_assignments,omitempty"`
	Entities          *scriptpkg.EntityResult
	VidRushSegments   []scriptpkg.VidRushSegmentResult
	Metadata          []scriptpkg.VideoMetadata
	Voiceovers        []SceneVoiceover
	SceneImages       []SceneImage
	ScriptID          int64
	AlreadyPersisted  bool
	// Changed is set by mutative processors (e.g. ClipBindingsProcessor)
	// that modify input state but don't produce canonical output fields.
	// When true, IsEmpty() returns false even if all output fields
	// are zero. P1 #10 (June 2026).
	Changed bool `json:"changed,omitempty"`
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
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09): the canonical
	// pipeline-level surface for translated text + translated
	// SpecScene. The TranslationProcessor populates both in
	// addition to the in-place mutation on ProcessInput (per the
	// MIX design; the in-place mutation handles downstream
	// document/persistence within the same Run, the explicit
	// fields handle cross-Run observability + buildGenerationResult
	// preference). omitempty so callers that did not request
	// translation don't see a serialisation diff.
	TranslatedText      string                       `json:"translated_text,omitempty"`
	TranslatedSpecScene scriptpkg.SpecSceneOutput    `json:"translated_specscene,omitempty"`
	OriginalText        string                       `json:"original_text,omitempty"`
	OriginalSpecScene   scriptpkg.SpecSceneOutput    `json:"original_specscene,omitempty"`
	EffectiveLanguage   string                       `json:"effective_language,omitempty"`
	StageProgress       map[string]job.StageProgress `json:"stage_progress,omitempty"`
	UpdatedSpecScene    scriptpkg.SpecSceneOutput    `json:"updated_specscene,omitempty"`
	// SpecSceneChanged is internal pipeline metadata used to force a
	// document refresh after durable location reconciliation.
	SpecSceneChanged bool `json:"-"`
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
	if len(r.VisualAssignments) > 0 {
		return false
	}
	if len(r.VidRushSegments) > 0 {
		return false
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
	if len(r.UpdatedSpecScene.Scenes) > 0 {
		return false
	}
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09): translated text +
	// translated SpecScene count as observable work. The MIX design
	// keeps the in-place mutation on ProcessInput.SpecScene (so
	// downstream document/persistence see the translated bundle
	// within the same Run) AND surfaces the explicit fields here
	// for cross-Run observability. Without this branch, the registry
	// would flag the TranslationProcessor "returned empty output"
	// when only in-place mutation occurred (a false-positive bug
	// surfaced in the pre-PR-5 audit).
	if strings.TrimSpace(r.TranslatedText) != "" {
		return false
	}
	if len(r.TranslatedSpecScene.Scenes) > 0 {
		return false
	}
	return true
}

// PipelineResult aggregates the postprocessor outputs across the
// full Run sequence. PR 5: it's the typed merged view that
// generation_job.go writes to script/section rows via the
// canonical artifacts contract.
type PipelineResult struct {
	VisualPlans       []mediamemory.SceneVisualPlan
	VisualAssignments []mediadomain.VisualAssignment `json:"visual_assignments,omitempty"`
	Entities          *scriptpkg.EntityResult
	VidRushSegments   []scriptpkg.VidRushSegmentResult
	VideoMetadata     []scriptpkg.VideoMetadata
	Voiceovers        []SceneVoiceover
	// TimingArtifacts retains the in-process word-level timing SSOT for the
	// legacy/batch path until its Chronon overlay has been enqueued. It is
	// never serialized into the job or Google Doc.
	TimingArtifacts  map[string]*capabilityaudio.SpeechTimingArtifact `json:"-"`
	Scenes           []SceneImage
	ScriptID         int64
	AlreadyPersisted bool
	StageProgress    map[string]job.StageProgress `json:"stage_progress,omitempty"`
	// SynthesizedScenes mirrors PostProcessResult.SynthesizedScenes
	// after MergePostProcessResult — the canonical pipeline-level
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
	// when the heuristic engaged. Post-fix: MergePostProcessResult
	// writes SynthesizedScenes back into the registry-local
	// ProcessInput.SpecScene.Scenes (so document/persistence see
	// populated scenes during the same Run) AND captures the
	// post-walk envelope here. buildGenerationResult prefers
	// postResult.FinalSpecScene with the empty-aware fallback so
	// the normal-model-output path is unaffected. omitempty keeps
	// the JSON envelope stable for calls that did not exercise any
	// postprocessor.
	FinalSpecScene scriptpkg.SpecSceneOutput `json:"final_specscene,omitempty"`
	// PR-TRANSLATE-SCRIPT-SPEC PR-6 (2026-07-09): the canonical
	// pipeline-level surface for the translated text + translated
	// SpecScene (Last-writer-wins from MergePostProcessResult).
	// omitempty so callers that did not opt into translation don't
	// see a serialisation diff. The buildGenerationResult consumer
	// in usecase/generate_one_usecase.go prefers these fields
	// (when populated) so the post-translation version is the
	// canonical wire-shape observed downstream.
	TranslatedText      string                    `json:"translated_text,omitempty"`
	TranslatedSpecScene scriptpkg.SpecSceneOutput `json:"translated_specscene,omitempty"`
	// Original* preserve the source-language surface when translation runs
	// before persistence, allowing the persistence processor to write both
	// canonical language rows.
	OriginalText      string                    `json:"original_text,omitempty"`
	OriginalSpecScene scriptpkg.SpecSceneOutput `json:"original_specscene,omitempty"`
	EffectiveLanguage string                    `json:"effective_language,omitempty"`
	// SpecSceneChanged is internal pipeline metadata used to force a
	// document refresh after durable location reconciliation.
	SpecSceneChanged bool `json:"-"`
}
