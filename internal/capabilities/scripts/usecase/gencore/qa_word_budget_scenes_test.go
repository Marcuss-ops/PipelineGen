package gencore

import (
	"math"
	"strings"
	"testing"
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
