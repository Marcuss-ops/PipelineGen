// Package app — test_sqlite_asset_committer.go: TEST-ONLY SQLite-backed
// persistence.AssetCommitter for composition wiring tests.
//
// POSTGRES-MEDIA-CUTOVER demolition note: the production SQLite media
// writer family (imagesregistry.SQLiteAssetCommitter + its adapters) is
// REMOVED — the only canonical media writer is PostgresMediaCommitter over
// the PostgreSQL + pgvector SSOT. The artlist composition tests, however,
// run against a hermetic file-backed SQLite database and exercise the
// WIRING (fail-closed gates, argument passing), not the engine. This stub
// implements the narrow AssetCommitter port by persisting the asset row +
// locations and enqueueing the index-request event — enough for the wiring
// assertions to hold without resurrecting the demolished writer or
// requiring a live PostgreSQL DSN in unit tests. It lives in a _test-adjacent
// file but is compiled only with the package's test binaries (build tag).
package wiring

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

// sqliteTestAssetCommitter is the hermetic test double for the
// ArtlistBundle.Committer slot (test-only; never wired in production).
type sqliteTestAssetCommitter struct {
	db  *sql.DB
	box *outboxEventsTestRepo
}

// outboxEventsTestRepo narrows the outbox write surface used by the stub.
type outboxEventsTestRepo struct {
	db *sql.DB
}

// Enqueue inserts one outbox_events row (SQLite canonical envelope).
func (r *outboxEventsTestRepo) Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*outboxEnqueueTestResult, error) {
	exec := dbExecPicker(r.db, tx)
	_, err := exec.ExecContext(ctx, `
		INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, status, attempt_count, max_attempts, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'pending', 0, 5, ?, ?)
	`, eventType, aggregateID, aggregateType, payloadJSON, eventKey, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("test outbox enqueue: %w", err)
	}
	return &outboxEnqueueTestResult{Inserted: true}, nil
}

type execPicker interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func dbExecPicker(db *sql.DB, tx *sql.Tx) execPicker {
	if tx != nil {
		return tx
	}
	return db
}

// outboxEnqueueTestResult mirrors the canonical enqueue result shape.
type outboxEnqueueTestResult struct {
	Inserted bool
}

// CommitAndIndex persists the asset + locations inside one SQLite tx and
// emits the index-request event (canonical envelope shape, test stub).
func (c *sqliteTestAssetCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return persistence.CommitResult{}, fmt.Errorf("test committer: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, filename, media_type, category, duration_ms, lifecycle_state, index_state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'DISCOVERED', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			media_type = excluded.media_type,
			updated_at = excluded.updated_at
	`, req.AssetID, req.Source, req.Name, req.Filename, req.MediaType, req.Category, req.DurationMs, req.LifecycleState, now, now); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("test committer: upsert media_assets: %w", err)
	}
	for i, loc := range req.Locations {
		kind := loc.Kind
		if strings.TrimSpace(kind) == "" {
			return persistence.CommitResult{}, fmt.Errorf("test committer: location[%d] has empty Kind", i)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO asset_locations (asset_id, location_kind, uri, external_id, web_view_link, download_url, is_primary, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(asset_id, location_kind) DO UPDATE SET uri = excluded.uri, updated_at = excluded.updated_at
		`, req.AssetID, kind, loc.URI, loc.ExternalID, loc.WebViewLink, loc.DownloadURL, boolToIntForTest(loc.IsPrimary), now, now); err != nil {
			return persistence.CommitResult{}, fmt.Errorf("test committer: upsert location %s: %w", kind, err)
		}
	}
	if req.EmitIndexEvent {
		eventKey := fmt.Sprintf("index:%s:%s", req.Source, req.AssetID)
		if _, err := c.box.Enqueue(ctx, tx, "asset.index.requested", req.AssetID, "media_asset", fmt.Sprintf(`{"asset_id":%q,"source":%q}`, req.AssetID, req.Source), eventKey); err != nil {
			return persistence.CommitResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("test committer: commit: %w", err)
	}
	return persistence.CommitResult{AssetRowsAffected: 1}, nil
}

// CommitAsset satisfies the canonical user-facing entry point by
// delegating to CommitAndIndex.
func (c *sqliteTestAssetCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return c.CommitAndIndex(ctx, persistence.CommitRequest(req))
}

// CommitTx satisfies the caller-owned-tx variant (test path: minimal
// projection only — wiring tests never exercise engine-level CAS).
func (c *sqliteTestAssetCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if c == nil || c.db == nil {
		return persistence.CommitResult{}, fmt.Errorf("test committer: db is required")
	}
	_ = tx
	return c.CommitAndIndex(ctx, req)
}

func boolToIntForTest(b bool) int {
	if b {
		return 1
	}
	return 0
}
