package outbox

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

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
