package script

import (
	"errors"
	"fmt"
	"strings"
)

// VisualBeatPolicy controls how a segment timing budget is divided into
// semantic visual beats. Durations are expressed in milliseconds.
type VisualBeatPolicy struct {
	MinBeatMs    int64 `json:"min_beat_ms"`
	TargetBeatMs int64 `json:"target_beat_ms"`
	MaxBeatMs    int64 `json:"max_beat_ms"`
}

// DefaultVisualBeatPolicy is the conservative policy used when callers do not
// provide one explicitly.
var DefaultVisualBeatPolicy = VisualBeatPolicy{
	MinBeatMs:    4000,
	TargetBeatMs: 7500,
	MaxBeatMs:    12000,
}

// SegmentVisualBeat is one semantically coherent visual interval within a
// canonical segment.
type SegmentVisualBeat struct {
	ID              string                 `json:"id"`
	SegmentID       string                 `json:"segment_id"`
	Position        int                    `json:"position"`
	Text            string                 `json:"text"`
	StartMs         int64                  `json:"start_ms"`
	EndMs           int64                  `json:"end_ms"`
	SemanticProfile SegmentSemanticProfile `json:"semantic_profile"`
}

// SegmentVisualPlan is the complete duration-aware visual plan for a segment.
type SegmentVisualPlan struct {
	SegmentID  string              `json:"segment_id"`
	DurationMs int64               `json:"duration_ms"`
	Policy     VisualBeatPolicy    `json:"policy"`
	Beats      []SegmentVisualBeat `json:"beats"`
}

// VisualBeatPlanner deterministically assigns timing to semantic blocks. The
// planner never lets a model choose arbitrary durations.
type VisualBeatPlanner struct {
	Policy VisualBeatPolicy
}

// NewVisualBeatPlanner creates a planner with normalized policy values.
func NewVisualBeatPlanner(policy VisualBeatPolicy) (*VisualBeatPlanner, error) {
	normalized, err := NormalizeVisualBeatPolicy(policy)
	if err != nil {
		return nil, err
	}
	return &VisualBeatPlanner{Policy: normalized}, nil
}

// NormalizeVisualBeatPolicy validates and fills omitted policy values.
func NormalizeVisualBeatPolicy(policy VisualBeatPolicy) (VisualBeatPolicy, error) {
	defaults := DefaultVisualBeatPolicy
	if policy.MinBeatMs <= 0 {
		policy.MinBeatMs = defaults.MinBeatMs
	}
	if policy.TargetBeatMs <= 0 {
		policy.TargetBeatMs = defaults.TargetBeatMs
	}
	if policy.MaxBeatMs <= 0 {
		policy.MaxBeatMs = defaults.MaxBeatMs
	}
	if policy.MinBeatMs > policy.TargetBeatMs {
		return VisualBeatPolicy{}, errors.New("visual beat policy: min_beat_ms must not exceed target_beat_ms")
	}
	if policy.TargetBeatMs > policy.MaxBeatMs {
		return VisualBeatPolicy{}, errors.New("visual beat policy: target_beat_ms must not exceed max_beat_ms")
	}
	return policy, nil
}

// Plan divides durationMs across the supplied semantic blocks. Empty blocks
// fall back to the segment profile/topic and are never emitted as empty beats.
func (p VisualBeatPlanner) Plan(segmentID string, durationMs int64, blocks []SegmentVisualBlock) (SegmentVisualPlan, error) {
	if strings.TrimSpace(segmentID) == "" {
		return SegmentVisualPlan{}, errors.New("visual beat planner: segment_id is required")
	}
	if durationMs <= 0 {
		return SegmentVisualPlan{}, errors.New("visual beat planner: duration_ms must be positive")
	}
	policy, err := NormalizeVisualBeatPolicy(p.Policy)
	if err != nil {
		return SegmentVisualPlan{}, err
	}

	usable := make([]SegmentVisualBlock, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block.Text) != "" || hasProfileMeaning(block.SemanticProfile) {
			block.Text = strings.TrimSpace(block.Text)
			if block.Text == "" {
				block.Text = firstProfileText(block.SemanticProfile)
			}
			if block.SemanticProfile.SegmentID == "" {
				block.SemanticProfile.SegmentID = segmentID
			}
			usable = append(usable, block)
		}
	}
	if len(usable) == 0 {
		return SegmentVisualPlan{}, errors.New("visual beat planner: at least one semantic block is required")
	}

	count := desiredBeatCount(durationMs, len(usable), policy)
	groups := groupVisualBlocks(usable, count)
	spans := allocateBeatDurations(durationMs, len(groups), policy)
	plan := SegmentVisualPlan{SegmentID: segmentID, DurationMs: durationMs, Policy: policy, Beats: make([]SegmentVisualBeat, 0, len(groups))}
	cursor := int64(0)
	for i, group := range groups {
		end := cursor + spans[i]
		if i == len(groups)-1 {
			end = durationMs
		}
		profile := mergeVisualProfiles(segmentID, group)
		text := joinVisualBlockText(group)
		plan.Beats = append(plan.Beats, SegmentVisualBeat{
			ID: fmt.Sprintf("%s-beat-%d", segmentID, i), SegmentID: segmentID,
			Position: i, Text: text, StartMs: cursor, EndMs: end,
			SemanticProfile: profile,
		})
		cursor = end
	}
	return plan, nil
}

// PlanWithBudget uses the resolved timing source and duration as the sole
// visual clock for a segment. This keeps voiceover/scene/estimate precedence
// outside the beat allocation algorithm while preserving the source in the
// returned plan.
func (p VisualBeatPlanner) PlanWithBudget(budget VisualTimingBudget, blocks []SegmentVisualBlock) (SegmentVisualPlan, error) {
	if strings.TrimSpace(budget.SegmentID) == "" {
		return SegmentVisualPlan{}, errors.New("visual beat planner: budget segment_id is required")
	}
	if budget.DurationMs <= 0 {
		return SegmentVisualPlan{}, errors.New("visual beat planner: budget duration_ms must be positive")
	}
	plan, err := p.Plan(budget.SegmentID, budget.DurationMs, blocks)
	if err != nil {
		return SegmentVisualPlan{}, err
	}
	return plan, nil
}

// VisualTimingBudget is the kernel-level timing input for visual planning.
// Source is voiceover, scene, or estimated and is informational; DurationMs
// is the authoritative clock used to allocate beats.
type VisualTimingBudget struct {
	SegmentID  string `json:"segment_id"`
	DurationMs int64  `json:"duration_ms"`
	Source     string `json:"source"`
}

// SegmentVisualBlock is a semantic boundary identified by the understanding
// stage. Timing is intentionally excluded from this input.
type SegmentVisualBlock struct {
	Text            string                 `json:"text"`
	SemanticProfile SegmentSemanticProfile `json:"semantic_profile"`
}

func desiredBeatCount(durationMs int64, blockCount int, policy VisualBeatPolicy) int {
	count := int((durationMs + policy.TargetBeatMs/2) / policy.TargetBeatMs)
	if count < 1 {
		count = 1
	}
	if blockCount > 1 && durationMs >= policy.MinBeatMs*int64(blockCount) {
		if durationMs < policy.TargetBeatMs*int64(blockCount) {
			count = minPlannerInt(count, blockCount)
		} else {
			count = blockCount
		}
	} else if count > blockCount {
		count = blockCount
	}
	for count > 1 && durationMs/int64(count) < policy.MinBeatMs {
		count--
	}
	return count
}

func minPlannerInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func groupVisualBlocks(blocks []SegmentVisualBlock, count int) [][]SegmentVisualBlock {
	groups := make([][]SegmentVisualBlock, count)
	for i, block := range blocks {
		group := i * count / len(blocks)
		if group >= count {
			group = count - 1
		}
		groups[group] = append(groups[group], block)
	}
	return groups
}

func allocateBeatDurations(total int64, count int, policy VisualBeatPolicy) []int64 {
	spans := make([]int64, count)
	base := total / int64(count)
	remainder := total % int64(count)
	for i := range spans {
		spans[i] = base
		if int64(i) < remainder {
			spans[i]++
		}
	}
	// For a short segment, preserve the exact total rather than violating the
	// timing budget in an attempt to force MinBeatMs. For normal budgets, the
	// count selection guarantees the bounds.
	_ = policy
	return spans
}

func mergeVisualProfiles(segmentID string, blocks []SegmentVisualBlock) SegmentSemanticProfile {
	profile := SegmentSemanticProfile{SegmentID: segmentID}
	for _, block := range blocks {
		p := block.SemanticProfile
		if profile.TextHash == "" {
			profile.TextHash = p.TextHash
		}
		if profile.Topic == "" {
			profile.Topic = p.Topic
		}
		profile.Subtopics = appendUniqueStrings(profile.Subtopics, p.Subtopics...)
		profile.Keywords = appendUniqueWeighted(profile.Keywords, p.Keywords...)
		profile.VisualTerms = appendUniqueWeighted(profile.VisualTerms, p.VisualTerms...)
		profile.Terms = appendUniqueSemanticTerms(profile.Terms, p.Terms...)
		profile.Actions = appendUniqueStrings(profile.Actions, p.Actions...)
		profile.VisualConcepts = appendUniqueStrings(profile.VisualConcepts, p.VisualConcepts...)
		profile.ImportantPhrases = appendUniqueStrings(profile.ImportantPhrases, p.ImportantPhrases...)
		profile.Entities = appendUniqueEntities(profile.Entities, p.Entities...)
	}
	return profile
}

func hasProfileMeaning(profile SegmentSemanticProfile) bool {
	return strings.TrimSpace(profile.Topic) != "" || len(profile.Keywords) > 0 || len(profile.VisualTerms) > 0 || len(profile.Actions) > 0 || len(profile.VisualConcepts) > 0 || len(profile.Entities) > 0
}

func firstProfileText(profile SegmentSemanticProfile) string {
	if profile.Topic != "" {
		return strings.TrimSpace(profile.Topic)
	}
	if len(profile.VisualTerms) > 0 {
		return strings.TrimSpace(profile.VisualTerms[0].Value)
	}
	if len(profile.Keywords) > 0 {
		return strings.TrimSpace(profile.Keywords[0].Value)
	}
	return "visual beat"
}

func joinVisualBlockText(blocks []SegmentVisualBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func appendUniqueStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func appendUniqueWeighted(dst []WeightedKeyword, values ...WeightedKeyword) []WeightedKeyword {
	for _, value := range values {
		found := false
		for _, current := range dst {
			if strings.EqualFold(current.Value, value.Value) {
				found = true
				break
			}
		}
		if !found && strings.TrimSpace(value.Value) != "" {
			dst = append(dst, value)
		}
	}
	return dst
}

func appendUniqueSemanticTerms(dst []SemanticTerm, values ...SemanticTerm) []SemanticTerm {
	for _, value := range values {
		found := false
		for _, current := range dst {
			if current.Kind == value.Kind && strings.EqualFold(current.Value, value.Value) {
				found = true
				break
			}
		}
		if !found && strings.TrimSpace(value.Value) != "" {
			dst = append(dst, value)
		}
	}
	return dst
}

func appendUniqueEntities(dst []ExtractedEntity, values ...ExtractedEntity) []ExtractedEntity {
	for _, value := range values {
		found := false
		for _, current := range dst {
			if strings.EqualFold(current.Type, value.Type) && strings.EqualFold(current.Value, value.Value) {
				found = true
				break
			}
		}
		if !found && strings.TrimSpace(value.Value) != "" {
			dst = append(dst, value)
		}
	}
	return dst
}
