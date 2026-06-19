// Package audio defines the canonical contract for TTS generation that
// the voiceover service consumes. The concrete implementation lives in
// internal/infrastructure/audio (PR-D.5.3 extraction); the data types
// AudioInput/AudioResult stay alongside it so the concrete Processor
// can reference them without crossing package boundaries.
//
// Architecture (per AGENTS.md split app/infrastructure):
//
//   - application/voiceover/Service holds a core.audio.Processor field
//     (interface, not concrete). Wiring is done in internal/app/.
//   - infrastructure/audio/processor.go owns the os/exec subprocess
//     invocation of bridges/tts_edge.py plus the post-TTS pipeline
//     (silence removal via ffmpeg, hash, Drive upload).
//   - core/audio/audio.go owns the contract: minimal signature that
//     hides the implementation choice (Python sidecar today, could be
//     a hosted TTS API tomorrow).
//
// Why type aliases rather than new declarations: the data shapes
// AudioInput/AudioResult already exist alongside the concrete impl
// in package audioasset. Re-declaring them in core/ would force the
// processor to import core/ (reversing the dependency direction).
// A Go type alias preserves identity: *audioasset.Processor satisfies
// audio.Processor without coercion.
package audio

import (
	"context"

	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
)

// AudioInput is the canonical input for TTS generation. Currently a
// Go type alias of audioasset.AudioInput (infrastructure/audio/types.go)
// so callers can refer to either path interchangeably. New callers
// MUST use the core/audio identifier for forward-compat.
type AudioInput = audioasset.AudioInput

// AudioResult is the canonical output of TTS generation. Same alias
// pattern as AudioInput.
type AudioResult = audioasset.AudioResult

// Processor is the canonical contract for TTS generation. The
// signature matches the concrete audioasset.Processor.Generate method
// (input/output via pointer wrappers, ctx for cancellation). New
// implementations (e.g. a hosted-TTS adapter) MUST satisfy this
// signature; the voiceover service depends on the interface, not on
// the concrete subprocess impl.
type Processor interface {
	Generate(ctx context.Context, input *AudioInput) (*AudioResult, error)
}
