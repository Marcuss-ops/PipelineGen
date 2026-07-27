// Package mediamemory — types_resolver.go is the canonical home
// for the VisualResolver IO wire shapes: VisualIntent (sentence/
// phrase splitter → resolver), SceneSpec (one scene from a
// project), Layer (one entry in a SceneVisualPlan), CandidateOption
// (alternative candidates for the dashboard preview),
// SceneIntent / SceneBackendCall / SceneResolutionTrace (brain-
// backed diagnostics mirror envelopes that do NOT import brain),
// SceneVisualPlan (canonical output consumed by the headless
// renderer), ResolvePolicy + OptionalResolvePolicy (controller
// knobs handed to VisualResolver), ResolveRequest (top-level
// controller input) + ResolveResult (per-project batched output).
//
// godlike/06 SSOT (lossless brain-mirror envelopes): SceneIntent,
// SceneBackendCall, and SceneResolutionTrace mirror the canonical
// brain types WITHOUT importing the brain package — they carry
// the wire-subset that's useful for diagnostics on the dashboard.
// Drift between the mediamemory SSOT envelope and the brain
// origin is caught by Fase 4 dashboard conformance tests.
//
// godlike/06 SSOT (3-layer renderer ceiling): SceneVisualPlan
// carries 1–3 layers per scene (godlike/06 SSOT: 1 ≤ len(Layers) ≤
// 3 for current renderer). Exceeding it is forbidden; the ranker
// already caps at len(scene.Slots) ≤ 3 by API convention.
//
// File split ownership (godlike/06 SSOT):
//   - types.go               : package doc + SlotKind alias
//   - types_enums.go         : 9 enums + their constants + 9 IsKnown predicates + Provider tag constants + IsKnownProvider
//   - types_entities.go      : MediaConcept + MediaBinding + MediaCandidate + BatchSpec + Batch + BatchChild + UsageEvent
//   - types_resolver.go      : VisualIntent + SceneSpec + Layer + CandidateOption + SceneIntent + SceneBackendCall + SceneResolutionTrace + SceneVisualPlan + ResolvePolicy + OptionalResolvePolicy + ResolveRequest + ResolveResult  ← this file
//   - types_linker.go        : LinkerRequest + LinkerResult + EncodingChannels + MediaEmbedding + TranscriptSegment + Keyframe
//   - types_sentinels.go     : 19 sentinel errors (14 phase 1.x + 5 ErrLinker*)
package mediamemory

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// VisualIntent is the resolver input. Produced by the upstream
// sentence/phrase splitter and consumed by VisualResolver.Resolve.
type VisualIntent struct {
	Text           string
	Language       string
	Entities       []string
	Concepts       []string
	VisualActions  []string
	PreferredSlots []media.SlotKind
}

// SceneSpec is one scene from a project. The resolver merges a
// ResolveRequest (project-shared) with N SceneSpecs to produce N
// LayerGroups.
type SceneSpec struct {
	ID         string
	Text       string
	DurationMs int64
	Slots      []media.SlotKind
	Language   string
	// SceneConcepts is the Fase 4.3 per-scene concept_id
	// list. godlike/06 SSOT (scene-concepts union): when
	// non-empty it overrides the request-level
	// PlanGeneratorRequest.SceneConcepts so the
	// SceneVisualPlanGenerator scopes pickBindingForSlot to
	// the scene's actual concept set. Empty (the default)
	// falls back to the request-level filter.
	SceneConcepts []string
}

// Layer is one entry in a SceneVisualPlan.
type Layer struct {
	Slot           media.SlotKind
	AssetID        string
	CandidateID    string
	BindingID      string
	StartMs        int64
	EndMs          int64
	Layout         string // "fullscreen", "right_panel", "fullscreen_fade", ...
	CandidateScore float64
	// Provider is the canonical source tag from the winning
	// MediaCandidate that produced this layer (godlike/06
	// SSOT propagation: the Level 3-7 semantic adapter
	// stamps mediamemory.ProviderSemanticIndex, the Level 9
	// SearchFanOutAdapter stamps the forwarding provider
	// name, ...).
	Provider string
}

type CandidateOption struct {
	AssetID      string
	CandidateID  string
	SourceURL    string
	Provider     string
	Score        float64
	DurationMs   int64
	MediaType    string
	RightsStatus string
}

// SceneIntent captures what the brain understood about a scene.
// It mirrors the brain's VisualIntent without importing the brain
// package into the mediamemory SSOT.
type SceneIntent struct {
	Entities []string
	Concepts []string
	Actions  []string
	Keywords []string
}

// SceneBackendCall records one backend invocation performed by the
// brain for a scene. It mirrors brain.BackendCall.
type SceneBackendCall struct {
	Backend string
	Hits    int
	Error   string
}

// SceneResolutionTrace records how the brain arrived at its
// decisions for a scene. It mirrors brain.ResolutionTrace, scoped
// to the subset of fields useful for diagnostics on the wire.
type SceneResolutionTrace struct {
	NormalizedText string
	BackendCalls   []SceneBackendCall
	Reasons        []string
}

// SceneVisualPlan is the canonical output of the ranker, consumed
// by the headless renderer. The plan carries 1–3 layers per scene
// (godlike/06 SSOT: 1 ≤ len(Layers) ≤ 3 for current renderer).
type SceneVisualPlan struct {
	ProjectID  string
	SceneID    string
	SegmentID  string
	Text       string
	Language   string
	DurationMs int64
	Layers     []Layer
	Source     string // "exact", "semantic", "local", "external", "mixed"
	// Intent, Trace and DecisionFingerprint are produced by the
	// brain-backed resolver to aid debugging. They are optional:
	// the legacy VisualResolver leaves them zero-valued.
	Intent              SceneIntent
	Trace               SceneResolutionTrace
	DecisionFingerprint string
	Candidates          []CandidateOption
}

// ResolvePolicy bundles the controller knobs that VisualResolver
// reads on each Resolve call.
//
// godlike/06 SSOT: the MaxExternalMaterializations knob was
// retired in Fase 1.5 cleanup — materialization is owned by the
// BatchService.MaterializeTopK path (BatchSpec.MaterializeTopK),
// not by per-request policy. The remaining knobs are the live
// controls the dashboard preview / API consumers supply.
//
// SearchPolicy carries the canonical search knobs forwarded to the
// underlying SearchFanOut. Legacy fields are still honoured when
// SearchPolicy is zero so existing callers keep working; new code
// should populate SearchPolicy directly.
type ResolvePolicy struct {
	PreferApprovedBindings bool
	AllowExternalSearch    bool
	MaxCandidatesPerSlot   int
	AvoidRecentAssets      bool
	SearchPolicy           media.ResolutionSearchPolicy
}

// OptionalResolvePolicy carries the client-supplied overrides before
// canonical defaults are applied. Pointer bools distinguish "field
// absent" from "field explicitly false". The zero value means "use
// the application-layer defaults" (see ResolutionPolicyResolver).
//
// godlike/06 SSOT: the API layer maps its wire DTO directly to this
// struct and does NOT apply defaults; defaulting is the sole
// responsibility of ResolutionPolicyResolver in the application
// layer.
type OptionalResolvePolicy struct {
	PreferApprovedBindings *bool
	AllowExternalSearch    *bool
	MaxCandidatesPerSlot   int
	AvoidRecentAssets      *bool
	Mode                   string
	AllowedProviders       []string
	CacheRead              *bool
}

// ResolveRequest is the top-level controller input to the resolver.
type ResolveRequest struct {
	ProjectID string
	Language  string
	Scenes    []SceneSpec
	Policy    ResolvePolicy
}

// ResolveResult is the per-project batched output of ResolveRequest.
type ResolveResult struct {
	ProjectID string
	Plans     []SceneVisualPlan
	Warnings  []string
}
