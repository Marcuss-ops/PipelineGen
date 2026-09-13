package outbox

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// ErrMediaRestoreRequiresPostgresSaga is the typed fail-closed sentinel
// returned by Dispatcher.EnqueueAndRestore.
//
// MEDIA-SSOT (September 2026): a media restore is a MEDIA mutation and must
// commit its index-state flip together with the
// `asset.index.restore_requested` outbox event on the PostgreSQL media SSOT
// in a single PostgreSQL transaction. The SQLite dispatcher holds no media
// writer and must not fabricate the event on the operational outbox, so it
// fails closed and points callers at the canonical
// pgmedia.PostgresMediaCommitter saga.
var ErrMediaRestoreRequiresPostgresSaga = errors.New("outbox.Dispatcher.EnqueueAndRestore: media restore is owned by the PostgreSQL media saga (pgmedia.PostgresMediaCommitter); the SQLite dispatcher cannot mutate the media SSOT")

// ── Errors / Schema Constants ──────────────────────────────────────────

// DeleteRequestSchemaVersion is the canonical, EXACT string the
// handler on the consumer side accepts. OWNED by internal/kernel/event
// (godlike/06 one owner per fact); this is a compile-time re-export so the
// producer and the consumer resolve the same constant.
const DeleteRequestSchemaVersion = event.AssetIndexDeleteRequestedV1Schema

// Compile-time assertion (Wave 22 task 1 of 5, June 2026):
// *outbox.Dispatcher statically satisfies the canonical
// mutations.AssetMutationDispatcher SSOT interface declared in
// internal/capabilities/assets/mutations/dispatcher.go.
//
// The standard AGENTS.md Pattern 0 layering rule forbids
// `internal/platform/` from importing
// `internal/capabilities/`. The placement of the interface in
// `internal/capabilities/assets/mutations/` is a deliberate layering
// INVERSION: the canonical asset-mutation dispatcher port lives
// alongside its consumer (the application layer), and the dispatcher
// assertion here grants the dispatcher its explicit SSOT membership.
var _ mutations.AssetMutationDispatcher = (*Dispatcher)(nil)
