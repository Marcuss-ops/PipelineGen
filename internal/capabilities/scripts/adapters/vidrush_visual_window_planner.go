package adapters

import (
	"errors"
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VisualWindowPlanningInput contains the canonical scene timing and semantic
// inputs used to derive visual windows without changing the narration.
type VisualWindowPlanningInput struct {
	SceneID       string
	SegmentID     string
	Text          string
	DurationMs    int64
	PhraseTimings []VisualPhraseTiming
	Profile       scriptpkg.SegmentSemanticProfile
}

// VisualPhraseTiming is a local-to-scene phrase interval. It deliberately
// carries no asset identity; provider selection happens after window planning.
type VisualPhraseTiming struct {
	Text    string
	StartMs int64
	EndMs   int64
}

// VidRushVisualWindowPlanner converts semantic beats into canonical
// script.VisualLayer values. The planner never invents asset IDs or splits
// the narrative scene itself.
type VidRushVisualWindowPlanner struct {
	beatPlanner scriptpkg.VisualBeatPlanner
}

func NewVidRushVisualWindowPlanner(policy scriptpkg.VisualBeatPolicy) (*VidRushVisualWindowPlanner, error) {
	planner, err := scriptpkg.NewVisualBeatPlanner(policy)
	if err != nil {
		return nil, err
	}
	return &VidRushVisualWindowPlanner{beatPlanner: *planner}, nil
}

// Plan returns contiguous visual windows covering exactly the scene duration.
// Phrase timings become semantic boundaries when valid; otherwise the
// canonical beat planner provides uniform 4–7 second-oriented subdivision.
func (p VidRushVisualWindowPlanner) Plan(input VisualWindowPlanningInput) ([]scriptpkg.VisualLayer, error) {
	if strings.TrimSpace(input.SceneID) == "" {
		return nil, errors.New("vidrush visual window planner: scene_id is required")
	}
	if strings.TrimSpace(input.SegmentID) == "" {
		return nil, errors.New("vidrush visual window planner: segment_id is required")
	}
	if input.DurationMs <= 0 {
		return nil, errors.New("vidrush visual window planner: duration_ms must be positive")
	}

	blocks := visualWindowBlocks(input)
	if len(blocks) == 0 {
		blocks = fallbackVisualWindowBlocks(input)
	}
	if len(input.PhraseTimings) > 0 && len(blocks) == len(input.PhraseTimings) {
		if len(blocks) > 1 && input.DurationMs/int64(len(blocks)) >= p.beatPlanner.Policy.MinBeatMs {
			layers := make([]scriptpkg.VisualLayer, 0, len(blocks))
			for _, phrase := range input.PhraseTimings {
				layers = append(layers, scriptpkg.VisualLayer{Slot: mediadomain.SlotPrimaryVideo, StartMs: phrase.StartMs, EndMs: phrase.EndMs, DurationMs: phrase.EndMs - phrase.StartMs, Score: 1})
			}
			return layers, nil
		}
	}
	beatPlan, err := p.beatPlanner.Plan(input.SegmentID, input.DurationMs, blocks)
	if err != nil {
		return nil, err
	}
	layers := make([]scriptpkg.VisualLayer, 0, len(beatPlan.Beats))
	for i, beat := range beatPlan.Beats {
		layers = append(layers, scriptpkg.VisualLayer{
			Slot:       mediadomain.SlotPrimaryVideo,
			StartMs:    beat.StartMs,
			EndMs:      beat.EndMs,
			DurationMs: beat.EndMs - beat.StartMs,
			Score:      float64(len(beatPlan.Beats)-i) / float64(len(beatPlan.Beats)),
		})
	}
	return layers, nil
}

func fallbackVisualWindowBlocks(input VisualWindowPlanningInput) []scriptpkg.SegmentVisualBlock {
	count := int((input.DurationMs + scriptpkg.DefaultVisualBeatPolicy.TargetBeatMs/2) / scriptpkg.DefaultVisualBeatPolicy.TargetBeatMs)
	if count < 1 {
		count = 1
	}
	blocks := make([]scriptpkg.SegmentVisualBlock, count)
	for i := range blocks {
		blocks[i] = scriptpkg.SegmentVisualBlock{Text: strings.TrimSpace(input.Text), SemanticProfile: input.Profile}
	}
	return blocks
}

func visualWindowBlocks(input VisualWindowPlanningInput) []scriptpkg.SegmentVisualBlock {
	if len(input.PhraseTimings) == 0 {
		return nil
	}
	blocks := make([]scriptpkg.SegmentVisualBlock, 0, len(input.PhraseTimings))
	for _, phrase := range input.PhraseTimings {
		if strings.TrimSpace(phrase.Text) == "" || phrase.StartMs < 0 || phrase.EndMs <= phrase.StartMs {
			continue
		}
		blocks = append(blocks, scriptpkg.SegmentVisualBlock{
			Text: phrase.Text,
			SemanticProfile: scriptpkg.SegmentSemanticProfile{
				SegmentID: input.SegmentID,
				TextHash:  input.Profile.TextHash,
				Topic:     strings.TrimSpace(phrase.Text),
			},
		})
	}
	return blocks
}

// PlanVisualWindows is a convenience entry point for callers that do not
// need to retain a planner instance.
func PlanVisualWindows(input VisualWindowPlanningInput) ([]scriptpkg.VisualLayer, error) {
	planner, err := NewVidRushVisualWindowPlanner(scriptpkg.DefaultVisualBeatPolicy)
	if err != nil {
		return nil, err
	}
	return planner.Plan(input)
}
