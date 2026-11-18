package clipsequence

import (
	"fmt"
)

// Transition is the incoming transition declared by a clip.
type Transition struct {
	Preset Preset `json:"preset"`
	// DurationMS is the overlap in milliseconds. Zero uses the preset default;
	// the resolved value is what the plan and the renderer see.
	DurationMS int64 `json:"duration_ms,omitempty"`
}

// Clip is one rendered deliverable clip in editorial order.
type Clip struct {
	// AssetID is the certified asset identity of the rendered clip.
	AssetID string `json:"asset_id"`
	// Path is the local artifact the assembler reads. The contract does not
	// require it (a plan can be computed before materialization), but a merger
	// does.
	Path string `json:"path,omitempty"`
	// DurationMS is the clip's own length, before any transition overlap.
	DurationMS int64 `json:"duration_ms"`
	// TransitionIn joins this clip to the PREVIOUS one. Nil means a hard cut
	// and, by the merge rule, the start of a new artifact.
	TransitionIn *Transition `json:"transition_in,omitempty"`
}

// Sequence is the editorial order of clips.
type Sequence struct {
	Clips []Clip `json:"clips"`
}

// GroupTransition is one resolved transition inside a merged group.
type GroupTransition struct {
	Preset     Preset `json:"preset"`
	DurationMS int64  `json:"duration_ms"`
	// FromClip/ToClip are indexes into Sequence.Clips.
	FromClip int `json:"from_clip"`
	ToClip   int `json:"to_clip"`
}

// Group is one output artifact. A group with a single clip is NOT re-encoded:
// the merged artifact is the clip itself.
type Group struct {
	ID          string            `json:"id"`
	ClipIndexes []int             `json:"clip_indexes"`
	AssetIDs    []string          `json:"asset_ids"`
	Transitions []GroupTransition `json:"transitions,omitempty"`
	// DurationMS is the artifact length: the sum of the clip lengths minus the
	// transition overlaps.
	DurationMS int64 `json:"duration_ms"`
	// Merged reports whether the artifact is produced by joining clips. False
	// means the single clip is the artifact.
	Merged bool `json:"merged"`
}

// Plan is the deterministic merge decision for a sequence.
type Plan struct {
	Groups []Group `json:"groups"`
	// DurationMS is the total length: the sum of the group lengths.
	DurationMS int64 `json:"duration_ms"`
}

// resolveTransition validates one transition and resolves its duration.
func resolveTransition(from, to Clip, transition *Transition) (int64, error) {
	spec, ok := LookupPreset(transition.Preset)
	if !ok {
		return 0, fmt.Errorf("transition preset %q is not certified", transition.Preset)
	}
	duration := transition.DurationMS
	if duration == 0 {
		duration = spec.DefaultDurationMS
	}
	if duration < MinTransitionMS || duration > MaxFastTransitionMS {
		return 0, fmt.Errorf("transition preset %q duration_ms must be in [%d,%d] (fast transitions only)",
			transition.Preset, MinTransitionMS, MaxFastTransitionMS)
	}
	for _, clip := range []Clip{from, to} {
		limit := clip.DurationMS * MaxTransitionFractionPercent / 100
		if limit < MinTransitionMS {
			limit = MinTransitionMS
		}
		if duration > limit {
			return 0, fmt.Errorf("transition %q (%d ms) exceeds %d%% of clip %q (%d ms)",
				transition.Preset, duration, MaxTransitionFractionPercent, clip.AssetID, clip.DurationMS)
		}
	}
	return duration, nil
}

// Plan validates the sequence and returns the merge decision.
//
// The rule is the producer's, applied literally: clip i joins the group of clip
// i-1 if and only if clip i declares a transition. A clip without a transition
// closes the current group and opens a new one, so two clips that were meant to
// follow one another are never fused by accident.
func (s Sequence) Plan() (Plan, error) {
	if len(s.Clips) == 0 {
		return Plan{}, fmt.Errorf("clip sequence requires at least one clip")
	}
	for i, clip := range s.Clips {
		if clip.DurationMS <= 0 {
			return Plan{}, fmt.Errorf("clips[%d] (%q) duration_ms must be positive", i, clip.AssetID)
		}
		if clip.AssetID == "" {
			return Plan{}, fmt.Errorf("clips[%d].asset_id is required", i)
		}
	}
	if s.Clips[0].TransitionIn != nil {
		return Plan{}, fmt.Errorf("clips[0] cannot declare transition_in: there is no previous clip to join")
	}
	plan := Plan{Groups: make([]Group, 0, len(s.Clips))}
	for i, clip := range s.Clips {
		if i == 0 || clip.TransitionIn == nil {
			plan.Groups = append(plan.Groups, Group{
				ClipIndexes: []int{i},
				AssetIDs:    []string{clip.AssetID},
				DurationMS:  clip.DurationMS,
			})
			continue
		}
		duration, err := resolveTransition(s.Clips[i-1], clip, clip.TransitionIn)
		if err != nil {
			return Plan{}, fmt.Errorf("clips[%d]: %w", i, err)
		}
		group := &plan.Groups[len(plan.Groups)-1]
		group.ClipIndexes = append(group.ClipIndexes, i)
		group.AssetIDs = append(group.AssetIDs, clip.AssetID)
		group.Transitions = append(group.Transitions, GroupTransition{
			Preset:     clip.TransitionIn.Preset,
			DurationMS: duration,
			FromClip:   i - 1,
			ToClip:     i,
		})
		group.DurationMS += clip.DurationMS - duration
	}
	plan.DurationMS = 0
	for i := range plan.Groups {
		group := &plan.Groups[i]
		group.ID = fmt.Sprintf("group-%02d", i+1)
		group.Merged = len(group.ClipIndexes) > 1
		plan.DurationMS += group.DurationMS
	}
	return plan, nil
}
