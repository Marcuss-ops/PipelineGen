package media

// VisualSelectionMode controls who fills a visual timeline slot.
type VisualSelectionMode string

const (
	VisualSelectionManual    VisualSelectionMode = "manual"
	VisualSelectionAssisted  VisualSelectionMode = "assisted"
	VisualSelectionHybrid    VisualSelectionMode = "hybrid"
	VisualSelectionGemma     VisualSelectionMode = "gemma"
	VisualSelectionAutomatic VisualSelectionMode = "automatic"
	VisualSelectionAuto      VisualSelectionMode = "auto"
)

// VisualSlotPlan is a closed-candidate plan for intro/outro/post-segment
// clips. Manual clips are ordered as supplied; locked clips are immutable.
type VisualSlotPlan struct {
	Mode              VisualSelectionMode `json:"mode"`
	Clips             []VisualClip        `json:"clips,omitempty"`
	TargetDurationMs  int64               `json:"target_duration_ms,omitempty"`
	MaxClips          int                 `json:"max_clips,omitempty"`
	CandidateAssetIDs []string            `json:"candidate_asset_ids,omitempty"`
	Goal              string              `json:"goal,omitempty"`
}

type PostSegmentVisualPlan struct {
	SegmentID string `json:"segment_id"`
	VisualSlotPlan
}

type VisualClip struct {
	AssetID    string `json:"asset_id"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	StartMs    int64  `json:"start_ms,omitempty"`
	Locked     bool   `json:"locked,omitempty"`
	Position   *int   `json:"position,omitempty"`
}

type VisualVariationPolicy struct {
	Seed                 int64 `json:"seed,omitempty"`
	PreserveLocked       bool  `json:"preserve_locked,omitempty"`
	ShuffleUnlocked      bool  `json:"shuffle_unlocked,omitempty"`
	ResampleUnlocked     bool  `json:"resample_unlocked,omitempty"`
	AvoidPreviousVariant bool  `json:"avoid_previous_variant,omitempty"`
}

func (p VisualSlotPlan) Clone() VisualSlotPlan {
	p.Clips = append([]VisualClip(nil), p.Clips...)
	p.CandidateAssetIDs = append([]string(nil), p.CandidateAssetIDs...)
	return p
}

// VisualSlot is the common registry for timeline-level and scene-level slots.
type VisualSlot string

const (
	VisualSlotIntro          VisualSlot = "intro"
	VisualSlotSecondaryVideo VisualSlot = "secondary_video"
	VisualSlotEntityImage    VisualSlot = "entity_image"
	VisualSlotPostSegment    VisualSlot = "post_segment"
	VisualSlotTransition     VisualSlot = "transition"
	VisualSlotOutro          VisualSlot = "outro"
)

func (s VisualSlot) IsValid() bool {
	switch s {
	case VisualSlotIntro, VisualSlotPrimaryVideo, VisualSlotSecondaryVideo,
		VisualSlotSecondaryImage, VisualSlotEntityImage, VisualSlotEvidence,
		VisualSlotPostSegment, VisualSlotTransition, VisualSlotOutro:
		return true
	default:
		return false
	}
}

type VisualSelectedBy string

const (
	VisualSelectedByUser     VisualSelectedBy = "user"
	VisualSelectedByGemma    VisualSelectedBy = "gemma"
	VisualSelectedBySampler  VisualSelectedBy = "sampler"
	VisualSelectedByCache    VisualSelectedBy = "cache"
	VisualSelectedByFallback VisualSelectedBy = "fallback"
)

// VisualAssignment is the durable, renderer-neutral result of resolving a
// visual slot. It records the complete selection provenance.
type VisualAssignment struct {
	AssignmentID    string           `json:"assignment_id"`
	SceneID         string           `json:"scene_id,omitempty"`
	SegmentID       string           `json:"segment_id,omitempty"`
	Slot            VisualSlot       `json:"slot"`
	AssetID         string           `json:"asset_id"`
	Position        int              `json:"position"`
	DurationMs      int64            `json:"duration_ms,omitempty"`
	StartMs         int64            `json:"start_ms,omitempty"`
	Locked          bool             `json:"locked,omitempty"`
	SelectedBy      VisualSelectedBy `json:"selected_by"`
	SelectionReason string           `json:"selection_reason,omitempty"`
	VariationSeed   int64            `json:"variation_seed,omitempty"`
	PromptVersion   string           `json:"prompt_version,omitempty"`
}
