package usecase

import (
	"context"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

func TestClipSourceBuilder_ChronologicalOrdering_ReordersByStartMs(t *testing.T) {
	t.Parallel()

	resolver := newFakeClipResolver()
	clipA := makeTestClip("clip-a", "Clip A", 1)
	clipA.SetMetadataInt("start_ms", 2000)
	clipA.SetMetadataInt("end_ms", 3000)
	clipB := makeTestClip("clip-b", "Clip B", 1)
	clipB.SetMetadataInt("start_ms", 1000)
	clipB.SetMetadataInt("end_ms", 2000)
	clipC := makeTestClip("clip-c", "Clip C", 1)
	clipC.SetMetadataInt("start_ms", 3000)
	clipC.SetMetadataInt("end_ms", 4000)

	resolver.AddClip(clipA)
	resolver.AddClip(clipB)
	resolver.AddClip(clipC)

	builder := NewClipSourceBuilder(resolver, nil, zap.NewNop())
	builder.ConfigureTextTrackReader(&stubTextTrackReader{
		tracks: map[string]*detail.TextTrack{
			"clip-a:en": makeTrack("clip-a", "en", "transcript clip-a"),
			"clip-b:en": makeTrack("clip-b", "en", "transcript clip-b"),
			"clip-c:en": makeTrack("clip-c", "en", "transcript clip-c"),
		},
	})

	ev, _, sourceText, err := builder.BuildClipContext(context.Background(), []string{"clip-c", "clip-a", "clip-b"}, &ClipGenerationOptions{
		Language:         "en",
		OrderingStrategy: "chronological",
	})
	if err != nil {
		t.Fatalf("BuildClipContext returned error: %v", err)
	}
	want := []string{"clip-b", "clip-a", "clip-c"}
	if !slicesEqual(ev.AcceptedClipIDs, want) {
		t.Fatalf("AcceptedClipIDs = %v, want %v", ev.AcceptedClipIDs, want)
	}
	if idx := strings.Index(sourceText, "CLIP clip-b:"); idx < 0 {
		t.Fatalf("sourceText missing clip-b: %q", sourceText)
	} else {
		idxA := strings.Index(sourceText, "CLIP clip-a:")
		idxC := strings.Index(sourceText, "CLIP clip-c:")
		if !(idx < idxA && idxA < idxC) {
			t.Fatalf("sourceText order wrong: clip-b=%d clip-a=%d clip-c=%d", idx, idxA, idxC)
		}
	}
}
