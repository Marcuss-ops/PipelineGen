package adapters

import (
	"encoding/json"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushSegmentExplanation is the compact, inspectable decision record for a
// segment. It is derived from existing candidates and never changes selection.
type VidRushSegmentExplanation struct {
	SegmentID           string                    `json:"segment_id"`
	ProvidersConsidered []ProviderPreference      `json:"providers_considered"`
	Winner              *VidRushWinnerExplanation `json:"winner,omitempty"`
}

type VidRushWinnerExplanation struct {
	Provider      string  `json:"provider"`
	Score         float64 `json:"score"`
	Reason        string  `json:"reason"`
	SourceURL     string  `json:"source_url,omitempty"`
	SourceStartMs int64   `json:"start_ms"`
	SourceEndMs   int64   `json:"end_ms"`
	AssetID       string  `json:"asset_id"`
}

func ExplainVidRushSegment(plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult, contentType string) VidRushSegmentExplanation {
	selection, err := NewVidRushProviderSelector().Select(plan, segment, contentType)
	out := VidRushSegmentExplanation{SegmentID: segment.SegmentID}
	if err == nil {
		out.ProvidersConsidered = append([]ProviderPreference{}, selection.Preferences...)
	}
	if out.ProvidersConsidered == nil {
		out.ProvidersConsidered = []ProviderPreference{}
	}
	winner := segment.Assets.PrimaryVideo
	if winner == nil && len(segment.Assets.Candidates) > 0 {
		for i := range segment.Assets.Candidates {
			c := &segment.Assets.Candidates[i]
			if winner == nil || c.Score > winner.Score || (c.Score == winner.Score && (c.Provider < winner.Provider || (c.Provider == winner.Provider && c.AssetID < winner.AssetID))) {
				winner = c
			}
		}
	}
	if winner != nil {
		reason := strings.TrimSpace(winner.SelectionReason)
		if reason == "" {
			selected := ""
			if err == nil {
				selected = selection.Selected
			}
			reason = selectionReason(selected)
		}
		out.Winner = &VidRushWinnerExplanation{Provider: winner.Provider, Score: winner.Score, Reason: reason, SourceURL: winner.SourceURL, SourceStartMs: winner.SourceStartMs, SourceEndMs: winner.SourceEndMs, AssetID: winner.AssetID}
	}
	return out
}
func selectionReason(provider string) string {
	if provider == "" {
		return "no selected provider"
	}
	return "selected by deterministic provider policy and candidate score"
}
func (e VidRushSegmentExplanation) MarshalJSON() ([]byte, error) {
	type alias VidRushSegmentExplanation
	return json.Marshal(alias(e))
}
