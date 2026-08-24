// Package brain defines the canonical ports and data types for the
// Brain capability. It contains only interfaces and shapes; concrete
// implementations live in sibling packages and are wired by the
// composition root.
//
// These types intentionally mirror some concepts already present in
// other capabilities (e.g. mediamemory.SceneVisualPlan). During the
// brain migration the old capability-specific shapes will be replaced
// by these canonical ones so that every route that produces a visual
// plan passes through the Brain.
package brain

import (
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// BrainRequest is the canonical input to Brain.Resolve.
type BrainRequest struct {
	ProjectID string
	Language  string
	Scenes    []SceneRequest
	Policy    ResolutionPolicy
}

// SceneRequest is one scene inside a BrainRequest.
type SceneRequest struct {
	ID         string
	Text       string
	DurationMS int64
	Slots      []media.SlotKind
}

// ResolutionPolicy controls how the brain resolves candidates for a
// request. Keeping the policy explicit makes the decision reproducible
// and cacheable.
//
// SearchPolicy carries the canonical search knobs that are forwarded
// to the underlying SearchFanOut. The legacy PreferApprovedBindings /
// AllowExternalSearch / MaxCandidatesPerSlot fields are still
// respected when SearchPolicy is zero so existing callers keep working;
// new code should populate SearchPolicy directly.
type ResolutionPolicy struct {
	PreferApprovedBindings bool
	AllowExternalSearch    bool
	MaxCandidatesPerSlot   int
	AvoidRecentAssets      bool
	SearchPolicy           media.ResolutionSearchPolicy
}

// BrainResult is the canonical output of Brain.Resolve.
type BrainResult struct {
	ProjectID string
	Scenes    []SceneVisualPlan
}

// SceneVisualPlan is the canonical visual plan for a single scene.
// It is intentionally independent from transport or storage concerns.
//
// Each scene carries its own ResolutionTrace so operators can
// reconstruct exactly how that scene was resolved, and a
// DecisionFingerprint that uniquely identifies the input+versions
// tuple used to produce the plan.
type SceneVisualPlan struct {
	SceneID             string
	Intent              VisualIntent
	Layers              []VisualLayer
	Confidence          float64
	Status              string
	Trace               ResolutionTrace
	DecisionFingerprint string
}

// VisualIntent describes what the brain understood about a scene.
type VisualIntent struct {
	// Backward-compatible fields. New code should prefer the
	// structured fields below whenever possible.
	Entities []string
	Concepts []string
	Actions  []string
	Keywords []string

	// Structured intent decomposition produced by the language-aware
	// intent resolver registry.
	Topics           []string
	Objects          []string
	VisualActions    []string
	SearchKeywords   []string
	NegativeConcepts []string
}

// VisualLayer is a single resolved layer inside a SceneVisualPlan.
// The brain produces only the plan; materialization happens later
// through the stock pipeline / image pipeline.
type VisualLayer struct {
	Slot                 media.SlotKind
	CandidateID          string
	AssetID              string
	BindingID            string
	StartMs              int64
	EndMs                int64
	MaterializationState string
	Provider             string
	Score                float64
}

// ResolutionTrace records how the brain arrived at its decisions.
// It is meant for diagnostics, reproducibility and feedback loops.
//
// When embedded in SceneVisualPlan, the trace is scoped to that
// single scene. Selected/Excluded records therefore do not repeat
// the scene ID.
type ResolutionTrace struct {
	NormalizedText string
	Versions       ResolutionVersionSet
	BackendCalls   []BackendCall
	Selected       []SelectedRecord
	Excluded       []ExcludedRecord
	Reasons        []string
}

// ResolutionVersionSet tracks every component version that contributed
// to a single resolution. Changing any entry invalidates exact-memory
// hits produced by an older set. The struct is serialized as a whole
// when computing the decision fingerprint, so no version can drift
// out of the fingerprint.
type ResolutionVersionSet struct {
	BrainVersion            string
	NormalizerVersion       string
	IntentResolverVersion   string
	EmbeddingVersion        string
	RankingPolicyVersion    string
	DiversityPolicyVersion  string
	SlotPolicyVersion       string
	ProviderRegistryVersion string
}

// DecisionFingerprint returns a deterministic SHA-256 fingerprint that
// uniquely identifies the (language, normalized text, version set)
// tuple used to produce a visual plan.
func (v ResolutionVersionSet) DecisionFingerprint(language, normalized string) string {
	input := v.fingerprintInput(language, normalized)
	sum := digest.SHA256Bytes([]byte(input))
	return sum
}

// fingerprintPayload is the deterministic serialization envelope for
// the decision fingerprint. It nests ResolutionVersionSet so any new
// version field added to the set is automatically included in the
// fingerprint without touching this struct.
type fingerprintPayload struct {
	Language   string
	Normalized string
	Versions   ResolutionVersionSet
}

func (v ResolutionVersionSet) fingerprintInput(language, normalized string) string {
	p := fingerprintPayload{
		Language:   language,
		Normalized: normalized,
		Versions:   v,
	}
	b, err := json.Marshal(p)
	if err != nil {
		// fingerprintPayload only contains strings, so this cannot
		// fail in practice; treat a marshal failure as an unrecoverable
		// programming error rather than producing an unstable fingerprint.
		panic(fmt.Sprintf("brain: fingerprint serialization failed: %v", err))
	}
	return string(b)
}

// BackendCall records one backend invocation performed by the brain.
type BackendCall struct {
	Backend string
	Hits    int
	Error   string
}

// SelectedRecord records a candidate selected by the brain. It is
// scoped to the containing ResolutionTrace, which itself lives inside
// a single SceneVisualPlan, so it does not repeat the scene ID.
type SelectedRecord struct {
	Slot        media.SlotKind
	AssetID     string
	CandidateID string
	Score       float64
}

// ExcludedRecord records a candidate excluded by the brain and why.
type ExcludedRecord struct {
	Slot    media.SlotKind
	AssetID string
	Reason  string
}
