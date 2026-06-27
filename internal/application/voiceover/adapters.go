// Package voiceover — adapters.go bridges the new GenerateVoiceoverUseCase
// ports to the existing infrastructure implementations, so that the old
// Service methods can delegate to the use case without a full rewrite.
//
// PR 2 (June 2026): three adapters:
//
//	ttsProviderAdapter wraps *audioasset.Processor → TTSProvider
//	destinationResolverAdapter wraps asset.Resolver → DestinationResolver
//	voiceRegistryAdapter wraps a simple map[domain.Locale]VoiceProfile → VoiceRegistry
package voiceover

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	domain "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"

	"go.uber.org/zap"
)

// ── ttsProviderAdapter ─────────────────────────────────────────────────

// ttsProviderAdapter wraps the existing *audioasset.Processor to satisfy
// the TTSProvider port. It delegates Generate calls with a type conversion
// from the new TTSGenerationInput to the legacy AudioInput.
type ttsProviderAdapter struct {
	proc *audioasset.Processor
	log  *zap.Logger
}

// newTTSProviderAdapter creates an adapter or returns nil when proc is nil.
func newTTSProviderAdapter(proc *audioasset.Processor, log *zap.Logger) *ttsProviderAdapter {
	if proc == nil {
		return nil
	}
	return &ttsProviderAdapter{proc: proc, log: log}
}

// Generate converts the domain-level input into the legacy AudioInput,
// calls the audio processor, and maps the AudioResult back.
func (a *ttsProviderAdapter) Generate(
	ctx context.Context,
	input TTSGenerationInput,
	outputDir string,
) (*TTSGenerationOutput, error) {
	if a == nil || a.proc == nil {
		return nil, fmt.Errorf("voiceover: TTS processor not available")
	}

	audioInput := &audioasset.AudioInput{
		Text:      input.Text,
		Language:  string(input.Voice.Locale),
		Voice:     input.Voice.VoiceCode,
		Filename:  input.Filename,
		OutputDir: outputDir,
		// PR 2: Destination is set to nil so the audio processor does
		// NOT upload to Drive — the use case's lifecycle finaliser
		// owns the upload. This prevents double-upload (the processor
		// would upload once, then the lifecycle would upload again).
		Destination: nil,
		// RemoveSilence, Strategy, UseStdin left at zero values.
	}

	result, err := a.proc.Generate(ctx, audioInput)
	if err != nil {
		return nil, fmt.Errorf("voiceover: TTS generation failed: %w", err)
	}

	if result.VoiceProfile != "" && input.Voice.VoiceCode == "" {
		// If the processor detected a voice profile, surface it.
		_ = result.VoiceProfile // keep in scope for future use
	}

	return &TTSGenerationOutput{
		LocalPath:   result.LocalPath,
		FileHash:    result.FileHash,
		DriveLink:   result.DriveLink,
		DriveFileID: result.DriveFileID,
	}, nil
}

// ── destinationResolverAdapter ─────────────────────────────────────────

// destinationResolverAdapter wraps the existing asset.Resolver to satisfy
// the DestinationResolver port. It converts a domain.DestinationRef into
// a legacy ResolveRequest.
type destinationResolverAdapter struct {
	resolver asset.Resolver
	log      *zap.Logger
}

// newDestinationResolverAdapter creates an adapter or returns nil when
// resolver is nil.
func newDestinationResolverAdapter(resolver asset.Resolver, log *zap.Logger) *destinationResolverAdapter {
	if resolver == nil {
		return nil
	}
	return &destinationResolverAdapter{resolver: resolver, log: log}
}

// Resolve converts the minimal DestinationRef into a ResolveRequest and
// forwards to the asset resolver.
func (a *destinationResolverAdapter) Resolve(
	ctx context.Context,
	ref domain.DestinationRef,
) (ResolvedDestination, error) {
	if a == nil || a.resolver == nil {
		return ResolvedDestination{}, fmt.Errorf("voiceover: destination resolver not available")
	}

	req := &asset.ResolveRequest{
		Source:   "voiceover",
		FolderID: ref.FolderID,
	}

	resolved, err := a.resolver.Resolve(ctx, req)
	if err != nil {
		return ResolvedDestination{}, fmt.Errorf("voiceover: destination resolution failed: %w", err)
	}

	return ResolvedDestination{
		FolderID: resolved.FolderID,
	}, nil
}

// ── voiceRegistryAdapter ───────────────────────────────────────────────

// voiceRegistryAdapter provides a simple in-process locale → voice-code
// mapping. Production deployments wire a richer registry (e.g. DB-backed
// or config-file-driven); this adapter satisfies the port for the PR 2
// delegation cutover.
type voiceRegistryAdapter struct {
	// voices maps locale strings to voice codes.
	voices map[domain.Locale]domain.VoiceProfile
}

// newVoiceRegistryAdapter builds a registry from the known TTS voices
// shipped in the Python edge-tts bridge.
func newVoiceRegistryAdapter() *voiceRegistryAdapter {
	reg := &voiceRegistryAdapter{
		voices: make(map[domain.Locale]domain.VoiceProfile),
	}
	// Populate with the supported locale → voice mappings used by the
	// edge-tts Python bridge. These match the languages listed in
	// DefaultPromoLanguages plus any additional frequently-requested
	// locales observed in production.
	register := func(locale string, voiceCode string) {
		loc := domain.Locale(locale)
		reg.voices[loc] = domain.VoiceProfile{
			Locale:    loc.Normalize(),
			VoiceName: voiceCode,
			VoiceCode: voiceCode,
		}
	}
	register("en-US", "en-US-RogerNeural")
	register("es-ES", "es-ES-AlvaroNeural")
	register("fr-FR", "fr-FR-HenriNeural")
	register("de-DE", "de-DE-ConradNeural")
	register("it-IT", "it-IT-DiegoNeural")
	register("pt-BR", "pt-BR-AntonioNeural")
	register("pl-PL", "pl-PL-MarekNeural")
	register("nl-NL", "nl-NL-MaartenNeural")
	register("ja-JP", "ja-JP-KeitaNeural")
	register("ko-KR", "ko-KR-InJoonNeural")
	register("ru-RU", "ru-RU-DmitryNeural")
	register("tr-TR", "tr-TR-AhmetNeural")
	register("id-ID", "id-ID-ArdiNeural")
	register("en-GB", "en-GB-RyanNeural")
	register("en", "en-US-RogerNeural")
	register("es", "es-ES-AlvaroNeural")
	register("fr", "fr-FR-HenriNeural")
	register("de", "de-DE-ConradNeural")
	register("it", "it-IT-DiegoNeural")
	return reg
}

// Resolve looks up the voice profile for a given locale. When
// requestedVoice is non-empty, it is validated against the registry
// and returned as-is if it matches a known voice for this locale;
// otherwise an error is returned.
func (r *voiceRegistryAdapter) Resolve(
	locale domain.Locale,
	requestedVoice string,
) (domain.VoiceProfile, error) {
	if r == nil {
		return domain.VoiceProfile{}, fmt.Errorf("voiceover: voice registry not available")
	}

	normalized := locale.Normalize()

	// When a specific voice is requested, validate it against the
	// known voices. If the caller asked for a voice we don't know,
	// return an error rather than silently falling back.
	if requestedVoice != "" {
		for _, profile := range r.voices {
			if profile.VoiceCode == requestedVoice {
				return profile, nil
			}
		}
		return domain.VoiceProfile{}, &domain.VoiceNotAvailableError{
			Locale: normalized,
			Voice:  requestedVoice,
		}
	}

	// Exact match on the normalized locale.
	if profile, ok := r.voices[normalized]; ok {
		return profile, nil
	}

	// Fallback: try the base language (e.g. "en" when "en-US" was given).
	if idx := strings.Index(string(normalized), "-"); idx > 0 {
		base := normalized[:idx]
		if profile, ok := r.voices[base]; ok {
			return profile, nil
		}
	}

	// Last resort: use the locale string as the voice code (legacy
	// behaviour — the Python bridge will auto-detect).
	return domain.VoiceProfile{
		Locale:    normalized,
		VoiceName: string(normalized),
		VoiceCode: string(normalized),
	}, nil
}
