// Package overlays — visual_plan.go owns the CompiledVisualPlan: the final,
// renderer-ready JSON surface produced by the semantic-index pipeline.
//
// Pipeline position (the terminal stage before the renderer):
//
//	VisualIntentResolver → []VisualIntent
//	  → VisualBudget.Apply      (per-kind caps)
//	  → VisualScheduler.Schedule (temporal constraints)
//	  → EntityMediaResolver + VisualUsageTracker (asset resolution)
//	  → DeterministicPresetSampler (preset + animation)
//	  → VisualPlanCompiler.Compile → CompiledVisualPlan { "visual_events": [...] }
//
// The compiled plan is deliberately a SEALED, declarative list of visual
// events. The renderer (PureFrame/FFmpeg/Chronon) receives asset + preset +
// animation + timing and must only execute — it never re-derives why a choice
// was made. The plan's shape mirrors the pipeline's canonical "visual_events"
// contract, with integer-microsecond timing (never floats).
package overlays

import (
	"errors"
	"fmt"
	"strings"
)

// CompiledVisualPlan is the final per-scene visual plan: an ordered list of
// visual events the renderer materializes. It is the "Compiled Visual Plan"
// stage — everything upstream (semantic index, intent resolution, budgeting,
// scheduling, media resolution, sampling) is already folded in; nothing here
// needs further editorial decisions.
type CompiledVisualPlan struct {
	// SceneID is the id of the scene this plan belongs to.
	SceneID string `json:"scene_id"`
	// VisualEvents is the ordered list of renderer events. Order is the
	// canonical play order (by start_us, then event_id).
	VisualEvents []VisualEvent `json:"visual_events"`
}

// VisualEvent is one sealed visual event: COSA mostrare (type + asset), COME
// (preset + animation triple) and QUANDO (start_us + duration_us). All timing
// is integer microseconds.
type VisualEvent struct {
	// EventID is the stable event id (derived from scene + semantic id).
	EventID string `json:"event_id"`
	// SemanticID is the id of the source SemanticItem.
	SemanticID string `json:"semantic_id"`
	// Type is the coarse visual category (VisualIntent.Kind spelling, e.g.
	// "ENTITY_IMAGE", "IMPORTANT_NUMBER").
	Type string `json:"type"`
	// PresetFamily names the sampling family this event resolved through
	// (e.g. "person_image", "money"). Traceability metadata; the renderer
	// only consumes Preset.
	PresetFamily string `json:"preset_family,omitempty"`
	// AssetID is the resolved asset (empty for text-only events).
	AssetID string `json:"asset_id,omitempty"`
	// Preset is the sampled Chronon visual preset id.
	Preset string `json:"preset,omitempty"`
	// AnimationIn is the entry animation id.
	AnimationIn string `json:"animation_in,omitempty"`
	// AnimationIdle is the idle (loop) animation id.
	AnimationIdle string `json:"animation_idle,omitempty"`
	// AnimationOut is the exit animation id.
	AnimationOut string `json:"animation_out,omitempty"`
	// StartUS is the event start in integer microseconds.
	StartUS int64 `json:"start_us"`
	// DurationUS is the on-screen duration in integer microseconds.
	DurationUS int64 `json:"duration_us"`
}

// ErrInvalidVisualPlan is returned when a CompiledVisualPlan or VisualEvent
// violates its sealed-plan invariants.
var ErrInvalidVisualPlan = errors.New("overlays: invalid visual plan")

// Validate enforces the sealed-plan invariants of one event: a stable id, a
// semantic link, a non-empty visual type and a monotonic timing span. A
// renderer must never receive an event whose WHAT is not grounded in a valid
// WHEN.
func (e VisualEvent) Validate() error {
	if strings.TrimSpace(e.EventID) == "" {
		return fmt.Errorf("%w: event_id is required", ErrInvalidVisualPlan)
	}
	if strings.TrimSpace(e.SemanticID) == "" {
		return fmt.Errorf("%w: semantic_id is required", ErrInvalidVisualPlan)
	}
	if strings.TrimSpace(e.Type) == "" {
		return fmt.Errorf("%w: type is required", ErrInvalidVisualPlan)
	}
	if e.StartUS < 0 || e.DurationUS <= 0 {
		return fmt.Errorf("%w: invalid timing", ErrInvalidVisualPlan)
	}
	return nil
}

// Validate enforces the sealed-plan invariants: a non-empty scene id and a
// list of valid events. An empty event list is allowed (a scene may
// legitimately produce zero overlays after budgeting/scheduling).
func (p CompiledVisualPlan) Validate() error {
	if strings.TrimSpace(p.SceneID) == "" {
		return fmt.Errorf("%w: scene_id is required", ErrInvalidVisualPlan)
	}
	for i, ev := range p.VisualEvents {
		if err := ev.Validate(); err != nil {
			return fmt.Errorf("%w: event[%d]: %v", ErrInvalidVisualPlan, i, err)
		}
	}
	return nil
}

// VisualEventInput is one resolved + sampled intent ready to be sealed into a
// VisualEvent. It carries the full VisualIntent (kind, family, timing, asset)
// plus the sampled preset and the animation triple. Idle/out animations are
// optional: the deterministic sampler currently selects a single entry
// animation, so idle/out stay empty until a triple-sampler supplies them.
type VisualEventInput struct {
	Intent VisualIntent
	// Preset is the sampled Chronon preset id.
	Preset string
	// AnimationIn is the sampled entry animation id.
	AnimationIn string
	// AnimationIdle is the optional idle animation id.
	AnimationIdle string
	// AnimationOut is the optional exit animation id.
	AnimationOut string
}

// EventInputFromSample is the convenience binding from a DeterministicPresetSampler
// result to a compiler input: the sampler's single Animation becomes the entry
// animation. This keeps the two concerns — sampling and compilation — wired
// without the compiler importing the sampler's result shape.
func EventInputFromSample(intent VisualIntent, sample PresetSample) VisualEventInput {
	return VisualEventInput{
		Intent:       intent,
		Preset:       sample.Preset,
		AnimationIn:  sample.Animation,
		AnimationOut: "",
	}
}

// VisualPlanCompiler seals resolved, sampled intents into a CompiledVisualPlan.
// It is stateless and safe for concurrent use.
type VisualPlanCompiler struct{}

// Compile returns the sealed per-scene plan for the given inputs. Inputs whose
// intent carries no semantic id or no visual kind are skipped (an event is
// never invented for an unresolved intent — fail-closed). Events are emitted
// in input order; the caller feeds a deterministically ordered slice.
func (VisualPlanCompiler) Compile(sceneID string, inputs []VisualEventInput) CompiledVisualPlan {
	events := make([]VisualEvent, 0, len(inputs))
	for _, in := range inputs {
		if ev, ok := sealEvent(sceneID, in); ok {
			events = append(events, ev)
		}
	}
	return CompiledVisualPlan{SceneID: sceneID, VisualEvents: events}
}

// sealEvent folds one resolved input into a sealed VisualEvent. ok=false when
// the intent is not linkable (no semantic id) or has no visual category.
func sealEvent(sceneID string, in VisualEventInput) (VisualEvent, bool) {
	if strings.TrimSpace(in.Intent.SemanticID) == "" {
		return VisualEvent{}, false
	}
	if strings.TrimSpace(string(in.Intent.Kind)) == "" {
		return VisualEvent{}, false
	}
	return VisualEvent{
		EventID:       visualEventID(sceneID, in.Intent.SemanticID),
		SemanticID:    in.Intent.SemanticID,
		Type:          string(in.Intent.Kind),
		PresetFamily:  string(in.Intent.PresetFamily),
		AssetID:       in.Intent.AssetID,
		Preset:        in.Preset,
		AnimationIn:   in.AnimationIn,
		AnimationIdle: in.AnimationIdle,
		AnimationOut:  in.AnimationOut,
		StartUS:       in.Intent.StartUS,
		DurationUS:    in.Intent.DurationUS,
	}, true
}

// visualEventID derives the stable renderer-facing event id from the
// (scene, semantic) pair. It reuses intentID's slugification (the single
// owner of id-slug logic in this package) so event ids and intent ids share
// one canonical derivation, then swaps the prefix.
func visualEventID(sceneID, semanticID string) string {
	return "visual-" + strings.TrimPrefix(intentID(sceneID, semanticID), "intent-")
}

// DefaultVisualPlanCompiler is the process-wide compiler. Every call site
// compiles through this single instance so the sealed event shape is uniform.
var DefaultVisualPlanCompiler = VisualPlanCompiler{}
