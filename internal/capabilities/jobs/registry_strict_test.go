package jobs

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// MEDIA-CUTOVER (September 2026): the previous tests in this file pinned
// `RegisterCoreHandlers`'s "Qdrant enabled + missing mandatory dep must abort
// boot" contract. That function was the SQLite→Qdrant media projection
// registration surface and is now DELETED: the media index plane is the
// PostgreSQL pgvector `PostgresIndexWorker`
// (internal/platform/postgres/media/outbox_worker.go), whose fail-closed
// contract is the nil-dependency panics in `NewPostgresIndexWorker`, and the
// composition root's `registerOutboxCoreHandlers`
// (internal/app/wiring/build_outbox_handlers.go) unconditionally registers no
// media handler in any mode.
//
// The surviving fail-closed invariant on this package's registration entry
// point is pinned here: a nil registry aborts with a typed error before any
// handler is registered, so a mis-wired boot cannot silently degrade into a
// dead-lettering runtime.

func TestRegisterOptionalHandlersRejectsNilRegistry(t *testing.T) {
	err := RegisterOptionalHandlers(nil, zap.NewNop(), &Deps{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "registry is nil")
}

func TestRegisterOptionalHandlersRegistersAtomicHandlerSet(t *testing.T) {
	registry := outboxevents.NewHandlerRegistry()

	require.NoError(t, RegisterOptionalHandlers(registry, zap.NewNop(), &Deps{}, nil))

	// The optional set must always contain the workflow-step and
	// asset-published receipts, which carry no external dependency. A
	// registration that silently dropped them would hide a boot wiring bug
	// behind a dead-letter at runtime.
	for _, eventType := range []string{
		outboxevents.EventWorkflowStepCompleted,
		outboxevents.EventWorkflowStepFailed,
		outboxevents.EventAssetPublished,
	} {
		_, registered := registry.Get(eventType)
		require.Truef(t, registered, "optional handler for %q must be registered", eventType)
	}
}
