package cliprender

import (
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestStageClipRenderMatchesJobType pins the one phase whose name is shared
// with a job type: the render boundary stage IS clip.render. The two facts
// live in different owners (observability registry + kernel/job job-type
// strings) so the equality is asserted instead of assumed — a rename on
// either side would otherwise surface as a report that joins nothing.
func TestStageClipRenderMatchesJobType(t *testing.T) {
	if string(StageClipRender) != job.TypeClipRender {
		t.Fatalf("StageClipRender = %q, want the clip.render job type %q",
			StageClipRender, job.TypeClipRender)
	}
}

// TestStageAliasesAreTheRegistryConstants pins that this package aliases the
// registry instead of re-declaring literals: the values must be the registry
// constants themselves.
func TestStageAliasesAreTheRegistryConstants(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  kernobs.StageName
		want kernobs.StageName
	}{
		{"prepare", StageClipPrepare, kernobs.StageClipPrepare},
		{"subtitles", StageClipSubtitles, kernobs.StageClipSubtitles},
		{"probe", StageClipProbe, kernobs.StageClipProbe},
		{"publish", StageClipPublish, kernobs.StageClipPublish},
		{"destination_resolve", StageClipDestinationResolve, kernobs.StageClipDestinationResolve},
		// StageClipRender is intentionally absent from the registry: its literal
		// is the job type, pinned by TestStageClipRenderMatchesJobType.
	} {
		if tc.got != tc.want {
			t.Errorf("%s: stage = %q, want registry constant %q", tc.name, tc.got, tc.want)
		}
	}
}
