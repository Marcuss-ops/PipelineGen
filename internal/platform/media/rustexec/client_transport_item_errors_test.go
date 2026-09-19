package rustexec

import (
	"strings"
	"testing"
)

// TestFormatItemErrors_SurfacesPerItemCause is the regression pin for the
// swallowed-diagnostics defect (September 2026).
//
// The Rust media executor returns one items[] entry per job, each carrying the
// real ffmpeg/ffprobe stderr. The top-level error is only a generic summary
// ("all cut jobs failed"). Folding only the summary into the returned error
// left "the stock cut failed" with no cause, and a manual reproduction of the
// same invocation succeeded because it did not share the service's
// environment — the failure was undiagnosable from the logs alone.
func TestFormatItemErrors_SurfacesPerItemCause(t *testing.T) {
	items := []cutItem{
		{JobID: "job-a", Status: "failed", Error: "ffmpeg cut failed: No space left on device"},
		{JobID: "job-b", Status: "failed", Error: "ffmpeg cut failed: Invalid data found when processing input"},
		{JobID: "job-c", Status: "validated"},
	}

	got := formatItemErrors(items)

	for _, want := range []string{
		"No space left on device",
		"Invalid data found when processing input",
		"job-a",
		"job-b",
		"failed 2/3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted item errors %q does not mention %q", got, want)
		}
	}
	// A validated item must not be reported as a failure.
	if strings.Contains(got, "job-c") {
		t.Errorf("a validated item must not appear in the failure reasons: %q", got)
	}
}

// TestFormatItemErrors_BoundsAndCounts pins that a large batch cannot flood the
// log while the suppressed count stays visible — silent truncation would hide
// failures just as effectively as dropping them.
func TestFormatItemErrors_BoundsAndCounts(t *testing.T) {
	items := make([]cutItem, 0, 10)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		items = append(items, cutItem{JobID: id, Status: "failed", Error: "boom"})
	}

	got := formatItemErrors(items)

	if strings.Count(got, "boom") != maxReportedItemErrors {
		t.Errorf("expected exactly %d reported reasons, got %q", maxReportedItemErrors, got)
	}
	if !strings.Contains(got, "+2 more") {
		t.Errorf("suppressed items must be counted, got %q", got)
	}
	if !strings.Contains(got, "failed 5/5") {
		t.Errorf("the total failure count must be reported, got %q", got)
	}
}

// TestFormatItemErrors_EmptyWhenNoItemFailed pins the byte-for-byte
// compatibility requirement: a response that fails without per-item detail
// keeps its original message.
func TestFormatItemErrors_EmptyWhenNoItemFailed(t *testing.T) {
	if got := formatItemErrors(nil); got != "" {
		t.Errorf("nil items must add nothing, got %q", got)
	}
	if got := formatItemErrors([]cutItem{{JobID: "a", Status: "validated"}}); got != "" {
		t.Errorf("all-validated items must add nothing, got %q", got)
	}
	// A failed item with no reason still counts as a failure (the caller must
	// not see "0 failures" while the response says otherwise).
	got := formatItemErrors([]cutItem{{JobID: "a", Status: "failed"}})
	if !strings.Contains(got, "no reason reported") {
		t.Errorf("a reasonless failure must still be reported, got %q", got)
	}
}
