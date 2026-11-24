// Package timeline owns the canonical scene→timeline projection: the pure
// resolution of scenes into ResolvedScene plus the freeze-tail policy that
// keeps the canonical video track covering every scene.
//
// It is a leaf: it imports only contract and the audio value package, never
// the scripts root, so both the root and its subpackages can depend on it.
package timeline

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// FreezeTailedVideoSegments returns the scene's visual segments, appending a
// synthetic freeze tail when the canonical scene duration outlasts the real
// clips (a narration longer than its clip). The tail holds the last clip's
// final frame for the remaining window, so the canonical timeline's video
// always covers the scene. This is the single owner of the freeze policy —
// never an implicit black gap guessed by the renderer.
func FreezeTailedVideoSegments(scene contract.ResolvedScene, durationUS int64) []audio.VideoSegment {
	videos := scene.VideoSegments
	if len(videos) == 0 && scene.Video.AssetID != "" {
		videos = []audio.VideoSegment{scene.Video}
	}
	if len(videos) == 0 {
		return nil
	}
	var visualEnd int64
	for _, v := range videos {
		visualEnd += v.TimelineDurationUS
	}
	if visualEnd >= durationUS {
		return videos
	}
	last := videos[len(videos)-1]
	return append(append([]audio.VideoSegment(nil), videos...), audio.VideoSegment{
		AssetID:            last.AssetID,
		SourceInUS:         last.SourceInUS + last.SourceDurationUS, // exclusive source end
		TimelineOffsetUS:   visualEnd,
		TimelineDurationUS: durationUS - visualEnd,
		Freeze:             true,
	})
}
