// Package app — local_tts_router.go
//
// English-only routing for the local TTS pilot. The router is the ONLY
// place that decides which provider serves a language: the voiceover use
// case keeps seeing one canonical TTSProvider port, and every other
// language continues to use the certified Edge bridge.
//
// It also implements the optional ProviderIdentityFor port extension used
// by the cache fingerprint, so a Pocket/Phonon output can never reuse an
// Edge cache row (or vice versa) even when the voice string is identical.
package voiceover

import (
	"context"
	"strings"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
)

type languageRoutedTTS struct {
	local              voiceover.TTSProvider
	fallback           voiceover.TTSProvider
	languages          map[string]struct{}
	localVoice         string
	localProviderID    string
	fallbackProviderID string
}

func newLanguageRoutedTTS(
	local, fallback voiceover.TTSProvider,
	languages []string,
	localVoice, localProviderID, fallbackProviderID string,
) *languageRoutedTTS {
	if local == nil {
		panic("app.voiceover.newLanguageRoutedTTS: local provider is required")
	}
	if fallback == nil {
		panic("app.voiceover.newLanguageRoutedTTS: fallback provider is required")
	}
	routed := make(map[string]struct{}, len(languages))
	for _, language := range languages {
		if base := normalizeBaseLanguage(language); base != "" {
			routed[base] = struct{}{}
		}
	}
	if len(routed) == 0 {
		// Fail-closed pilot scope: English only.
		routed["en"] = struct{}{}
	}
	return &languageRoutedTTS{
		local:              local,
		fallback:           fallback,
		languages:          routed,
		localVoice:         strings.TrimSpace(localVoice),
		localProviderID:    strings.TrimSpace(localProviderID),
		fallbackProviderID: strings.TrimSpace(fallbackProviderID),
	}
}

func (r *languageRoutedTTS) Synthesize(ctx context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	if r.routes(in.Language) {
		if r.localVoice != "" {
			// The local engines have their own voice catalog (alba, ...).
			// The Edge voice id configured for the language is meaningless
			// here, so the pilot voice overrides it.
			in.Voice = r.localVoice
		}
		return r.local.Synthesize(ctx, in)
	}
	return r.fallback.Synthesize(ctx, in)
}

// ProviderIdentityFor is the optional port extension consumed by
// BuildVoiceoverContentFingerprintV3. It is deterministic for a given
// language and configuration, which is what the pre-synthesis cache lookup
// needs.
func (r *languageRoutedTTS) ProviderIdentityFor(language voiceover.Language) string {
	if r.routes(language) {
		return r.localProviderID
	}
	return r.fallbackProviderID
}

func (r *languageRoutedTTS) routes(language voiceover.Language) bool {
	_, ok := r.languages[normalizeBaseLanguage(string(language))]
	return ok
}

func normalizeBaseLanguage(language string) string {
	normalized := strings.ToLower(strings.TrimSpace(language))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	if idx := strings.IndexByte(normalized, '-'); idx >= 0 {
		normalized = normalized[:idx]
	}
	return normalized
}

var _ voiceover.TTSProvider = (*languageRoutedTTS)(nil)
