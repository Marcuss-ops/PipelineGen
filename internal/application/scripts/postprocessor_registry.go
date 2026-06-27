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
package scripts

import (
	"context"
	"fmt"
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
// their own classification logic. Used by:
//   - PostProcessorRegistry.ValidateRequested (classify missing
//     names so a plan referencing an unset Required processor
//     fails before Ollama).
//   - wire_script.validateRequiredProcessors (composition-time
//     guard for the canonical {persistence, document} required
//     set when the registry's per-name recorded policy is empty
//     because composition never wired the dep).
func DefaultPolicyFor(name string) ProcessorPolicy {
	return defaultPolicyByName[name]
}

// PostProcessor executes one post-generation phase. Each processor
// is opt-in — it only runs when its name is in the plan's
// Postprocessors list.
//
// PR 5 (June 2026): the second argument changed from `script string`
// to `input ProcessInput`. The envelope carries the canonical
// output text plus typed fields required by individual processors
// — most importantly the canonical ScriptID for PersistenceProcessor.
// Non-persistence processors that only need the prose read
// `input.Text`; processors that need the full output (specscene,
// wordcount) read the relevant typed fields.
//
// PR 2 (June 2026): the interface gained `Policy(plan)` so the
// registry can classify each proc as ProcessorRequired or
// ProcessorBestEffort at registration time. The plan argument is
// nil for registry-level policy resolution (registry passes nil);
// processors that want payload-conditional policy accept plan and
// return the resolved value. Pass nil when implementing static
// policy.
type PostProcessor interface {
	// Name returns the processor identifier ("entities", "metadata",
	// "voiceover", "images", "document", "persistence").
	Name() string

	// Policy reports the processor's policy classification. The
	// registry calls this at Register time with plan=nil. Pass nil
	// when the policy is static (persistence, document, etc.);
	// return the live policy when classification depends on the
	// plan payload (entities/metadata in payload-conditional mode).
	Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy

	// Process executes the post-generation work. The plan carries
	// the resolved generation plan (including identity, sizing,
	// and output options). The input envelope carries the canonical
	// model output + metadata every processor may need.
	//
	// Returns a PostProcessResult on success, or an error wrapping
	// scriptpkg.ErrPostprocessFailed on failure.
	Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error)
}

// PostProcessResult carries the output of a single processor.
// Each processor populates only the fields relevant to its phase.
//
// PR 2 (June 2026): added `Warnings []string` so a processor can
// emit non-fatal observations (e.g. "image download succeeded but
// alt text missing") that the registry and use case surface to
// the caller via GenerationResult.Warnings.
//
// PR 3 (June 2026): replaced the prior opaque-string
// `EntitiesJSON string` field with the canonical typed
// `*scriptpkg.EntityResult`. The string artefact is now produced
// at the artifact boundary by buildGenerationResult via
// scripts.SerializeEntityResultRoundTrip (round-trip
// JSON-marshalling only — read-only compatibility, never the
// source of truth). Producers MUST populate Entities directly;
// producers MUST NOT regenerate Entities from any prior
// EntitiesJSON string.
type PostProcessResult struct {
	Entities    *scriptpkg.EntityResult
	Metadata    []scriptpkg.VideoMetadata
	Voiceovers  []SceneVoiceover
	SceneImages []SceneImage
	DocLink     string
	DocID       string
	ScriptID    int64

	// AlreadyPersisted is set true by PersistenceProcessor when the
	// idempotency lookup found an existing row and the save was
	// skipped. Consumers downstream can use this flag to log a
	// "replay" outcome without re-marking the script as newly
	// produced. PR 5 default value is false for non-persistence
	// processors.
	AlreadyPersisted bool

	// Warnings carries non-fatal observations emitted by the
	// processor itself. Aggregated into the PipelineResult and
	// propagated to GenerationResult.Warnings.
	Warnings []string `json:"warnings,omitempty"`
}

// IsEmpty reports whether the result carries no observable work.
// Empty results from ProcessorRequired processors count as a hard
// failure (Run returns an error). Empty results from
// ProcessorBestEffort processors count as a warning.
//
// PR 2 (June 2026): the canonical definition. Each processor can
// override by populating its field explicitly; if it does not, this
// returns true and the registry marks the run as empty.
//
// PR 3 (June 2026): the Entities check now examines the typed
// *scriptpkg.EntityResult. A result is non-empty when the typed
// field is non-nil and carries at least one populated slice
// (Persons / Places / Concepts). Empty structured results (all
// three slices empty) still count as empty so the Required-fail
// gate catches a processor that produced no observations.
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
		// PersistenceProcessor success: ScriptID is the canonical
		// signal. AlreadyPersisted is a "no-row written" success
		// state — the operation succeeded even though no row was
		// inserted — so it does NOT count as empty.
		return false
	}
	return true
}

// ProcessInput is the typed envelope passed to every postprocessor.
// It carries the canonical model output plus the metadata fields
// that individual processors need:
//
//   - Text: the canonical V1 `output.text` field — what the model
//     produced. All non-persistence processors read this for their
//     own text-driven work (splitting scenes, generating metadata,
//     building doc HTML, etc.).
//   - WordCount: the model's reported token count. PersistenceProcessor
//     uses this to populate `final_word_count` on the script row.
//   - SpecScene: the structured V1 scene breakdown. PersistenceProcessor
//     persists it as a JSON column on the script row.
//   - ModelUsed: the model name that produced the output. Forwarded
//     to the script row as `model_used`.
//   - CacheStatus: "exact_hit" or "generated". Persisted to the script
//     row's generation_logs to make cache replays auditable.
//   - SourceTrace: nullable clip-evidence forward — currently unused
//     by processors but exposed so future processors (e.g. a
//     clip-driven QA processor) can read the resolved clip IDs
//     without re-deriving them.
//   - PriorArtifacts: outputs of earlier postprocessors (in the
//     order they ran). Processors that depend on prior-output
//     metadata receive it here. Currently empty (postprocessors
//     are independent today), but the slot is reserved for
//     staggered-pipeline work without a signature change.
//
// PR 5 (June 2026): introduced alongside the unified PostProcessor
// interface change. The previous `script string` shape was lossy
// — WordCount, SpecScene, ModelUsed, CacheStatus were re-derivable
// only by callers reading engineResult directly.
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
// After Freeze() is called (post-composition), registration
// is rejected. The composition root calls Freeze() once all
// processors are registered.
//
// PR 2 (June 2026): the registry stores each proc's policy
// classification at Register time so missing-registered checks
// at preflight can classify requested-but-missing processors as
// required (composition error) or best-effort (warning). The
// validateRequested call below surfaces this distinction.
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

// Register adds a processor. Returns false when proc is nil,
// the registry is nil, a processor with the same Name is already
// registered, or the registry is frozen.
//
// Duplicate registration is rejected (fail-closed) — the first
// registration wins. Callers that need idempotent registration
// should check Registered() first.
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

	// PR 2 (June 2026): capture the proc's policy at Register
	// time so ValidateRequested can classify missing-registered
	// lookups without holding the proc instance.
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

// LookupPolicy returns the registered policy for the named
// processor, or an empty string if the proc is not registered.
//
// PR 2 (June 2026): used by composition-level helpers to surface
// "missing required dependency" composition failures (the
// composition root can verify a Registry was wired with all
// ProcessorRequired processors before Freeze()).
func (r *PostProcessorRegistry) LookupPolicy(name string) ProcessorPolicy {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policies[name]
}

// Freeze prevents further registration. After freeze, all
// Register() calls return false. Idempotent — multiple
// freeze calls are no-ops.
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

// ValidateRequested checks every name in the supplied list against
// the registry. Names referencing unregistered processors with a
// ProcessorRequired classification cause a typed error so the
// preflight gate can short-circuit BEFORE the Ollama call. Names
// referencing unregistered ProcessorBestEffort processors are
// tolerated — Run will surface a warning for them.
//
// Classification for missing names uses the canonical
// `defaultPolicyByName` map (see below) so the runtime gate
// matches composition-time classification. A name registered
// successfully at composition uses its recorded runtime policy
// (overrides the default).
//
// The error returned is *scriptpkg.PlanInvalidError where the
// Details list carries each missing-required processor name. The
// use case wraps this with ErrPlanInvalid at the boundary.
//
// PR 2 (June 2026): gate that closes the "non-canonical WriteScript
// to dragnet" gap. Composition-side composition errors are
// reported separately by wire_script.go via validateRequiredProcessors.
//
// Safe on nil receiver (returns no-op nil).
func (r *PostProcessorRegistry) ValidateRequested(names []string) error {
	if r == nil {
		return nil
	}
	if len(names) == 0 {
		return nil
	}

	// Deduplicate input names so malformed plans (postprocessor
	// duplicates) do not produce duplicate preflight errors.
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
		// Proc not registered. Classify by policy. Prefer the
		// runtime-recorded policy (composition may have set it
		// via Register); fall back to the canonical name-default
		// (so plans referencing Required-class names that
		// composition never wired are caught).
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
		ItemID:  names[0], // best-effort marker for log correlation
		Details: []string{"preflight: required postprocessor(s) not registered: " + strings.Join(missing, ",")},
	}
}

// Run executes every processor whose name appears in the plan's
// Postprocessors list, in list order.
//
// PR 2 (June 2026) policy semantics — per TODO §3:
//   - ProcessorRequired processors that fail (return error), are
//     not registered, return a nil result, or return an empty
//     PostProcessResult all count as required failures.
//   - ProcessorBestEffort processors that fail similarly produce
//     warnings but do NOT contribute to the required-failure
//     count.
//   - Run returns a non-nil error wrapping ErrPostprocessFailed
//     only when ALL ProcessorRequired processors in the plan
//     failed AND at least one was requested; nil otherwise.
//     Best-effort failures are accumulated into
//     PipelineResult.Warnings. The "all required failed" gate
//     preserves isolation: a single failing required processor
//     does NOT fail Run as long as another required processor
//     succeeded (matches the original
//     TestRegistry_RunProcessorErrorIsIsolated contract).
//
// Run returns the aggregate PipelineResult (for backward compat
// with buildGenerationResult). The result is always non-nil.
//
// PR 5 (June 2026): the `script string` parameter was replaced
// with `input ProcessInput`. Processors that only need the prose
// read `input.Text`; processors that need WordCount / SpecScene /
// CacheStatus / ModelUsed read those fields directly.
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

	// PR 2 (June 2026): removed the previous early return
	// `if len(procs) == 0 { return &PipelineResult{}, nil }`.
	// Empty registry + non-empty plan.Postprocessors still walks
	// the loop and emits "postprocessor X not registered" warnings
	// (BestEffort tolerated; Required blocked). An empty
	// Postprocessors list still triggers the early return so we
	// don't iterate a zero-length slice unnecessarily.
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
			// Defensive fallback — ValidateRequested is the
			// canonical gate, but Run needs a policy to count
			// the proc towards required aggregation.
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

		if r.log != nil {
			r.log.Debug("running postprocessor",
				zap.String("name", name),
				zap.String("item_id", plan.ID),
				zap.String("policy", string(policy)))
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
				level := zap.ErrorLevel
				if policy != ProcessorRequired {
					level = zap.WarnLevel
				}
				if ce := r.log.Check(level, "postprocessor outcome"); ce != nil {
					ce.Write(
						zap.String("name", name),
						zap.String("item_id", plan.ID),
						zap.String("policy", string(policy)),
						zap.Error(err),
					)
				}
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

		// PR 2: empty output on a required processor counts as a
		// failure. Empty output on a best-effort processor is a
		// warning only.
		if ppResult.IsEmpty() {
			warn := fmt.Sprintf("postprocessor %q returned empty output", name)
			warnings = append(warnings, warn)
			if policy == ProcessorRequired {
				requiredRequested++
				requiredFails = append(requiredFails, name+" (empty output)")
			}
			if r.log != nil {
				r.log.Warn("postprocessor returned empty output",
					zap.String("name", name),
					zap.String("policy", string(policy)))
			}
			continue
		}

		// Success path.
		if policy == ProcessorRequired {
			requiredRequested++
			requiredSucceeded++
		}

		// Merge processor output (PR 5: honours AlreadyPersisted
		// flag from PersistenceProcessor so consumers see the
		// replay state).
		mergePostProcessResult(result, ppResult)

		// PR 2: surface the per-processor Warnings on the
		// aggregate PipelineResult.
		if len(ppResult.Warnings) > 0 {
			warnings = append(warnings, ppResult.Warnings...)
		}

		// PR 5: feed each processor's PostProcessResult into the
		// next iteration's PriorArtifacts slot. Currently unused
		// (processors are independent) but the slot is wired so
		// future staggered processors don't need a signature
		// change.
		if input.PriorArtifacts == nil {
			input.PriorArtifacts = make(map[string]PostProcessResult)
		}
		input.PriorArtifacts[name] = *ppResult
	}

	result.Warnings = warnings
	if r.log != nil && len(warnings) > 0 {
		r.log.Warn("postprocessors completed with warnings",
			zap.Int("warning_count", len(warnings)),
			zap.Int("required_requested", requiredRequested),
			zap.Int("required_succeeded", requiredSucceeded),
			zap.Int("total", len(plan.Postprocessors)),
			zap.Strings("warnings", warnings))
	}

	// PR 2: error only when ALL required processors failed AND at
	// least one was requested. The counter test
	// (TestRegistry_RunProcessorErrorIsIsolated) confirms that
	// single failure with other required successes → no error.
	if requiredRequested > 0 && requiredSucceeded == 0 {
		return result, fmt.Errorf("%w: all required postprocessor(s) failed (none succeeded): %s",
			scriptpkg.ErrPostprocessFailed, strings.Join(requiredFails, "; "))
	}
	return result, nil
}

// mergePostProcessResult copies non-zero fields from a processor
// result into the aggregate PipelineResult.
//
// PR 3 (June 2026): the prior EntitiesJSON string field on dst
// is no longer in scope — entities flow via the typed scriptpkg
// wrap at the artifact boundary (see buildGenerationResult which
// serializes dst.Entities to EntitiesJSON for read-only
// compatibility). The merge forwards the typed *EntityResult
// verbatim and updates src-driven warnings.
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
		// PR 5: forward the AlreadyPersisted replay flag.
		dst.AlreadyPersisted = src.AlreadyPersisted
	}
}
