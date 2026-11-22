package voiceover

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// TestVoiceoverPostPolicyInvalidatesLegacyExplicitRateCache pins the
// 2026-11-22 rate-contract bump: before the processor fix, explicit rates
// (including the scriptgen default "-20%") were dropped on the wire, so
// cached audio was actually synthesized at 1.0x while its key claimed the
// requested rate. The token forces those entries to miss and resynthesize.
// Provider-default entries (empty rate) were never affected and keep their
// existing key.
func TestVoiceoverPostPolicyInvalidatesLegacyExplicitRateCache(t *testing.T) {
	explicit := VoiceoverPostPolicy{
		Rate:    "-20%",
		Silence: audio.DefaultSilenceRemovalPolicy(),
	}
	legacyKey := "rate=-20%;" + audio.DefaultSilenceRemovalPolicy().Normalized().Fingerprint()
	if explicit.Fingerprint() == legacyKey {
		t.Fatal("explicit-rate fingerprint must differ from the pre-fix key")
	}
	if !strings.Contains(explicit.Fingerprint(), "rate-contract=v2;") {
		t.Fatalf("explicit-rate fingerprint missing contract token: %q", explicit.Fingerprint())
	}

	providerDefault := VoiceoverPostPolicy{Silence: audio.DefaultSilenceRemovalPolicy()}
	if strings.Contains(providerDefault.Fingerprint(), "rate-contract=v2;") {
		t.Fatalf("provider-default fingerprint must keep its legacy key: %q", providerDefault.Fingerprint())
	}
}
