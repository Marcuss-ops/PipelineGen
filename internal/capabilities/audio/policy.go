package audio

import "fmt"

// SilenceRemovalPolicy is the versioned post-processing contract for the
// voiceover audio pipeline. The executor (BrainRust remove_silence) must apply
// exactly these values; the voiceover cache fingerprint includes the resolved
// policy so a request produced under a different cut policy can never reuse
// this audio.
//
// The defaults are the historical executor values (-40 dB detector,
// 120 ms minimum silence, canonical 48 kHz stereo output). Making them a
// value instead of a literal in the fingerprint is what lets a caller change
// the policy without silently colliding with cached audio from the old one.
type SilenceRemovalPolicy struct {
	// ThresholdDB is the silencedetect / silenceremove threshold.
	ThresholdDB int
	// MinSilenceMS is the minimum pause retained/removed boundary.
	MinSilenceMS int
	// OutputSampleRate and OutputChannels describe the canonical cleaned mix.
	OutputSampleRate int
	OutputChannels   int
}

// DefaultSilenceRemovalPolicy returns the executor's canonical policy.
func DefaultSilenceRemovalPolicy() SilenceRemovalPolicy {
	return SilenceRemovalPolicy{
		ThresholdDB:      -40,
		MinSilenceMS:     120,
		OutputSampleRate: 48000,
		OutputChannels:   2,
	}
}

// Normalized fills zero fields with canonical defaults and rejects impossible
// values by mapping them back to the default. The fingerprint is a cache
// identity, so it must be deterministic for every caller, including one that
// only sets a single field.
func (p SilenceRemovalPolicy) Normalized() SilenceRemovalPolicy {
	defaults := DefaultSilenceRemovalPolicy()
	if p.ThresholdDB == 0 {
		p.ThresholdDB = defaults.ThresholdDB
	}
	if p.MinSilenceMS <= 0 {
		p.MinSilenceMS = defaults.MinSilenceMS
	}
	if p.OutputSampleRate <= 0 {
		p.OutputSampleRate = defaults.OutputSampleRate
	}
	if p.OutputChannels <= 0 {
		p.OutputChannels = defaults.OutputChannels
	}
	return p
}

// Fingerprint is the stable token folded into the voiceover content and
// idempotency keys.
func (p SilenceRemovalPolicy) Fingerprint() string {
	p = p.Normalized()
	return fmt.Sprintf("threshold=%ddB/min=%dms/out=%dHz/%dch",
		p.ThresholdDB, p.MinSilenceMS, p.OutputSampleRate, p.OutputChannels)
}
