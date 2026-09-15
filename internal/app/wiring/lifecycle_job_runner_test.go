package wiring

import (
	"fmt"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
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

// TestClipRenderPhaseScopeMatchesCapabilityContract pins the pool split against
// the capability-owned wire contract. This scope is what routes a settle
// continuation to the dedicated pool AND keeps it out of the general pool: if
// it stopped matching the key/value cliprender actually writes, every settle job
// would silently fall back into the general pool — no error, no log, guardrail
// gone.
//
// The probe payload is built from the CAPABILITY's contract
// (cliprender.PayloadKeyRenderPhase + ParseRenderPhase) rather than restating
// the literals, so a reintroduced local literal fails here.
func TestClipRenderPhaseScopeMatchesCapabilityContract(t *testing.T) {
	match, exclude := clipRenderPhaseScope()
	if len(match) != 1 || len(exclude) != 1 {
		t.Fatalf("scope must name exactly one phase key: match=%v exclude=%v", match, exclude)
	}

	settlePayload := map[string]any{cliprender.PayloadKeyRenderPhase: string(cliprender.RenderPhaseSettle)}
	phase, err := cliprender.ParseRenderPhase(settlePayload)
	if err != nil {
		t.Fatalf("capability must parse its own settle payload: %v", err)
	}
	if phase != cliprender.RenderPhaseSettle {
		t.Fatalf("ParseRenderPhase = %q, want %q", phase, cliprender.RenderPhaseSettle)
	}
	for _, scope := range []map[string]string{match, exclude} {
		for k, v := range scope {
			raw, ok := settlePayload[k]
			if !ok {
				t.Fatalf("scope key %q is not the key the capability writes (%v)", k, settlePayload)
			}
			if fmt.Sprintf("%v", raw) != v {
				t.Fatalf("scope value %q for key %q does not match the capability's %v", v, k, raw)
			}
		}
	}

	// The submit phase carries NO render_phase (absent key = submit), so the
	// exclusion must not key on anything a submit payload carries — otherwise the
	// general pool would refuse the jobs it is the only owner of.
	submitPayload := map[string]any{}
	if _, err := cliprender.ParseRenderPhase(submitPayload); err != nil {
		t.Fatalf("an absent phase must be the submit phase: %v", err)
	}
	for k := range exclude {
		if _, ok := submitPayload[k]; ok {
			t.Fatalf("the exclusion must not key on anything a submit payload carries (%q)", k)
		}
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
