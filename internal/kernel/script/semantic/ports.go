package semantic

import (
	"context"

	audiokernel "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// PhraseAligner is owned by the kernel audio package; the semantic engine
// adapter is its production implementation.
type PhraseAligner = audiokernel.PhraseAligner

// CandidateScorer annotates discovered image candidates with a semantic
// verdict. Implementations belong to infrastructure (the Ollama vision
// adapter); returning an error is treated as "scoring unavailable" by the
// discovery lane.
type CandidateScorer interface {
	ScoreImageCandidates(ctx context.Context, query string, candidates []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error)
}

// NERBackend is the shared extraction contract for interchangeable named-
// entity backends. Implementations return source-grounded UTF-8 byte spans;
// the scene enricher owns the single validation/normalization/deduplication
// path regardless of which backend is selected.
type NERBackend interface {
	Extract(ctx context.Context, language string, sourceText string, entityCount int) ([]VisualEntity, error)
}

// VisualNERPort is retained as the domain-specific name used by the scene
// pipeline; it is the same contract as NERBackend, not a second interface.
type VisualNERPort = NERBackend

// LocalizedSourceEntity is one SOURCE annotation identity offered to the Rust
// localized-identity matcher. Kind is already normalized to the annotation
// vocabulary (PERSON, LOCATION, ORGANIZATION, ...).
type LocalizedSourceEntity struct {
	Kind          string
	CanonicalName string
	Text          string
}

// LocalizedIdentityMatch is one SOURCE identity grounded in translated text.
// The span is in rune coordinates and Surface is the verbatim translated
// surface; the canonical id remains on the source identity, never here.
type LocalizedIdentityMatch struct {
	SourceIndex int
	Kind        string
	StartRune   int
	EndRune     int
	Surface     string
}

// LocalizedIdentityMatcher is the Rust boundary for the localization seam.
type LocalizedIdentityMatcher interface {
	MatchLocalizedIdentities(ctx context.Context, language, text string, sources []LocalizedSourceEntity) ([]LocalizedIdentityMatch, error)
	CompleteLocalizedIdentities(ctx context.Context, language, text string, sources []LocalizedSourceEntity, visual []string, existing []LocalizedIdentityMatch) ([]LocalizedIdentityMatch, error)
}
