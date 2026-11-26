package gencore

import (
	"errors"
	"math"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

func budgetWords(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSpace(strings.Repeat("parola ", n))
}

// TestCheckWordBudgetPinsFiveSceneContract freezes the R21 regression: a
// 500-word scene asked for 3.015 words (503% over budget) passed no gate, and
// the run shipped with a 3.015-word scene. Five representative scenes pin the
// pass/fail boundary so the contract cannot silently widen again.
func TestCheckWordBudgetPinsFiveSceneContract(t *testing.T) {
	cases := []struct {
		name      string
		words     int
		target    int
		wantPass  bool
		wantDev   float64
		tolerance float64
	}{
		{"on target", 500, 500, true, 0, 0.01},
		{"within overage", 503, 500, true, 0.6, 0.01},
		{"R21 runaway", 3015, 500, false, 503, 0.01},
		{"lower boundary", 375, 500, true, -25, 0.01},
		{"below lower boundary", 374, 500, false, -25.2, 0.01},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := CheckWordBudget(budgetWords(tc.words), tc.target)
			if report.ActualWords != tc.words {
				t.Fatalf("ActualWords = %d, want %d", report.ActualWords, tc.words)
			}
			if report.Pass != tc.wantPass {
				t.Fatalf("Pass = %t, want %t (deviation %.2f%%)", report.Pass, tc.wantPass, report.DeviationPercent)
			}
			if math.Abs(report.DeviationPercent-tc.wantDev) > tc.tolerance {
				t.Fatalf("DeviationPercent = %.2f, want %.2f", report.DeviationPercent, tc.wantDev)
			}
		})
	}
}

// fiveScenePlan builds the canonical 5 × 700 plan from the live r4/r5
// regression: the script budget is 3.500 words, not the 700 of one scene.
func fiveScenePlan() *scriptpkg.ResolvedGenerationPlan {
	segments := make([]scriptpkg.ScriptSegment, 5)
	for i := range segments {
		segments[i] = scriptpkg.ScriptSegment{Topic: "scene", TargetWords: 700}
	}
	return &scriptpkg.ResolvedGenerationPlan{TargetWords: 3500, Segments: segments}
}

func fiveSceneText(words ...int) string {
	parts := make([]string, len(words))
	for i, n := range words {
		parts[i] = budgetWords(n)
	}
	return strings.Join(parts, "\n\n")
}

// TestCheckScriptWordBudgetSumsFiveSceneTargets freezes CODEX-02: the live
// r4 total (4.361 / 3.500 = +24,6%) is inside the aggregate window and must
// pass, while r5 (4.394 / 3.500 = +25,5%) is outside and must fail. Before
// the fix both were compared with a single 700-word scene (+523%).
func TestCheckScriptWordBudgetSumsFiveSceneTargets(t *testing.T) {
	plan := fiveScenePlan()

	r4 := CheckScriptWordBudget(fiveSceneText(872, 872, 872, 872, 873), plan)
	if r4.Aggregate.TargetWords != 3500 {
		t.Fatalf("r4 aggregate target = %d, want 3500 (sum of 5×700)", r4.Aggregate.TargetWords)
	}
	if r4.Aggregate.ActualWords != 4361 {
		t.Fatalf("r4 aggregate actual = %d, want 4361", r4.Aggregate.ActualWords)
	}
	if math.Abs(r4.Aggregate.DeviationPercent-24.6) > 0.05 {
		t.Fatalf("r4 deviation = %.2f%%, want +24.6%% (not the historical +523%%)", r4.Aggregate.DeviationPercent)
	}
	if !r4.Pass {
		t.Fatalf("r4 must pass the aggregate ±25%% gate: %+v", r4.Aggregate)
	}
	if !r4.SceneSplit || len(r4.Scenes) != 5 {
		t.Fatalf("r4 scene split = %v with %d reports, want true/5", r4.SceneSplit, len(r4.Scenes))
	}

	r5 := CheckScriptWordBudget(fiveSceneText(879, 879, 879, 879, 878), plan)
	if math.Abs(r5.Aggregate.DeviationPercent-25.5) > 0.06 {
		t.Fatalf("r5 deviation = %.2f%%, want +25.5%%", r5.Aggregate.DeviationPercent)
	}
	if r5.Pass {
		t.Fatalf("r5 (+25.5%%) must fail the aggregate ±25%% gate: %+v", r5.Aggregate)
	}
	if len(r5.Scenes) != 5 {
		t.Fatalf("r5 scene reports = %d, want 5", len(r5.Scenes))
	}
	for i, scene := range r5.Scenes {
		if scene.TargetWords != 700 {
			t.Fatalf("r5 scene[%d] target = %d, want 700", i, scene.TargetWords)
		}
		if scene.Pass {
			t.Fatalf("r5 scene[%d] actual=%d target=%d must fail its own gate", i, scene.ActualWords, scene.TargetWords)
		}
	}
}

// TestEffectiveScriptTargetFallsBackToPlanTarget pins the second half of the
// CODEX-02 contract: when no scene declares TargetWords, the aggregate target
// is plan.TargetWords, never plan.TargetWords multiplied by the scene count.
func TestEffectiveScriptTargetFallsBackToPlanTarget(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{
		TargetWords: 3500,
		Segments:    make([]scriptpkg.ScriptSegment, 5),
	}
	if got := effectiveScriptTargetWords(plan); got != 3500 {
		t.Fatalf("effectiveScriptTargetWords = %d, want plan.TargetWords 3500", got)
	}
	if got := effectiveSegmentTargetWords(plan.Segments[0], plan); got != 3500 {
		t.Fatalf("legacy per-scene chain = %d, want plan.TargetWords 3500", got)
	}
}

// TestWordBudgetPolicyStrictFailsClosed proves the operator policy: warn
// keeps the historical observational behavior, strict returns the typed
// sentinel instead of shipping the over-budget script.
func TestWordBudgetPolicyStrictFailsClosed(t *testing.T) {
	t.Setenv(legacyWordBudgetPolicy, "")
	engine := &Engine{log: zap.NewNop()}
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:       "codex-02",
		Segments: []scriptpkg.ScriptSegment{{Topic: "scene", TargetWords: 100}},
	}
	text := budgetWords(126)

	t.Setenv(EnvWordBudgetPolicy, WordBudgetPolicyWarn)
	if err := engine.enforceWordBudget(plan, text); err != nil {
		t.Fatalf("warn policy must not block: %v", err)
	}

	t.Setenv(EnvWordBudgetPolicy, "enforce")
	err := engine.enforceWordBudget(plan, text)
	if !errors.Is(err, scriptpkg.ErrWordBudgetExceeded) {
		t.Fatalf("strict policy error = %v, want ErrWordBudgetExceeded", err)
	}
	var budgetErr *scriptpkg.WordBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("strict policy error type = %T, want *WordBudgetError", err)
	}
	if budgetErr.TargetWords != 100 || budgetErr.ActualWords != 126 {
		t.Fatalf("budget error facts = target %d actual %d, want 100/126", budgetErr.TargetWords, budgetErr.ActualWords)
	}
}
