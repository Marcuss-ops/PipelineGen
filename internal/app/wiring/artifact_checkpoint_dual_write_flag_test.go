package wiring

import (
	"testing"

	"github.com/stretchr/testify/require"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestArtifactCheckpointRolloutFlags keeps the dual-write, resume and cutover
// parsers on the same vocabulary: an operator typo must refuse boot for any
// one of them.

func TestArtifactCheckpointRolloutFlags(t *testing.T) {
	cases := []struct {
		value   string
		enabled bool
		wantErr bool
	}{
		{"", false, false},
		{"0", false, false},
		{"false", false, false},
		{"off", false, false},
		{"no", false, false},
		{"disabled", false, false},
		{"1", true, false},
		{"true", true, false},
		{"on", true, false},
		{"yes", true, false},
		{"localized_render", true, false},
		{" LOCALIZED_RENDER ", true, false},
		{"render", false, true},
		{"localized-render", false, true},
	}
	for _, env := range []string{
		scriptgen.ArtifactCheckpointDualWriteEnv,
		scriptgen.ArtifactCheckpointResumeEnv,
		scriptgen.ArtifactCheckpointCutoverEnv,
	} {
		env := env
		for _, tc := range cases {
			t.Run(env+"/"+tc.value, func(t *testing.T) {
				t.Setenv(env, tc.value)
				enabled, err := artifactCheckpointFlag(env)
				if tc.wantErr {
					require.Error(t, err)
					require.False(t, enabled)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tc.enabled, enabled)
			})
		}
	}
}
