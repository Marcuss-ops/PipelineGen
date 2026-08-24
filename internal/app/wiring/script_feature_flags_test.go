package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestAnyScriptFeatureEnabled(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{name: "nil cfg", cfg: nil, want: false},
		{name: "images only", cfg: &config.Config{Features: config.FeaturesConfig{ImagesEnabled: true}}, want: true},
		{name: "clips only", cfg: &config.Config{Features: config.FeaturesConfig{ScriptClipsEnabled: true}}, want: true},
		{name: "none", cfg: &config.Config{Features: config.FeaturesConfig{}}, want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := anyScriptFeatureEnabled(tc.cfg); got != tc.want {
				t.Fatalf("anyScriptFeatureEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScriptGenerationEnabledUsesCapabilityFlag(t *testing.T) {
	t.Parallel()

	if scriptGenerationEnabled(nil) {
		t.Fatal("nil config must disable script generation")
	}
	if scriptGenerationEnabled(&config.Config{Scripts: config.ScriptsConfig{Capability: config.ScriptCapabilityConfig{Enabled: false}}, Features: config.FeaturesConfig{ImagesEnabled: true}}) {
		t.Fatal("optional image feature must not enable script generation")
	}
	if !scriptGenerationEnabled(&config.Config{Scripts: config.ScriptsConfig{Capability: config.ScriptCapabilityConfig{Enabled: true}}}) {
		t.Fatal("script capability must enable script generation independently")
	}
}
