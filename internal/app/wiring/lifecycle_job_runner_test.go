package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TestSettleWorkerBudget pins the guardrail switch: the dedicated clip.render
// settle budget exists only when the clip.render feature is on AND the budget is
// positive. Either being off is the pre-guardrail single-pool behaviour, so an
// operator can roll the split back without a code change.
func TestSettleWorkerBudget(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want int
	}{
		{"nil config", nil, 0},
		{"feature off, budget set", &config.Config{Jobs: config.JobsConfig{ClipRenderSettleWorkers: 16}}, 0},
		{"feature on, budget zero", &config.Config{Features: config.FeaturesConfig{ClipRenderEnabled: true}}, 0},
		{"feature on, budget negative", &config.Config{Features: config.FeaturesConfig{ClipRenderEnabled: true}, Jobs: config.JobsConfig{ClipRenderSettleWorkers: -1}}, 0},
		{"feature on, budget positive", &config.Config{Features: config.FeaturesConfig{ClipRenderEnabled: true}, Jobs: config.JobsConfig{ClipRenderSettleWorkers: 16}}, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := settleWorkerBudget(tc.cfg); got != tc.want {
				t.Fatalf("settleWorkerBudget = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestJobRunnerBuildersFailClosedWithoutRoot pins that both pools refuse to
// build without the canonical root wiring instead of returning a runner that
// would poll an unowned queue.
func TestJobRunnerBuildersFailClosedWithoutRoot(t *testing.T) {
	deps := jobRunnerDeps{root: nil, cfg: &config.Config{}, log: nil}
	if got := buildJobRunner(deps); got != nil {
		t.Fatalf("buildJobRunner without root = %v, want nil", got)
	}
	if got := buildClipRenderSettleRunner(deps); got != nil {
		t.Fatalf("buildClipRenderSettleRunner without root = %v, want nil", got)
	}
}
