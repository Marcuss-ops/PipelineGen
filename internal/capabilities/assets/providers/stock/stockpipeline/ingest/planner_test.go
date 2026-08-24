// Package stock — planner_test.go (Stock Cutover Commit 1, July 2026).
//
// Round-trip test for the deterministic ClipPlanner. Per the
// ExecutePlan-1 spec verification: same input → same OutputLogicalID
// (and same StartSec/EndSec). The planner's determinism is what
// unlocks the plan-persistence pattern: once the plan is written
// to the typed envelope, retries + cuts consume the SAME plan
// rather than re-computing random offsets.
package ingest

import (
	"context"
	"errors"
	"testing"
)

func TestDeterministicPlanner_SameInputSamePlan(t *testing.T) {
	p := NewDeterministicPlanner()
	src := VideoSource{
		URL:    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Title:  "Test",
		Source: "youtube",
	}
	plans1, err := p.Plan(context.Background(), src, 60, 10, "policy-v1")
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	plans2, err := p.Plan(context.Background(), src, 60, 10, "policy-v1")
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(plans1) != len(plans2) {
		t.Fatalf("plan length differs across invocations: %d vs %d", len(plans1), len(plans2))
	}
	for i := range plans1 {
		if plans1[i].OutputLogicalID != plans2[i].OutputLogicalID {
			t.Errorf("plan[%d].OutputLogicalID differs: %q vs %q",
				i, plans1[i].OutputLogicalID, plans2[i].OutputLogicalID)
		}
		if plans1[i].StartSec != plans2[i].StartSec {
			t.Errorf("plan[%d].StartSec differs: %v vs %v",
				i, plans1[i].StartSec, plans2[i].StartSec)
		}
		if plans1[i].EndSec != plans2[i].EndSec {
			t.Errorf("plan[%d].EndSec differs: %v vs %v",
				i, plans1[i].EndSec, plans2[i].EndSec)
		}
	}
}

func TestDeterministicPlanner_DifferentSourcesDifferentPlans(t *testing.T) {
	p := NewDeterministicPlanner()
	srcA := VideoSource{URL: "https://www.youtube.com/watch?v=alpha", Title: "A"}
	srcB := VideoSource{URL: "https://www.youtube.com/watch?v=bravo", Title: "B"}
	plansA, err := p.Plan(context.Background(), srcA, 60, 10, "policy-v1")
	if err != nil {
		t.Fatalf("Plan A: %v", err)
	}
	plansB, err := p.Plan(context.Background(), srcB, 60, 10, "policy-v1")
	if err != nil {
		t.Fatalf("Plan B: %v", err)
	}
	if len(plansA) != len(plansB) {
		t.Fatalf("plan length differs: %d vs %d", len(plansA), len(plansB))
	}
	someDiffer := false
	for i := range plansA {
		if plansA[i].OutputLogicalID != plansB[i].OutputLogicalID {
			someDiffer = true
			break
		}
	}
	if !someDiffer {
		t.Errorf("expected different sources to produce different OutputLogicalIDs")
	}
}

func TestDeterministicPlanner_BudgetSmallerThanClip(t *testing.T) {
	p := NewDeterministicPlanner()
	src := VideoSource{URL: "https://www.youtube.com/watch?v=x", Title: "x"}
	_, err := p.Plan(context.Background(), src, 5, 10, "policy-v1")
	if err == nil {
		t.Fatalf("expected ErrPlannerBudgetTooSmall for budget < clipDur, got nil")
	}
	if !errors.Is(err, ErrPlannerBudgetTooSmall) {
		t.Errorf("expected ErrPlannerBudgetTooSmall via errors.Is, got %v", err)
	}
}

func TestDeterministicPlanner_BudgetEqualsClipReturnsSingleFullWindow(t *testing.T) {
	p := NewDeterministicPlanner()
	src := VideoSource{URL: "https://www.youtube.com/watch?v=z", Title: "z"}
	plans, err := p.Plan(context.Background(), src, 10, 10, "policy-v1")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("expected 1 clip when budget == clipDur, got %d", len(plans))
	}
	if plans[0].StartSec != 0 || plans[0].EndSec != 10 {
		t.Errorf("expected start=0 end=10, got start=%v end=%v",
			plans[0].StartSec, plans[0].EndSec)
	}
}

func TestDeterministicPlanner_DistributesWindowsAcrossKnownSource(t *testing.T) {
	p := NewDeterministicPlanner()
	plans, err := p.Plan(context.Background(), VideoSource{
		URL:         "https://www.youtube.com/watch?v=distributed",
		DurationSec: 1800,
	}, 60, 4, "policy-v1")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plans) != 15 {
		t.Fatalf("expected 15 plans, got %d", len(plans))
	}
	for i, plan := range plans {
		if plan.EndSec-plan.StartSec != 4 {
			t.Errorf("plan[%d] duration = %.3f, want 4", i, plan.EndSec-plan.StartSec)
		}
		if i > 0 && plan.StartSec < plans[i-1].EndSec {
			t.Errorf("plan[%d] overlaps previous plan: %.3f < %.3f", i, plan.StartSec, plans[i-1].EndSec)
		}
	}
	if plans[1].StartSec <= plans[0].EndSec || plans[14].StartSec <= plans[1].StartSec {
		t.Fatal("planned windows were not distributed across the source")
	}
}

func TestDeterministicPlanner_OutputLogicalIDFormatSuffixIndex(t *testing.T) {
	// Operators relying on Visual ID format for log greps will hit
	// "planner:HEX:N" pattern. Lock the format string so a future
	// implementation does not silently break the log regex.
	p := NewDeterministicPlanner()
	src := VideoSource{URL: "https://www.youtube.com/watch?v=q", Title: "q"}
	plans, err := p.Plan(context.Background(), src, 30, 10, "policy-v1")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}
	for i, p := range plans {
		expected := indexSuffixFormat(i)
		if !endsWith(p.OutputLogicalID, expected) {
			t.Errorf("plan[%d].OutputLogicalID = %q; expected to end with %q", i, p.OutputLogicalID, expected)
		}
	}
}

func TestExplicitPlanner_SameSourceDifferentWindows_UniqueIDs(t *testing.T) {
	src := VideoSource{URL: "https://www.youtube.com/watch?v=Di-Awl0XyQs"}
	clips := []ClipSpec{
		{URL: src.URL, StartSec: 0, EndSec: 5, Slug: "seg-a-0-5"},
		{URL: src.URL, StartSec: 80, EndSec: 85, Slug: "seg-a-80-85"},
	}
	plans, err := NewExplicitPlanner(clips).Plan(context.Background(), src, 0, 0, "v1")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}
	if plans[0].OutputLogicalID == plans[1].OutputLogicalID {
		t.Fatalf("different clip windows must produce different OutputLogicalIDs, both = %q", plans[0].OutputLogicalID)
	}

	// The same window must still mint the same ID (determinism preserved).
	again, err := NewExplicitPlanner([]ClipSpec{{URL: src.URL, StartSec: 0, EndSec: 5}}).Plan(context.Background(), src, 0, 0, "v1")
	if err != nil {
		t.Fatalf("Plan again: %v", err)
	}
	if again[0].OutputLogicalID != plans[0].OutputLogicalID {
		t.Fatalf("same window must mint the same OutputLogicalID: %q vs %q", again[0].OutputLogicalID, plans[0].OutputLogicalID)
	}
}

func indexSuffixFormat(i int) string {
	return ":" + itoa(i)
}

func endsWith(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
