// Package outboxhandlers wires concrete handlers into an
// outboxevents.HandlerRegistry. Each handler is responsible for one
// event type (registered by EventType()).
//
// Conventions:
//   - Real handlers (workflow_step_*, asset.index.requested) do something
//     useful: parse the payload, call the canonical service, emit a
//     structured audit log. They return errors on failure so the outbox
//     pool retries or dead-letters the event.
//   - Stubs (delivery, metadata_export, provider_sync) are placeholder
//     implementations for events that have a defined contract but no
//     production handler yet. They return errors so the outbox pool
//     retries and eventually dead-letters the event — operators can
//     inspect dead_letter rows for queue depth visibility instead of
//     silently draining events.
//
// All handlers MUST be safe for concurrent invocation. The outbox
// worker pool calls them from N goroutines.
package outboxhandlers

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// RegisterAll wires the canonical set of handlers into the registry.
// indexer is the real IndexingHandler's dependency (typically
// *clipindexer.Service). Pass nil to skip registration (tests,
// partial wiring) — the handler is optional, not mandatory, because
// the outbox worker pool's health is more important than a single
// handler's presence.
//
// Returns an error if any individual registration fails (e.g. duplicate
// event type from a partial previous registration).
func RegisterAll(registry *outboxevents.HandlerRegistry, log *zap.Logger, indexer IndexClipper) error {
	realHandlers := []outboxevents.Handler{
		&WorkflowStepCompletedHandler{log: log},
		&WorkflowStepFailedHandler{log: log},
	}
	// Real indexing handler — replaces the IndexingHandlerStub.
	// nil indexer means the handler is skipped (tests, partial wiring).
	if indexer != nil {
		realHandlers = append(realHandlers, &IndexingHandler{
			indexer: indexer,
			log:     log,
		})
	}

	for _, h := range realHandlers {
		if err := registry.Register(h); err != nil {
			return err
		}
	}

	stubs := []outboxevents.Handler{
		&DeliveryHandlerStub{log: log},
		&MetadataExportHandlerStub{log: log},
		&ProviderSyncHandlerStub{log: log},
	}
	for _, h := range stubs {
		if err := registry.Register(h); err != nil {
			return err
		}
	}

	log.Info("outbox handlers registered",
		zap.Int("real", len(realHandlers)),
		zap.Int("stubs", len(stubs)),
	)
	return nil
}
