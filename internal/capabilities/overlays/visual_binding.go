// Package overlays — visual_binding.go owns the VisualBinding contract: the
// durable row of a compiled visual decision (the `script_visual_bindings`
// table of the semantic-index pipeline).
//
// A CompiledVisualPlan is the renderer-facing seal; a VisualBinding is the
// traceability row that answers "why did that event exist?": it records the
// preset family, the sampled preset, the resolved asset, the animation triple,
// the timing and the resolver/sampler versions that produced it. One binding
// exists per VisualEvent and is joined back to script_semantic_items on
// semantic_id.
package overlays

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Version constants for the resolver and sampler stages that produce a
// binding. They are persisted verbatim so a binding is fully attributable to
// the exact stage versions that computed it.
const (
	// VisualIntentResolverVersion is the version of the VisualIntentResolver
	// (semantic type → kind/family/priority table).
	VisualIntentResolverVersion = "visual-intent-resolver.v1"
	// DeterministicPresetSamplerVersion is the version of the
	// DeterministicPresetSampler (seeded preset/animation selection).
	DeterministicPresetSamplerVersion = "deterministic-preset-sampler.v1"
)

// VisualBinding is one durable row of the compiled visual plan. It mirrors a
// VisualEvent and adds the attribution the event deliberately omits: the
// script link, the preset family and the stage versions.
type VisualBinding struct {
	// ScriptID is the owning script (scripts.id). Zero until bound to a
	// script at persistence time.
	ScriptID int64 `json:"script_id"`
	// SemanticID is the id of the source SemanticItem.
	SemanticID string `json:"semantic_id"`
	// VisualEventID is the stable event id of the corresponding VisualEvent.
	VisualEventID string `json:"visual_event_id"`
	// PresetFamily names the sampling family (person_image, money, ...).
	PresetFamily string `json:"preset_family"`
	// PresetID is the sampled Chronon preset id.
	PresetID string `json:"preset_id"`
	// AssetID is the resolved asset (empty for text-only events).
	AssetID string `json:"asset_id,omitempty"`
	// AnimationIn/Idle/Out is the animation triple.
	AnimationIn   string `json:"animation_in,omitempty"`
	AnimationIdle string `json:"animation_idle,omitempty"`
	AnimationOut  string `json:"animation_out,omitempty"`
	// StartUS is the event start in integer microseconds.
	StartUS int64 `json:"start_us"`
	// DurationUS is the on-screen duration in integer microseconds.
	DurationUS int64 `json:"duration_us"`
	// ResolverVersion is the VisualIntentResolver version that resolved the
	// binding's family/priority.
	ResolverVersion string `json:"resolver_version"`
	// SamplerVersion is the DeterministicPresetSampler version that selected
	// the preset/animation.
	SamplerVersion string `json:"sampler_version"`
}

// ErrInvalidVisualBinding is returned when a VisualBinding violates its
// traceability invariants.
var ErrInvalidVisualBinding = errors.New("overlays: invalid visual binding")

// Validate enforces the binding invariants: a non-negative script link, a
// stable event id and semantic link, a preset family, and a monotonic timing
// span. A binding that cannot be joined back to its semantic item is never
// persisted.
func (b VisualBinding) Validate() error {
	if b.ScriptID < 0 {
		return fmt.Errorf("%w: script_id must be non-negative", ErrInvalidVisualBinding)
	}
	if strings.TrimSpace(b.VisualEventID) == "" {
		return fmt.Errorf("%w: visual_event_id is required", ErrInvalidVisualBinding)
	}
	if strings.TrimSpace(b.SemanticID) == "" {
		return fmt.Errorf("%w: semantic_id is required", ErrInvalidVisualBinding)
	}
	if strings.TrimSpace(b.PresetFamily) == "" {
		return fmt.Errorf("%w: preset_family is required", ErrInvalidVisualBinding)
	}
	if b.StartUS < 0 || b.DurationUS <= 0 {
		return fmt.Errorf("%w: invalid timing", ErrInvalidVisualBinding)
	}
	return nil
}

// BuildVisualBindings projects a compiled plan into its durable binding rows,
// attributed to the given resolver/sampler versions. It is the single owner of
// the event → binding shape, so the persistence layer never re-derives fields.
func BuildVisualBindings(scriptID int64, plan CompiledVisualPlan, resolverVersion, samplerVersion string) []VisualBinding {
	out := make([]VisualBinding, 0, len(plan.VisualEvents))
	for _, ev := range plan.VisualEvents {
		out = append(out, VisualBinding{
			ScriptID:        scriptID,
			SemanticID:      ev.SemanticID,
			VisualEventID:   ev.EventID,
			PresetFamily:    ev.PresetFamily,
			PresetID:        ev.Preset,
			AssetID:         ev.AssetID,
			AnimationIn:     ev.AnimationIn,
			AnimationIdle:   ev.AnimationIdle,
			AnimationOut:    ev.AnimationOut,
			StartUS:         ev.StartUS,
			DurationUS:      ev.DurationUS,
			ResolverVersion: resolverVersion,
			SamplerVersion:  samplerVersion,
		})
	}
	return out
}

// VisualBindingsStore is the durable visual-bindings port. SaveBindings
// upserts a script's bindings (the latest canonical set wins); ListBindings
// returns them in deterministic play order.
type VisualBindingsStore interface {
	SaveBindings(ctx context.Context, scriptID int64, bindings []VisualBinding) error
	ListBindings(ctx context.Context, scriptID int64) ([]VisualBinding, error)
}

// InMemoryVisualBindingsStore is the pure, deterministic VisualBindingsStore
// for tests and non-durable callers. Save replaces the script's whole set
// (upsert-by-event-id semantics), matching the SQLite adapter's behavior.
type InMemoryVisualBindingsStore struct {
	byScript map[int64]map[string]VisualBinding // scriptID → visual_event_id → binding
}

// NewInMemoryVisualBindingsStore returns an empty in-memory bindings store.
func NewInMemoryVisualBindingsStore() *InMemoryVisualBindingsStore {
	return &InMemoryVisualBindingsStore{byScript: map[int64]map[string]VisualBinding{}}
}

// SaveBindings validates then replaces the script's bindings. An invalid
// binding aborts the whole save (no partial state), matching the SQLite
// adapter's transactional behavior.
func (s *InMemoryVisualBindingsStore) SaveBindings(_ context.Context, scriptID int64, bindings []VisualBinding) error {
	if s == nil {
		return errors.New("visual bindings store: nil receiver")
	}
	rows := make(map[string]VisualBinding, len(bindings))
	for _, b := range bindings {
		if err := b.Validate(); err != nil {
			return err
		}
		if b.ScriptID != scriptID {
			return errors.New("visual bindings store: binding script_id does not match")
		}
		rows[b.VisualEventID] = b
	}
	s.byScript[scriptID] = rows
	return nil
}

// ListBindings returns the script's bindings in deterministic play order
// (start_us, then event id) — never map order.
func (s *InMemoryVisualBindingsStore) ListBindings(_ context.Context, scriptID int64) ([]VisualBinding, error) {
	if s == nil {
		return nil, errors.New("visual bindings store: nil receiver")
	}
	rows := s.byScript[scriptID]
	out := make([]VisualBinding, 0, len(rows))
	for _, b := range rows {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartUS != out[j].StartUS {
			return out[i].StartUS < out[j].StartUS
		}
		return out[i].VisualEventID < out[j].VisualEventID
	})
	return out, nil
}
