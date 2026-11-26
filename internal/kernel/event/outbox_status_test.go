package event_test

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// TestOutboxLifecycleStatuses pins the canonical lifecycle buckets. The set is
// the contract every operator view iterates: dropping a bucket silently hides
// those rows from the dashboard, which is exactly how `superseded` became
// invisible in two of the four views before this vocabulary was centralised.
func TestOutboxLifecycleStatuses(t *testing.T) {
	want := []string{"pending", "processing", "completed", "dead_letter", "superseded"}
	got := event.OutboxLifecycleStatuses()
	if len(got) != len(want) {
		t.Fatalf("OutboxLifecycleStatuses() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("OutboxLifecycleStatuses()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if errors := event.OutboxErrorStatuses(); len(errors) != len(want) {
		t.Fatalf("OutboxErrorStatuses() = %v, want the full lifecycle %v", errors, want)
	}
}

// TestOutboxLifecycleStatuses_NotAliased pins that each call returns a fresh
// slice: a caller mutating its copy must not corrupt the registry.
func TestOutboxLifecycleStatuses_NotAliased(t *testing.T) {
	first := event.OutboxLifecycleStatuses()
	first[0] = "tampered"
	if got := event.OutboxLifecycleStatuses()[0]; got != event.OutboxStatusPending {
		t.Fatalf("registry was mutated through a returned slice: [0] = %q", got)
	}
}

// TestIsFailedOutboxStatus pins the failure half of the terminal set: a
// producer answering an idempotent re-emit must not treat a dead-lettered row
// as a delivered event, while completed/superseded stay success terminals.
func TestIsFailedOutboxStatus(t *testing.T) {
	failed := []string{"dead_letter", "dead", " DEAD_LETTER ", "Dead"}
	for _, status := range failed {
		if !event.IsFailedOutboxStatus(status) {
			t.Errorf("IsFailedOutboxStatus(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"", "pending", "processing", "completed", "superseded"} {
		if event.IsFailedOutboxStatus(status) {
			t.Errorf("IsFailedOutboxStatus(%q) = true, want false", status)
		}
	}
	// The two predicates must agree on the failure subset being terminal.
	for _, status := range failed {
		if !event.IsTerminalOutboxStatus(status) {
			t.Errorf("%q is a failed status but not terminal", status)
		}
	}
}

// TestIsTerminalOutboxStatus pins the single predicate. Before the
// consolidation four copies existed and two disagreed on the legacy `dead`
// spelling.
func TestIsTerminalOutboxStatus(t *testing.T) {
	terminal := []string{
		"completed",
		"dead_letter",
		"superseded",
		"dead", // legacy spelling of dead_letter
		" COMPLETED ",
		"Dead_Letter",
	}
	for _, status := range terminal {
		if !event.IsTerminalOutboxStatus(status) {
			t.Errorf("IsTerminalOutboxStatus(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"", "pending", "processing", "unknown"} {
		if event.IsTerminalOutboxStatus(status) {
			t.Errorf("IsTerminalOutboxStatus(%q) = true, want false", status)
		}
	}
}
