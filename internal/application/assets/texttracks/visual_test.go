package texttracks

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestBuildVisualTracksPreservesSourceTimeline(t *testing.T) {
	tracks, err := BuildVisualTracks("ai-1", asset.VisualAnalysis{Summary: "A boxer trains.", Events: []asset.VisualEvent{{StartMs: 0, EndMs: 2200, Text: "The boxer wraps his hands."}}}, []VisualTrackInput{{Language: "it", Summary: "Un pugile si allena.", Events: []string{"Il pugile si fascia le mani."}}}, "gemma", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[1].Track.SourceType != asset.TextSourceTranslation || tracks[1].Cues[0].StartMs != 0 {
		t.Fatalf("tracks=%#v", tracks)
	}
	if !ShouldAcquireTranscript(nil) { /* unknown is fail closed */
	} else {
		t.Fatal("unknown dialogue must not acquire transcript")
	}
}
