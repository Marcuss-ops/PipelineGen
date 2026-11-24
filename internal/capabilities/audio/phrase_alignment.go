package audio

import audiokernel "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"

// The phrase-alignment contract lives in the kernel: the rustexec adapter
// implements it without importing a capability, and the audio domain keeps the
// same names through these aliases.
type LocatedPhrase = audiokernel.LocatedPhrase
type PhraseAlignmentRequest = audiokernel.PhraseAlignmentRequest
type PhraseAligner = audiokernel.PhraseAligner

var ErrPhraseNotFound = audiokernel.ErrPhraseNotFound
