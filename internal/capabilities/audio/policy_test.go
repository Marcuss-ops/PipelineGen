package audio

import "testing"

// The silence-removal policy is part of the cache identity: changing the
// detector threshold, minimum silence, or canonical output mix must produce a
// different fingerprint.
func TestSilenceRemovalPolicyFingerprintSeparatesPolicies(t *testing.T) {
	base := DefaultSilenceRemovalPolicy()
	if got, want := base.Fingerprint(), "threshold=-40dB/min=120ms/out=48000Hz/2ch"; got != want {
		t.Fatalf("default fingerprint = %q, want %q", got, want)
	}
	differentThreshold := base
	differentThreshold.ThresholdDB = -30
	differentMin := base
	differentMin.MinSilenceMS = 200
	differentMix := base
	differentMix.OutputSampleRate = 44100
	if base.Fingerprint() == differentThreshold.Fingerprint() ||
		base.Fingerprint() == differentMin.Fingerprint() ||
		base.Fingerprint() == differentMix.Fingerprint() {
		t.Fatal("every silence policy field must change the fingerprint")
	}
	if got := (SilenceRemovalPolicy{}).Fingerprint(); got != base.Fingerprint() {
		t.Fatalf("zero policy must normalize to the canonical defaults, got %q want %q", got, base.Fingerprint())
	}
}
