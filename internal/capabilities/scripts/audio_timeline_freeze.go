package scriptgeneration

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

// freezeTailedVideoSegments returns the scene's visual segments, appending a
// synthetic freeze tail when the canonical scene duration outlasts the real
// clips (a narration longer than its clip). The tail holds the last clip's
// final frame for the remaining window, so the canonical timeline's video
// always covers the scene. This is the single owner of the freeze policy —
// never an implicit black gap guessed by the renderer.
func freezeTailedVideoSegments(scene ResolvedScene, durationUS int64) []audio.VideoSegment {
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
