package config

import (
	"testing"
)

// A2 (October 2026): the per-language translation fan-out of
// asset.text.materialize is config-driven. Default = full expansion (10, one
// in-flight call per target language of the canonical 10-language fan-out),
// clamp 1..16, 1 = documented sequential rollback. applyEnvVars silently
// skips unparseable env values, so garbage keeps the default; here the
// clamp surface is FanoutConcurrency.
func TestMultilingualFanoutConcurrency(t *testing.T) {
	cases := []struct {
		name string
		raw  int
		want int
	}{
		{"zero value (field unset) = sequential rollback", 0, 1},
		{"negative = sequential rollback", -2, 1},
		{"explicit 1 = sequential rollback", 1, 1},
		{"explicit 3", 3, 3},
		{"default full expansion", 10, 10},
		{"99 clamps to max", 99, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MultilingualConfig{TextTracksFanout: tc.raw}.FanoutConcurrency()
			if got != tc.want {
				t.Fatalf("FanoutConcurrency(%d) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// TestMultilingualFanoutEnvOverride pins the env surface end-to-end through
// the canonical reflect loader: PIPELINEGEN_TEXTTRACKS_FANOUT must override
// the yaml/default value on MultilingualConfig.
func TestMultilingualFanoutEnvOverride(t *testing.T) {
	t.Setenv("PIPELINEGEN_TEXTTRACKS_FANOUT", "3")
	cfg := Config{}
	applyEnvVars(&cfg)
	if got := cfg.Media.Multilingual.TextTracksFanout; got != 3 {
		t.Fatalf("env override: TextTracksFanout = %d, want 3", got)
	}
	if got := cfg.Media.Multilingual.FanoutConcurrency(); got != 3 {
		t.Fatalf("clamped = %d, want 3", got)
	}
}
