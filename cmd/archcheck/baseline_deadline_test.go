// Package main — baseline_deadline_test.go: a transitional baseline entry is a
// promise ("this debt is removed by <deadline>"). Without a gate the promise
// decays silently: the file stays "active" forever and nobody notices that the
// date passed. This test is that gate.
//
// The real file is checked today, and the checker itself is checked with a
// synthetic expired entry so the test cannot pass because it stopped looking.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// timeNowUTCDate is the gate's clock seam: the deadline is compared against the
// UTC date, so the result does not depend on the host's timezone.
func timeNowUTCDate() string { return time.Now().UTC().Format("2006-01-02") }

// baselineEntry is the subset of the governance document this gate needs.
type baselineEntry struct {
	Rule           string `json:"rule"`
	Owner          string `json:"owner"`
	Deadline       string `json:"deadline"`
	RemovalTrigger string `json:"removal_trigger"`
}

// baselineDeadlineProblems returns one message per entry that is incomplete or
// whose deadline is in the past. ISO dates (YYYY-MM-DD) compare correctly as
// strings, which is exactly the format the document uses.
func baselineDeadlineProblems(entries []baselineEntry, today string) []string {
	var problems []string
	for _, entry := range entries {
		name := entry.Rule
		if name == "" {
			name = "<unnamed rule>"
		}
		if entry.Rule == "" {
			problems = append(problems, name+": entry has no rule id")
		}
		if entry.Owner == "" {
			problems = append(problems, name+": entry has no owner")
		}
		if entry.RemovalTrigger == "" {
			problems = append(problems, name+": entry has no removal trigger")
		}
		if entry.Deadline == "" {
			problems = append(problems, name+": entry has no deadline")
			continue
		}
		if entry.Deadline < today {
			problems = append(problems, name+": deadline "+entry.Deadline+" expired (today "+today+")")
		}
	}
	return problems
}

func TestTransitionalBaselineDeadlinesAreLive(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "migrations", "archcheck-strict-baseline.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Entries []baselineEntry `json:"transitional_baselines_sorted_urgency"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	// The gate runs with the real clock: an expired entry must fail CI, not
	// wait for an operator to notice.
	today := timeNowUTCDate()
	if problems := baselineDeadlineProblems(doc.Entries, today); len(problems) > 0 {
		for _, problem := range problems {
			t.Errorf("%s", problem)
		}
	}
}

func TestBaselineDeadlineCheckerRejectsExpiredEntry(t *testing.T) {
	entries := []baselineEntry{{
		Rule:           "percheck_example",
		Owner:          "example-owner",
		Deadline:       "2020-01-01",
		RemovalTrigger: "delete the legacy path",
	}}
	problems := baselineDeadlineProblems(entries, "2026-11-21")
	if len(problems) == 0 {
		t.Fatal("an expired entry must produce a problem; the checker is not looking")
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly the expired deadline", problems)
	}

	live := []baselineEntry{{
		Rule:           "percheck_example",
		Owner:          "example-owner",
		Deadline:       "2099-01-01",
		RemovalTrigger: "delete the legacy path",
	}}
	if problems := baselineDeadlineProblems(live, "2026-11-21"); len(problems) != 0 {
		t.Fatalf("a complete live entry must pass, got %v", problems)
	}
}
