package voiceover

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
)

type stubRoutedTTS struct {
	provider string
	calls    int
	voices   []string
}

func (s *stubRoutedTTS) Synthesize(_ context.Context, in voiceover.TTSInput) (voiceover.TTSOutput, error) {
	s.calls++
	s.voices = append(s.voices, in.Voice)
	return voiceover.TTSOutput{Provider: s.provider}, nil
}

func newTestRouter() (*languageRoutedTTS, *stubRoutedTTS, *stubRoutedTTS) {
	local := &stubRoutedTTS{provider: "local-tts:pocket"}
	fallback := &stubRoutedTTS{provider: "edge-tts"}
	router := newLanguageRoutedTTS(
		local, fallback,
		[]string{"en"},
		"alba",
		"local-tts:pocket",
		"edge-tts",
	)
	return router, local, fallback
}

func TestLanguageRoutedTTS_EnglishUsesLocalAndOverridesVoice(t *testing.T) {
	router, local, fallback := newTestRouter()

	out, err := router.Synthesize(context.Background(), voiceover.TTSInput{
		Text:     "hello",
		Language: "en",
		Voice:    "en-US-ChristopherNeural",
	})
	require.NoError(t, err)
	require.Equal(t, "local-tts:pocket", out.Provider)
	require.Equal(t, 1, local.calls)
	require.Equal(t, 0, fallback.calls)
	require.Equal(t, []string{"alba"}, local.voices)
}

func TestLanguageRoutedTTS_OtherLanguagesStayOnEdge(t *testing.T) {
	router, local, fallback := newTestRouter()

	for _, language := range []voiceover.Language{"it", "fr-FR", "pl", "ru", "tr", "id"} {
		out, err := router.Synthesize(context.Background(), voiceover.TTSInput{
			Language: language,
			Voice:    "edge-voice",
		})
		require.NoError(t, err)
		require.Equal(t, "edge-tts", out.Provider)
	}
	require.Equal(t, 0, local.calls)
	require.Equal(t, 6, fallback.calls)
}

func TestLanguageRoutedTTS_ProviderIdentityForFingerprint(t *testing.T) {
	router, _, _ := newTestRouter()

	require.Equal(t, "local-tts:pocket", router.ProviderIdentityFor("en"))
	require.Equal(t, "local-tts:pocket", router.ProviderIdentityFor("en-US"))
	require.Equal(t, "edge-tts", router.ProviderIdentityFor("it"))
	require.Equal(t, "edge-tts", router.ProviderIdentityFor("fr-FR"))
}

func TestLanguageRoutedTTS_DefaultsToEnglishOnly(t *testing.T) {
	local := &stubRoutedTTS{provider: "local"}
	fallback := &stubRoutedTTS{provider: "edge"}
	router := newLanguageRoutedTTS(local, fallback, nil, "", "local", "edge")

	require.True(t, router.routes("en"))
	require.False(t, router.routes("it"))
}
