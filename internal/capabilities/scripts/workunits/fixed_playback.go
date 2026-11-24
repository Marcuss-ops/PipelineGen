// fixed_playback.go — durata di playback di una sezione fissa.
package workunits

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// fixedPlaybackDurationUS returns the declared source span of a fixed section
// in microseconds, or 0 when the policy declares no usable window.
func fixedPlaybackDurationUS(playback scriptpkg.FixedPlaybackPolicy) int64 {
	if playback.SourceOutMS <= playback.SourceInMS || playback.SourceOutMS == 0 {
		return 0
	}
	return (playback.SourceOutMS - playback.SourceInMS) * 1000
}
