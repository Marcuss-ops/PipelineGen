// Package indexing contains the Qdrant write-side surface. The 4-file
// split landed under PR-SPLIT-INDEX-WRITER (Fase 3.3 of
// LONG-FILES-DECOMPOSITION-V2-2026-07-06, July 2026) lays the package
// out as follows:
//
//   - index_writer.go        — IndexWriter struct + NewIndexWriter ctor +
//     the 1-line UpsertFromClip delegator + the
//     newPartialUpsertError helper (slim
//     orchestrator, ~110 LoC).
//   - index_writer_ops.go    — UpsertFromClips + DeletePoints + ReindexAll
//     (~240 LoC).
//   - index_writer_validate.go — ValidatePoint package-level function
//     (~75 LoC).
//   - index_writer_types.go  — AssetData struct + AssetStore interface
//     (~95 LoC).
//
// All siblings are part of the same Go package (internal/platform/qdrant/indexing)
// so cross-file symbol resolution is via package-scope visibility (godlike/06 SSOT).
// Pure code-motion: 0 new exported symbols, 0 signature changes, 0 dependency
// changes. Lookup paths `indexing.IndexWriter` / `indexing.AssetData` /
// `indexing.AssetStore` / `indexing.ValidatePoint` are all preserved.
package indexing

import (
	"context"

	"go.uber.org/zap"

	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// IndexWriter handles point upsert and deletion for Qdrant collections.
// It implements clipindexer.VectorStoreIndexer and
// outbox.VectorPointDeleter (PR 4, June 2026,
// refactor/single-qdrant-runtime — the previous local
// `qdrant.QdrantDeleter` interface was deleted in favour of the
// canonical application-layer port).
//
// SINGLE TRUTH (Blocco 4e, July 2026): IndexWriter is the ONLY
// code path that calls transport.Client.UpsertPoints / transport.Client.DeletePoints.
// All Qdrant writes MUST route through this type. The outbox
// (Dispatcher.EnqueueAndIndex → outbox event → IndexWriter) is
// the canonical write trigger; ReindexAll is the documented admin
// bypass for blue-green operations. No other type in the codebase
// may call transport.Client write methods directly.
//
// All writes go through the runtime alias so callers never need to know
// the physical collection name.
type IndexWriter struct {
	client     *transport.Client
	idxSchema  *schema.IndexSchema
	mapper     *PayloadMapper
	log        *zap.Logger
	projection ProjectionWriter
}

// NewIndexWriter creates an IndexWriter.
//
// Compile-time assertions: IndexWriter satisfies the generic
// schema.IndexWriterPort (used by clipindexer) and the application-layer
// outbox.VectorPointDeleter port (consumed by IndexDeleteHandler).
// PR 4 consolidated the previously-duplicated `QdrantDeleter`
// interfaces (one in infra, one in outbox) into the single
// outbox.VectorPointDeleter port that lives in
// internal/capabilities/jobs/outbox/ports.go per AGENTS.md Pattern 0.
var (
	_ schema.IndexWriterPort        = (*IndexWriter)(nil)
	_ jobsoutbox.VectorPointDeleter = (*IndexWriter)(nil)
)

// NewIndexWriter creates an IndexWriter.
//
// Parameter naming: `idxSchema` (NOT `schema`) to avoid shadowing the
// imported `github.com/.../platform/qdrant/schema` package inside
// this function body. Renamed as part of the QDRANT-001 Check 2 fix
// (July 2026); the earlier `schema *schema.IndexSchema` parameter silently
// re-routed any future `schema.X` reference to a method call on the
// local variable. Callers pass positionally, so this rename is
// non-breaking at the call-site layer.
func NewIndexWriter(client *transport.Client, idxSchema *schema.IndexSchema, mapper *PayloadMapper, log *zap.Logger) *IndexWriter {
	return &IndexWriter{
		client:     client,
		idxSchema:  idxSchema,
		mapper:     mapper,
		log:        log,
		projection: NewTransportProjectionWriter(client),
	}
}

// UpsertFromClip reads a single clip's data from the asset store and upserts it
// to Qdrant. Implements clipindexer.VectorStoreIndexer.
func (w *IndexWriter) UpsertFromClip(ctx context.Context, clipID string) error {
	return w.UpsertFromClips(ctx, []string{clipID})
}

// newPartialUpsertError constructs a *transport.PartialUpsertError and pre-computes
// Retryable by classifying each failure through the canonical
// qdrant.transport.IsRetryable helper. Centralised here so the retry decision is
// made once at construction time rather than re-derived by every caller.
func newPartialUpsertError(successIDs []string, failures []transport.AssetUpsertFailure) *transport.PartialUpsertError {
	retryable := false
	for i := range failures {
		if transport.IsRetryable(failures[i].Cause) {
			retryable = true
			break
		}
	}
	return &transport.PartialUpsertError{
		SuccessfulIDs: successIDs,
		Failures:      failures,
		Retryable:     retryable,
	}
}
