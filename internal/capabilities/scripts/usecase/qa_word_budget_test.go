// Package scripts — qa_word_budget_test.go (PR-CS-1, FASE 5, DoD #6).
//
// Pins CheckWordBudget to the user-spec 5-case test list.
// Each test is narrow + didactic so a regression surfaces
// with one failing sub-test + one-line diagnosis.
package usecase

import (
	"strings"
	"testing"
)

// textOfNWords builds a deterministic N-word string via
// "word word word ..." (no trailing whitespace, single spaces).
// CountWords of this string returns exactly N.
func textOfNWords(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("word ", n), " ")
}

// ── 1. target 100, actual 100 → PASS ────────────────────────────────

func TestBudget_Target100_Actual100_Pass(t *testing.T) {
	t.Parallel()
	got := CheckWordBudget(textOfNWords(100), 100)
	if !got.Pass {
		t.Fatalf("exact-target input MUST pass; got report: %+v", got)
	}
	if got.TargetWords != 100 {
		t.Errorf("TargetWords want 100, got %d", got.TargetWords)
	}
	if got.ActualWords != 100 {
		t.Errorf("ActualWords want 100, got %d", got.ActualWords)
	}
	if got.DeviationPercent != 0 {
		t.Errorf("DeviationPercent want 0, got %v", got.DeviationPercent)
	}
}

// ── 2. target 100, actual 74 → FAIL (under min 75) ──────────────────

func TestBudget_Target100_Actual74_Fail(t *testing.T) {
	t.Parallel()
	got := CheckWordBudget(textOfNWords(74), 100)
	if got.Pass {
		t.Fatalf("74 words vs target=100 (min=75) MUST fail; got report: %+v", got)
	}
	if got.ActualWords != 74 {
		t.Errorf("ActualWords want 74, got %d", got.ActualWords)
	}
	// 74/100 -> -26%.
	if dev := got.DeviationPercent; dev != -26 {
		t.Errorf("DeviationPercent want -26, got %v", dev)
	}
}

// ── 3. target 100, actual 126 → FAIL (above max 125) ────────────────

func TestBudget_Target100_Actual126_Fail(t *testing.T) {
	t.Parallel()
	got := CheckWordBudget(textOfNWords(126), 100)
	if got.Pass {
		t.Fatalf("126 words vs target=100 (max=125) MUST fail; got report: %+v", got)
	}
	if got.ActualWords != 126 {
		t.Errorf("ActualWords want 126, got %d", got.ActualWords)
	}
	// 126/100 -> +26%.
	if dev := got.DeviationPercent; dev != 26 {
		t.Errorf("DeviationPercent want 26, got %v", dev)
	}
}

// ── 4. target 0 → no gate ──────────────────────────────────────────

func TestBudget_Target0_NoGate(t *testing.T) {
	t.Parallel()
	cases := []int{0, 1, 50, 1000}
	for _, n := range cases {
		n := n
		t.Run("words_"+itoaForTest(n), func(t *testing.T) {
			t.Parallel()
			got := CheckWordBudget(textOfNWords(n), 0)
			if !got.Pass {
				t.Errorf("target=0 MUST always pass; got report: %+v", got)
			}
			if got.TargetWords != 0 {
				t.Errorf("TargetWords must echo 0, got %d", got.TargetWords)
			}
			if got.ActualWords != n {
				t.Errorf("ActualWords want %d, got %d", n, got.ActualWords)
			}
			if got.DeviationPercent != 0 {
				t.Errorf("DeviationPercent must be 0 when target=0, got %v", got.DeviationPercent)
			}
		})
	}
}

// ── 5. Short target 12 → acceptable range [9, 15] ───────────────────

func TestBudget_ShortTarget12_AcceptableRange9to15(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int
		pass bool
	}{
		{8, false},  // below min 9
		{9, true},   // inclusive lower bound
		{10, true},  // mid-range
		{12, true},  // exact
		{15, true},  // inclusive upper bound
		{16, false}, // above max 15
	}
	for _, tc := range cases {
		tc := tc
		t.Run("words_"+itoaForTest(tc.n), func(t *testing.T) {
			t.Parallel()
			got := CheckWordBudget(textOfNWords(tc.n), 12)
			if got.Pass != tc.pass {
				t.Fatalf("target=12 n=%d: want pass=%v, got pass=%v (report: %+v)",
					tc.n, tc.pass, got.Pass, got)
			}
		})
	}
}

// ── helpers ──────────────────────────────────────────────────────────

// itoaForTest avoids importing strconv everywhere — keeps the
// test file dependency-light. Equivalent to strconv.Itoa for
// non-negative ints.
func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoaForTest(-n)
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
