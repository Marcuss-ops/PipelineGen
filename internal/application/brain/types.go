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

// SlotKind identifies the visual slot a layer may occupy in a scene.
// It is a closed set; new values require a code change.
type SlotKind string

const (
	SlotPrimaryVideo    SlotKind = "primary_video"
	SlotSecondaryImage  SlotKind = "secondary_image"
	SlotEvidenceOverlay SlotKind = "evidence_overlay"
	SlotMap             SlotKind = "map"
	SlotPortrait        SlotKind = "portrait"
	SlotDocument        SlotKind = "document"
	SlotBackground      SlotKind = "background"
)

// IsKnownSlotKind reports whether k is a supported slot kind.
func IsKnownSlotKind(k SlotKind) bool {
	switch k {
	case SlotPrimaryVideo, SlotSecondaryImage, SlotEvidenceOverlay,
		SlotMap, SlotPortrait, SlotDocument, SlotBackground:
		return true
	}
	return false
}

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
	Slots      []SlotKind
}

// ResolutionPolicy controls how the brain resolves candidates for a
// request. Keeping the policy explicit makes the decision reproducible
// and cacheable.
type ResolutionPolicy struct {
	PreferApprovedBindings bool
	AllowExternalSearch    bool
	MaxCandidatesPerSlot   int
	AvoidRecentAssets      bool
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
	SceneID           string
	Intent            VisualIntent
	Layers            []VisualLayer
	Confidence        float64
	Status            string
	Trace             ResolutionTrace
	DecisionFingerprint string
}

// VisualIntent describes what the brain understood about a scene.
type VisualIntent struct {
	Entities []string
	Concepts []string
	Actions  []string
	Keywords []string
}

// VisualLayer is a single resolved layer inside a SceneVisualPlan.
// The brain produces only the plan; materialization happens later
// through the stock pipeline / image pipeline.
type VisualLayer struct {
	Slot                 SlotKind
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
	Versions       ResolutionVersions
	BackendCalls   []BackendCall
	Selected       []SelectedRecord
	Excluded       []ExcludedRecord
	Reasons        []string
}

// ResolutionVersions tracks the component versions that contributed
// to a single resolution. Changing any of these must invalidate
// exact-memory hits produced by an older set.
type ResolutionVersions struct {
	BrainVersion           string
	NormalizerVersion      string
	IntentResolverVersion  string
	EmbeddingVersion       string
	RankingPolicyVersion   string
	DiversityPolicyVersion string
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
	Slot        SlotKind
	AssetID     string
	CandidateID string
	Score       float64
}

// ExcludedRecord records a candidate excluded by the brain and why.
type ExcludedRecord struct {
	Slot   SlotKind
	AssetID string
	Reason  string
}
