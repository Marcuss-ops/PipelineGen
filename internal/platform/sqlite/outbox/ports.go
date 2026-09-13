package outbox

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// ── Ports ──────────────────────────────────────────────────────────────

// outboxEnqueuer is the canonical port interface for the dispatcher's
// outbox-write seam (AGENTS.md Pattern 0 — port abstraction). The
// dispatcher only ever calls .Enqueue inside the same SQL tx that
// flips an OPERATIONAL (non-media) state column; the wider crash-tolerant
// methods (MarkCompleted, MarkFailed, etc.) are owned by the outbox
// worker pool, not the dispatcher.
type outboxEnqueuer interface {
	Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*outboxevents.EnqueueResult, error)
}

// Compile-time assertion: any signature drift between the
// canonical *outboxevents.Repository and the outboxEnqueuer port
// surfaces at build, not at first runtime panic.
var _ outboxEnqueuer = (*outboxevents.Repository)(nil)
