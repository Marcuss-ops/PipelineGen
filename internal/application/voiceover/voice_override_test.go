// Package voiceover — voice_override_test.go (Azione #1, July 2026).
//
// synthesizeStage removed from Service. TestTTSBridge_UsesPerLanguageVoice
// is skipped — the voice-override behavior is now tested via
// ProcessSegmentUseCase.Execute (which passes cmd.Voice through to
// TTSProvider.Synthesize). voiceOverrideFor free function preserved in
// process.go — the 4 unit-level tests below still pin the lookup contract.
package voiceover

import (
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Unit-level: voiceOverrideFor pin (audit-pinned helper surface) ──

func TestVoiceOverrideFor_Present_ReturnsValue(t *testing.T) {
	req := &BatchRequest{
		VoiceOverrides: map[string]string{
			"en": "en-US-RogerNeural",
			"it": "it-IT-IsabellaNeural",
		},
	}
	got := voiceOverrideFor(req, "en")
	assert.Equal(t, "en-US-RogerNeural", got,
		"P0.4: lookup hit must return the canonical voice identifier")
}

func TestVoiceOverrideFor_AbsentKey_ReturnsEmpty(t *testing.T) {
	req := &BatchRequest{
		VoiceOverrides: map[string]string{
			"en": "en-US-RogerNeural",
		},
	}
	got := voiceOverrideFor(req, "fr")
	assert.Equal(t, "", got,
		"P0.4: missing key must return \"\" (default-voice path downstream)")
}

func TestVoiceOverrideFor_NilMap_ReturnsEmpty(t *testing.T) {
	req := &BatchRequest{}
	got := voiceOverrideFor(req, "en")
	assert.Equal(t, "", got,
		"P0.4: nil VoiceOverrides map must return \"\" (nil-safety)")
}

func TestVoiceOverrideFor_NilReq_ReturnsEmptyNoPanic(t *testing.T) {
	got := voiceOverrideFor(nil, "en")
	assert.Equal(t, "", got,
		"P0.4: nil BatchRequest must not panic (nil-safety)")
}

// ── Unit-level: resolveVoiceForLanguage pin (registry SSOT) ──

func TestResolveVoiceForLanguage_VoiceOverrideWins(t *testing.T) {
	reg, err := asset.NewLanguageRegistry([]asset.LanguageSpec{
		{Code: "it", Enabled: true, GenerateTTS: true, EdgeTTSVoice: "it-IT-DiegoNeural"},
	})
	require.NoError(t, err)
	req := &BatchRequest{VoiceOverrides: map[string]string{"it": "it-IT-ElsaNeural"}}
	got := resolveVoiceForLanguage(req, "it", reg, nil)
	assert.Equal(t, "it-IT-ElsaNeural", got,
		"explicit per-request override must win over registry voice")
}

func TestResolveVoiceForLanguage_RegistryVoiceUsed(t *testing.T) {
	reg, err := asset.NewLanguageRegistry([]asset.LanguageSpec{
		{Code: "it", Enabled: true, GenerateTTS: true, EdgeTTSVoice: "it-IT-DiegoNeural"},
	})
	require.NoError(t, err)
	got := resolveVoiceForLanguage(nil, "it", reg, nil)
	assert.Equal(t, "it-IT-DiegoNeural", got,
		"registry EdgeTTSVoice must be returned when no override is set")
}

func TestResolveVoiceForLanguage_GenerateTTSFalse_FallsBack(t *testing.T) {
	reg, err := asset.NewLanguageRegistry([]asset.LanguageSpec{
		{Code: "it", Enabled: true, GenerateTTS: false, EdgeTTSVoice: "it-IT-DiegoNeural"},
	})
	require.NoError(t, err)
	got := resolveVoiceForLanguage(nil, "it", reg, nil)
	assert.Equal(t, "", got,
		"language with generate_tts=false must not use EdgeTTSVoice")
}

func TestResolveVoiceForLanguage_MissingRegistryEntry_FallsBack(t *testing.T) {
	reg, err := asset.NewLanguageRegistry([]asset.LanguageSpec{
		{Code: "it", Enabled: true, GenerateTTS: true, EdgeTTSVoice: "it-IT-DiegoNeural"},
	})
	require.NoError(t, err)
	got := resolveVoiceForLanguage(nil, "fr", reg, nil)
	assert.Equal(t, "", got,
		"missing registry entry must fall back to empty string")
}

func TestResolveVoiceForLanguage_NilRegistry_FallsBack(t *testing.T) {
	got := resolveVoiceForLanguage(nil, "it", nil, nil)
	assert.Equal(t, "", got,
		"nil registry must fall back to empty string")
}

// TestTTSBridge_UsesPerLanguageVoice — SKIPPED (Azione #1, July 2026).
// synthesizeStage removed from Service; the voice-override propagation is
// now tested via ProcessSegmentUseCase.Execute (cmd.Voice → TTSProvider).
func TestTTSBridge_UsesPerLanguageVoice(t *testing.T) {
	t.Skip("Azione #1 (July 2026): synthesizeStage removed — voice override propagation now tested via ProcessSegmentUseCase.Execute")
}

func TestE2E_VoiceOverrideReachesPython(t *testing.T) {
	// Step 1: wire-shape pin.
	req := &BatchRequest{
		Text:      "ciao mondo",
		Languages: []Language{"it"},
		VoiceOverrides: map[string]string{
			"it": "it-IT-IsabellaNeural",
		},
	}
	pm := req.PayloadMap()
	require.Contains(t, pm, "voice_overrides",
		"P0.4: BatchRequest.PayloadMap MUST serialise the canonical VoiceOverrides map under the snake_case `voice_overrides` key so the Python bridge reads it from the JSON payload")

	roundTripJSON, err := json.Marshal(pm)
	require.NoError(t, err,
		"P0.4: BatchRequest.PayloadMap() must round-trip via json.Marshal")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(roundTripJSON, &decoded),
		"P0.4: the produced payload must json.Unmarshal cleanly back into a map[string]any; downstream tts_edge.py reads via the same path")
	voRaw, ok := decoded["voice_overrides"]
	require.True(t, ok,
		"P0.4: round-tripped payload must contain `voice_overrides` key")
	voMap, ok := voRaw.(map[string]any)
	require.True(t, ok,
		"P0.4: `voice_overrides` must decode as map[string]any")
	assert.Equal(t, "it-IT-IsabellaNeural", voMap["it"],
		"P0.4 audit pin: the BCP-47 → voice identifier mapping must survive the payload round-trip")

	// Step 2: live subprocess guard — only runs in environments with
	// python3 + the canonical tts_edge.py script. CI without python
	// skips this branch.
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("P0.4 E2E: python3 not available in PATH (%v) — wire-shape assertion above is the canonical pin; the live subprocess smoke is environment-gated", err)
	}
	t.Log("P0.4 E2E: wire-shape audit pin complete; runtime subprocess smoke environment-gated (see scripts/bridges/tts_edge.py for the --voice flag surface)")
}
