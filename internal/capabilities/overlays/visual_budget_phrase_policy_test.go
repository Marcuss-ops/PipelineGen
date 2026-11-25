package overlays

import (
	"errors"
	"strings"
	"testing"
)

func TestApplyPhraseBudgetPolicyWarnIsExplicitAndNonFatal(t *testing.T) {
	budget, err := ApplyPhraseBudgetPolicy(PhraseOverlayBudget{
		Requested: 45, Materialized: 32, Shortfall: 13,
	}, "")
	if err != nil {
		t.Fatalf("warn policy returned an error: %v", err)
	}
	if budget.Policy != PhraseBudgetPolicyWarn {
		t.Fatalf("policy = %q, want %q", budget.Policy, PhraseBudgetPolicyWarn)
	}
	if !strings.Contains(budget.Warning, "requested 45") || !strings.Contains(budget.Warning, "missing 13") {
		t.Fatalf("warning = %q, want requested/materialized/shortfall", budget.Warning)
	}
}

func TestApplyPhraseBudgetPolicyStrictFailsClosed(t *testing.T) {
	budget, err := ApplyPhraseBudgetPolicy(PhraseOverlayBudget{
		Requested: 45, Materialized: 32, Shortfall: 13,
	}, PhraseBudgetPolicyStrict)
	if !errors.Is(err, ErrPhraseOverlayShortfall) {
		t.Fatalf("err = %v, want ErrPhraseOverlayShortfall", err)
	}
	if budget.Policy != PhraseBudgetPolicyStrict {
		t.Fatalf("policy = %q, want strict", budget.Policy)
	}
}

func TestApplyPhraseBudgetPolicyNoShortfallHasNoWarning(t *testing.T) {
	budget, err := ApplyPhraseBudgetPolicy(PhraseOverlayBudget{
		Requested: 45, Materialized: 45,
	}, PhraseBudgetPolicyStrict)
	if err != nil {
		t.Fatalf("complete budget returned an error: %v", err)
	}
	if budget.Warning != "" {
		t.Fatalf("warning = %q, want empty when the budget is complete", budget.Warning)
	}
}

func TestApplyPhraseBudgetPolicyUnknownPolicyFallsBackToWarn(t *testing.T) {
	budget, err := ApplyPhraseBudgetPolicy(PhraseOverlayBudget{
		Requested: 2, Materialized: 1, Shortfall: 1,
	}, "typo-policy")
	if err != nil {
		t.Fatalf("unknown policy must not fail closed: %v", err)
	}
	if budget.Policy != PhraseBudgetPolicyWarn {
		t.Fatalf("policy = %q, want warn", budget.Policy)
	}
}
