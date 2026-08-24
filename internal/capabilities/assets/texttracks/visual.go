package assets

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// VisualTrackInput is the output of the visual-analysis/translation provider.
// Translation providers supply text only; timing always comes from Original.
type VisualTrackInput struct {
	Language string
	Summary  string
	Events   []string
}

type VisualTrack struct {
	Track asset.TextTrack
	Cues  []asset.TimedCue
}

// BuildVisualTracks materializes the original and translated visual tracks in
// the canonical text-track shape. It never calls Whisper and rejects a
// translation whose event cardinality differs from the source timeline.
func BuildVisualTracks(assetID string, original asset.VisualAnalysis, translations []VisualTrackInput, provider, model string) ([]VisualTrack, error) {
	if strings.TrimSpace(assetID) == "" {
		return nil, fmt.Errorf("visual tracks: asset id is required")
	}
	if err := original.Validate(0); err != nil {
		return nil, err
	}
	events := original.SortedEvents()
	tracks := []VisualTrack{{Track: asset.TextTrack{AssetID: assetID, LanguageCode: "en", TextKind: asset.TextTrackVisualSummary, TextContent: original.Summary, SourceType: asset.TextSourceVisualAnalysis, SourceLanguageCode: "en", IsOriginal: true, Provider: provider, ModelName: model, Status: asset.TextTrackReady}, Cues: cuesFromEvents(events)}}
	for _, tr := range translations {
		if strings.TrimSpace(tr.Language) == "" || tr.Language == "en" {
			return nil, fmt.Errorf("visual tracks: invalid translation language %q", tr.Language)
		}
		if len(tr.Events) != len(events) {
			return nil, fmt.Errorf("visual tracks: %s has %d events, want %d", tr.Language, len(tr.Events), len(events))
		}
		cues := make([]asset.TimedCue, len(events))
		for i, event := range events {
			if strings.TrimSpace(tr.Events[i]) == "" {
				return nil, fmt.Errorf("visual tracks: empty event %d in %s", i, tr.Language)
			}
			cues[i] = asset.TimedCue{StartMs: event.StartMs, EndMs: event.EndMs, Text: tr.Events[i]}
		}
		tracks = append(tracks, VisualTrack{Track: asset.TextTrack{AssetID: assetID, LanguageCode: tr.Language, TextKind: asset.TextTrackVisualSummary, TextContent: tr.Summary, SourceType: asset.TextSourceTranslation, SourceLanguageCode: "en", IsOriginal: false, Provider: provider, ModelName: model, Status: asset.TextTrackReady}, Cues: cues})
	}
	return tracks, nil
}

func cuesFromEvents(events []asset.VisualEvent) []asset.TimedCue {
	out := make([]asset.TimedCue, len(events))
	for i, event := range events {
		out[i] = asset.TimedCue{StartMs: event.StartMs, EndMs: event.EndMs, Text: event.Text}
	}
	return out
}

// ShouldAcquireTranscript is the fail-closed decision gate used before the
// existing subtitle/Whisper chain. Unknown dialogue state is not treated as
// silence, so no transcript is invented for an unclassified asset.
func ShouldAcquireTranscript(hasDialogue *bool) bool { return hasDialogue != nil && *hasDialogue }
