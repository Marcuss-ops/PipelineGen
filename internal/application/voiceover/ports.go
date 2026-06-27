// Package voiceover — ports.go defines the three canonical structural ports
// consumed by GenerateVoiceoverUseCase. Each port is a narrow interface that
// keeps the use case independent of infrastructure implementations.
//
// PR 2 (June 2026): introduces VoiceRegistry, DestinationResolver, and
// TTSProvider as the single-source-of-truth contracts. Replaces the legacy
// pattern of the Service struct reaching directly into *audioasset.Processor,
// asset.Resolver, and cfg fields.
package voiceover

import (
	"context"

	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
)

// ── VoiceRegistry ──────────────────────────────────────────────────────

// VoiceRegistry resolves a voice profile from a locale and optional
// requested-voice hint.
//
// When requestedVoice is empty, the registry picks the default voice
// for the locale (e.g. "en-US" → "en-US-RogerNeural").
// When requestedVoice is non-empty, the registry validates it against
// the locale and returns the matching profile or an error.
type VoiceRegistry interface {
	Resolve(locale domain.Locale, requestedVoice string) (domain.VoiceProfile, error)
}

// ── DestinationResolver ────────────────────────────────────────────────

// DestinationResolver resolves a DestinationRef to a concrete folder ID.
// The resolver owns the default-folder fallback (e.g. from config), the
// voiceover_group → folder_id mapping, and any path validation.
type DestinationResolver interface {
	Resolve(ctx context.Context, ref domain.DestinationRef) (ResolvedDestination, error)
}

// Note: ResolvedDestination is defined in types.go alongside the legacy
// BatchRequest/BatchResponse types. It carries Group, FolderID, FolderPath,
// and DriveLink — more fields than the pure use case needs, but kept for
// backward compatibility with the old code path.
// The DestinationResolver port returns this same type.

// ── TTSProvider ────────────────────────────────────────────────────────

// TTSGenerationInput carries the resolved inputs for a TTS call.
type TTSGenerationInput struct {
	Text     string
	Voice    domain.VoiceProfile
	Filename string
}

// TTSGenerationOutput is the result of a TTS generation call.
type TTSGenerationOutput struct {
	LocalPath  string
	FileHash   string
	DriveLink  string
	DriveFileID string
}

// TTSProvider generates the audio file on disk and optionally uploads
// it to Drive. The provider owns the Edge-TTS invocation, file I/O,
// and upload logic. The use case supplies a fully-resolved command;
// the provider does not interpret locale or voice logic.
type TTSProvider interface {
	Generate(ctx context.Context, input TTSGenerationInput, outputDir string) (*TTSGenerationOutput, error)
}
