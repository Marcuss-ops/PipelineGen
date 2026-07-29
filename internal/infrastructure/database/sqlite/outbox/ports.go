package outbox

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Ports ──────────────────────────────────────────────────────────────

// ClipsUpserter is the *assets.ClipsRepository method surface the Dispatcher needs.
// Defined as an interface so unit tests can substitute a fake without
// pulling the full assets.ClipsRepository dependency.
type ClipsUpserter interface {
	UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error
}

// outboxEnqueuer is the canonical port interface for the dispatcher's
// outbox-write seam (AGENTS.md Pattern 0 — port abstraction). The
// dispatcher only ever calls .Enqueue inside the same SQL tx that
// flips media_assets.lifecycle_state; the wider crash-tolerant
// methods (MarkCompleted, MarkFailed, etc.) are owned by the outbox
// worker pool, not the dispatcher.
type outboxEnqueuer interface {
	Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*outboxevents.EnqueueResult, error)
}

// Compile-time assertion: any signature drift between the
// canonical *outboxevents.Repository and the outboxEnqueuer port
// surfaces at build, not at first runtime panic.
var _ outboxEnqueuer = (*outboxevents.Repository)(nil)

// ── MultiClipsUpserter ────────────────────────────────────────────────

// MultiClipsUpserter routes UpsertClipTx calls to one of several underlying
// repositories based on `clip.Source`. Useful when a single outbox.Dispatcher
// must ingest across many per-source assets.ClipsRepository instances.
type MultiClipsUpserter struct {
	repos       map[string]ClipsUpserter
	defaultRepo ClipsUpserter
	log         *zap.Logger
}

// Compile-time interface compliance check.
var _ ClipsUpserter = (*MultiClipsUpserter)(nil)

// NewMultiClipsUpserter constructs a routing upserter. `repos` is keyed by
// clip.Source (e.g. "youtube", "stock", "artlist") and may be nil.
func NewMultiClipsUpserter(repos map[string]ClipsUpserter, defaultRepo ClipsUpserter, log *zap.Logger) *MultiClipsUpserter {
	if log == nil {
		log = zap.NewNop()
	}
	return &MultiClipsUpserter{
		repos:       repos,
		defaultRepo: defaultRepo,
		log:         log,
	}
}

// UpsertClipTx routes the call based on clip.Source.
func (m *MultiClipsUpserter) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	if m == nil {
		return fmt.Errorf("outbox.MultiClipsUpserter is nil")
	}
	if clip == nil {
		return fmt.Errorf("outbox.MultiClipsUpserter: clip is nil")
	}
	var repo ClipsUpserter
	var matched bool
	if clip.Source != "" {
		if r, ok := m.repos[string(clip.Source)]; ok && r != nil {
			repo = r
			matched = true
		}
	}
	if !matched {
		m.log.Debug("MultiClipsUpserter: using default repo for unknown source",
			zap.String("source", string(clip.Source)),
			zap.String("asset_id", clip.ID),
		)
		repo = m.defaultRepo
	}
	if repo == nil {
		return fmt.Errorf("outbox.MultiClipsUpserter: no repository for source %q and no default configured", string(clip.Source))
	}
	return repo.UpsertClipTx(ctx, tx, clip)
}
