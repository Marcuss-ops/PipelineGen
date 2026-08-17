// Package assets — source_version.go is the canonical SQL-side reader
// for the media_assets.source_version fingerprint.
//
// History (PR 11 follow-up, June 2026):
//
//	The producer-side path
//	(cmd/admin/reconcile_qdrant.go::outboxRepairAdapter.EnqueueReindex)
//	and the consumer-side path
//	(internal/application/jobs/outbox/indexing.go::readSourceVersion)
//	both walked the same JSON/column priority chain in parallel.
//	The producer used an inline SQLite COALESCE over
//	metadata_json.$.content_hash → metadata_json.$.file_hash →
//	media_assets.file_hash. The consumer walked an *asset.Asset
//	via GetMetadataString/FileHash() accessors — but
//	Asset.FileHash() is defined as m.GetMetadataString("file_hash"),
//	which is the SAME priority position as the JSON file_hash tier,
//	not a real third tier. The two implementations DRIFTED silently:
//	the producer actually checks the legacy top-level column while
//	the consumer collapses to a 2-tier walk.
//
//	This helper is the single source of truth. Both call sites import
//	it. Priority changes MUST happen here and propagate to both
//	sides automatically. The corresponding test
//	(source_version_test.go) pins all four priority slots plus the
//	sql.ErrNoRows sentinel so future drift is structurally
//	impossible.
package assets

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// QueryRowContexter is the structural surface flowing through this
// helper. Both *sql.DB and *sql.Tx satisfy this interface — that
// symmetry is the design centrepiece: the producer-side caller
// (EnqueueReindex) passes a *sql.Tx (atomic with the dual-store
// classification + outbox INSERT) and the consumer-side caller
// (IndexingHandler via *assets.ClipsRepository.SourceVersionFor)
// passes a *sql.DB (the worker pool's ambient handle) without any
// adapter or wrapper layer between them.
type QueryRowContexter interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SourceVersionFor returns the canonical source_version fingerprint
// for the supplied assetID by walking a deterministic priority list
// of fields in a single SQLite COALESCE expression.
//
// Priority invariants — these are load-bearing, do NOT reorder:
//
//  0. metadata_json.$.index_revision is the indexable-snapshot
//     fingerprint (folds byte identity + text tracks + metadata). It
//     is the value the supersede gate must compare against the event's
//     source_version, and it is what changes when transcripts/tags/
//     metadata change WITHOUT corrupting byte identity (godlike/06:
//     content_sha256 vs index_revision). Present only for rows written
//     after the content_sha256/index_revision separation (Aug 2026);
//     legacy rows fall through to the byte-identity chain below.
//
//  1. metadata_json.$.content_hash is the dispatcher-aware BYTE
//     identity boundary (the Dispatcher writes it atomically inside
//     the same tx as the outbox event publish, so producer and worker
//     agree on the fingerprint). When MULTIPLE keys are populated and
//     DISAGREE, content_hash wins over the remaining legacy tiers —
//     it represents the same write boundary as the event's
//     source_version stamp so the two are guaranteed to be consistent
//     within a single ingest.
//
//  2. metadata_json.$.file_hash is a fallback for non-dispatcher
//     ingest paths (legacy CLI direct upserts, older YouTube sync
//     paths) where the Dispatcher was not in the write path.
//
//  3. media_assets.file_hash is the legacy top-level column,
//     populated by pre-metadata-json ingest tooling. This tier is
//     structurally necessary because some legacy rows predate the
//     metadata_json column.
//
// Returns:
//
//   - (value, nil)            — a fingerprint was found (or all
//     tiers fell through to "").
//   - ("", sql.ErrNoRows)     — assetID has no media_assets row.
//     Distinct empty so the producer can
//     wrap a fail-closed diagnostic and the
//     consumer can fall through to allow
//     IndexClip to short-circuit on its own.
//   - ("", err)               — generic SQL failure (lock, I/O,
//     schema drift). Retryable on the
//     consumer side, fail-closed on the
//     producer side.
//
// DRY rationale (PR 11 follow-up, June 2026):
//
//	The previous dual implementation across cmd/admin + outbox/handler
//	drift-fixed this priority list AFTER every data-backfill incident.
//	The consolidated helper is greppable, single-source-of-truth, and
//	tested against the legacy top-level column so backfilled rows
//	surface immediately in the regression suite.
func SourceVersionFor(ctx context.Context, q QueryRowContexter, assetID string) (string, error) {
	if q == nil {
		return "", errNilQuerier()
	}
	if assetID == "" {
		return "", errEmptyAssetID()
	}
	// Single COALESCE expression walks the priority chain in one
	// query. JSON-extract returns NULL when the slot is absent;
	// the bare column reference reads media_assets.file_hash at
	// the top level (so the legacy 3rd tier is honoured even for
	// rows pre-dating the metadata_json column). Tier 0
	// (index_revision) is the canonical SSOT field name
	// (mediaregistry.IndexRevisionField), injected so a future
	// rename has one owner.
	var v string
	err := q.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(
			json_extract(metadata_json, '$.%s'),
			json_extract(metadata_json, '$.content_hash'),
			json_extract(metadata_json, '$.file_hash'),
			file_hash,
			''
		) FROM media_assets WHERE id = ?
	`, mediaregistry.IndexRevisionField), assetID).Scan(&v)
	return v, err
}

// errNilQuerier / errEmptyAssetID are local helper errors. They surface
// only on a malformed caller configuration — production wiring
// guarantees q != nil and assetID != "" so these branches are
// effectively unreachable in deployment but the explicit error
// surfaces misuse at the first test or local-dev run instead of
// triggering a generic SQL NULL-pointer stack trace.
type callerError string

func (e callerError) Error() string { return string(e) }

func errNilQuerier() error {
	return callerError("assets.SourceVersionFor: querier must not be nil (production wiring guarantees *sql.DB or *sql.Tx)")
}

func errEmptyAssetID() error {
	return callerError("assets.SourceVersionFor: assetID must not be empty")
}
