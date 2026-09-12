package outboxevents_test

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// TestOutboxAdapterReexportsMatchOwner pins the compile-time re-exports in both
// engine adapters to the canonical owner in internal/kernel/event.
//
// The values must stay byte-identical across the SQLite and PostgreSQL
// outboxes: they are written into the same logical column, and before
// centralisation each adapter declared its own copy of the string (a drift
// hazard no scanner could see, because every scanner exempted both copies).
//
// This lives here, not in internal/kernel/event, because the kernel boundary
// rule forbids the kernel from importing platform packages.
func TestOutboxAdapterReexportsMatchOwner(t *testing.T) {
	if outboxevents.SupersedeStatus != event.OutboxStatusSuperseded {
		t.Errorf("sqlite outbox SupersedeStatus = %q, want %q", outboxevents.SupersedeStatus, event.OutboxStatusSuperseded)
	}
	if pgmedia.SupersedeStatus != event.OutboxStatusSuperseded {
		t.Errorf("postgres media SupersedeStatus = %q, want %q", pgmedia.SupersedeStatus, event.OutboxStatusSuperseded)
	}
	if outboxevents.PriorityNormal != event.OutboxPriorityNormal {
		t.Errorf("sqlite outbox PriorityNormal = %d, want %d", outboxevents.PriorityNormal, event.OutboxPriorityNormal)
	}
	if outboxevents.PriorityHigh != event.OutboxPriorityHigh {
		t.Errorf("sqlite outbox PriorityHigh = %d, want %d", outboxevents.PriorityHigh, event.OutboxPriorityHigh)
	}
	if pgmedia.PriorityNormal != event.OutboxPriorityNormal {
		t.Errorf("postgres media PriorityNormal = %d, want %d", pgmedia.PriorityNormal, event.OutboxPriorityNormal)
	}
	if pgmedia.PriorityHigh != event.OutboxPriorityHigh {
		t.Errorf("postgres media PriorityHigh = %d, want %d", pgmedia.PriorityHigh, event.OutboxPriorityHigh)
	}
}
