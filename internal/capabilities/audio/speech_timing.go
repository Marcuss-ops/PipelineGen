package audio

import (
	audiokernel "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
)

// SpeechTimingVersion is the schema version for SpeechTimingArtifact files.
// Bump it whenever the canonical timing JSON shape or semantics change so
// cached artifacts cannot be silently misread.
const SpeechTimingVersion = 1

// BoundaryMode risiede nel kernel (vedi internal/kernel/audio/timing.go).

// SpeechWordTiming and SpeechTimingArtifact are owned by the neutral kernel
// package so the media adapters can speak the timing contract without
// importing a capability.
type BoundaryMode = audiokernel.BoundaryMode

type TimingMode = audiokernel.TimingMode
type TimingFormat = audiokernel.TimingFormat
type TimingRequest = audiokernel.TimingRequest

const (
	BoundaryWord     = audiokernel.BoundaryWord
	TimingDisabled   = audiokernel.TimingDisabled
	TimingBestEffort = audiokernel.TimingBestEffort
	TimingRequired   = audiokernel.TimingRequired
	TimingJSON       = audiokernel.TimingJSON
	TimingSRT        = audiokernel.TimingSRT
	TimingVTT        = audiokernel.TimingVTT
)

func DefaultTimingRequest() TimingRequest { return audiokernel.DefaultTimingRequest() }

type SpeechWordTiming = audiokernel.SpeechWordTiming
type SpeechTimingArtifact = audiokernel.SpeechTimingArtifact

var (
	ErrInvalidTimingMode       = audiokernel.ErrInvalidTimingMode
	ErrInvalidTimingFormat     = audiokernel.ErrInvalidTimingFormat
	ErrInvalidTimingVersion    = audiokernel.ErrInvalidTimingVersion
	ErrUnsupportedBoundaryMode = audiokernel.ErrUnsupportedBoundaryMode
	ErrInvalidWordIndex        = audiokernel.ErrInvalidWordIndex
	ErrNegativeTiming          = audiokernel.ErrNegativeTiming
	ErrInvalidTimingRange      = audiokernel.ErrInvalidTimingRange
	ErrNonMonotonicTiming      = audiokernel.ErrNonMonotonicTiming
	ErrTimingPastAudioDuration = audiokernel.ErrTimingPastAudioDuration
)

// AudioFileHasher is the narrow port for computing the SHA-256 digest of the
// final audio file. Concrete adapters live in the platform layer; the
// application layer must never open files directly (I/O binder gate).
type AudioFileHasher interface {
	SHA256File(path string) (string, error)
}
