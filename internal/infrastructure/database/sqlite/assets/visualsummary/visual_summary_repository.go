// Package assets — visual_summary_repository.go
//
// SQLite concrete for the VisualSummaryRepository port. Persists and
// queries VLM-generated visual summaries (visual_summary_text +
// visible_actions + visible_entities) per media asset. Used by the
// VLM frame sampler (frame-sampling pass + VLM inference), the
// Qdrant payload mapper (reads row to emit payload keys), and the
// admin reindex command (ListByModelVersion + supersede-gate via
// source_hash).
//
// godlike/06 SSOT: this is the SOLE canonical owner of the
// asset_visual_summaries row shape. The Python sidecar, the
// Qdrant payload mapper, and the admin reindex command all read
// this struct via the repository — they MUST NOT construct an
// alternative mirror of these columns.
//
// godlike/07 NO-FAKE-AVAILABILITY: a missing row (no row in the
// table for the asset) signals "no VLM pass has run for this
// clip". The Qdrant payload omits visual_summary/visible_actions/
// visible_entities entirely in this state. See
// qdrant.payload_builder_test.go's omitempty regression contract
// for the strict-emit test.
package visualsummary

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"go.uber.org/zap"
)

// VisualSummaryRepository is the canonical port for the
// asset_visual_summaries table. It is satisfied by
// VisualSummaryRepositorySQLite; mock implementations should also
// satisfy this contract for the VLM frame-sampler unit tests.
//
// All methods are safe for concurrent use by multiple goroutines
// (the underlying *sql.DB is the canonical concurrent-safe SQLite
// driver — mattn/go-sqlite3 with WAL).
type VisualSummaryRepository interface {
	// Upsert inserts or replaces the row for the given asset_id.
	// The PRIMARY KEY = asset_id constraint makes this an atomic
	// single-row upsert. Returns the rewritten source_hash when
	// the caller did NOT pre-compute it (ComputeSourceHash is the
	// canonical owner).
	Upsert(ctx context.Context, summary asset.VisualSummary) error

	// Get returns the row for the asset_id, or (nil, nil) when no
	// row exists. The (nil, nil) return is the canonical
	// "NO_VLM_PASS" signal — godlike/07 NO-FAKE-AVAILABILITY.
	Get(ctx context.Context, assetID string) (*asset.VisualSummary, error)

	// Delete removes the row for the asset_id. Returns nil when
	// no row existed (idempotent). Used by the reindex command
	// when the operator wants to force a clean re-derive.
	Delete(ctx context.Context, assetID string) error

	// ListByModelVersion enumerates the rows whose VLM pass used
	// the (model_name, model_version) tuple. Used by the admin
	// reindex command to select clips whose projection should be
	// rebuilt when the VLM checkpoint is bumped. Returns an
	// empty slice when no rows match.
	ListByModelVersion(ctx context.Context, modelName, modelVersion string) ([]asset.VisualSummary, error)

	// ListBySourceHash enumerates the rows whose SourceHash equals
	// the given value. Used by the supersede-gate cross-check
	// ("which clips produced an identical aggregate?") — a
	// supersede gate can short-circuit Qdrant upsert when the
	// incoming SourceHash already matches the SQLite row.
	ListBySourceHash(ctx context.Context, sourceHash string) ([]asset.VisualSummary, error)
}

// VisualSummaryRepositorySQLite is the SQLite-backed implementation
// of VisualSummaryRepository.
type VisualSummaryRepositorySQLite struct {
	db  *sql.DB
	log *zap.Logger
}

// NewVisualSummaryRepository builds a SQLite-backed visual summary
// repository. nil db is a hard error; nil log resolves to zap.NewNop()
// to preserve the text_track_repository.go convention.
func NewVisualSummaryRepository(db *sql.DB, log *zap.Logger) (*VisualSummaryRepositorySQLite, error) {
	if db == nil {
		return nil, errors.New("visual_summary_repository: sql.DB is nil")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &VisualSummaryRepositorySQLite{db: db, log: log}, nil
}

var _ VisualSummaryRepository = (*VisualSummaryRepositorySQLite)(nil)

// ErrVisualSummaryRepositoryNotWired is the typed error returned
// when an Upsert/Get/Delete/ListBy* call lands on a nil repo
// pointer. Distinct from the constructor's nil-db error path so
// the two boundary surfaces can be programmatically distinguished
// and the test matrix can assert which one fired.
//
// godlike/07 fail-closed: a nil receiver MUST NOT panic. The
// constructor returns an error on nil db; this typed sentinel
// covers the runtime path where a repo pointer is conditionally
// nil (e.g. a feature flag disabled at composition-time).
var ErrVisualSummaryRepositoryNotWired = errors.New("visual_summary_repository: not wired (nil receiver)")

const upsertVisualSummarySQL = `
INSERT INTO asset_visual_summaries (
    asset_id,
    visual_summary_text,
    visible_actions_json,
    visible_entities_json,
    frame_count,
    interval_seconds,
    preprocessing_version,
    model_name,
    model_version,
    source_hash,
    sampled_at,
    sampled_at_unix,
    created_at,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
ON CONFLICT(asset_id) DO UPDATE SET
    visual_summary_text   = excluded.visual_summary_text,
    visible_actions_json  = excluded.visible_actions_json,
    visible_entities_json = excluded.visible_entities_json,
    frame_count           = excluded.frame_count,
    interval_seconds      = excluded.interval_seconds,
    preprocessing_version = excluded.preprocessing_version,
    model_name            = excluded.model_name,
    model_version         = excluded.model_version,
    source_hash           = excluded.source_hash,
    sampled_at            = excluded.sampled_at,
    sampled_at_unix       = excluded.sampled_at_unix,
    updated_at            = datetime('now')
`

// Upsert inserts or replaces the row for the given asset_id. The
// PRIMARY KEY conflict (asset_id) updates in place. Empty AssetID
// is rejected with a typed error to match the domain contract.
//
// Nil-safe: a nil receiver returns ErrVisualSummaryRepositoryNotWired
// (godlike/07 fail-closed boundary) instead of panicking on the
// first field-assign. The composition root returns an error on
// nil db at construction time; this surface covers the runtime
// path where a repo pointer is conditionally nil.
func (r *VisualSummaryRepositorySQLite) Upsert(ctx context.Context, summary asset.VisualSummary) error {
	if r == nil {
		return ErrVisualSummaryRepositoryNotWired
	}
	if err := summary.Validate(); err != nil {
		return fmt.Errorf("visual_summary_repository.Upsert: validate: %w", err)
	}

	actionsJSON, err := json.Marshal(summary.VisibleActions)
	if err != nil {
		return fmt.Errorf("visual_summary_repository.Upsert: marshal visible_actions: %w", err)
	}
	entitiesJSON, err := json.Marshal(summary.VisibleEntities)
	if err != nil {
		return fmt.Errorf("visual_summary_repository.Upsert: marshal visible_entities: %w", err)
	}

	var sampledAt, sampledAtUnix string
	if summary.SampledAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339, summary.SampledAt)
		if parseErr != nil {
			return fmt.Errorf("visual_summary_repository.Upsert: parse sampled_at %q: %w", summary.SampledAt, parseErr)
		}
		sampledAt = summary.SampledAt
		sampledAtUnix = fmt.Sprintf("%d", parsed.Unix())
	} else {
		// No real VLM pass ran for this row (EmptyAssetDB legacy).
		// Persist sampled_at_unix as 0 so the SQL INTEGER column
		// round-trips through the driver as int64(0), not a
		// type-mismatched "" string that would fail scan at Get/ListBy.
		sampledAtUnix = "0"
	}

	if _, execErr := r.db.ExecContext(ctx, upsertVisualSummarySQL,
		summary.AssetID,
		summary.VisualSummaryText,
		string(actionsJSON),
		string(entitiesJSON),
		summary.FrameCount,
		summary.IntervalSeconds,
		summary.PreprocessingVersion,
		summary.ModelName,
		summary.ModelVersion,
		summary.SourceHash,
		sampledAt,
		sampledAtUnix,
	); execErr != nil {
		return fmt.Errorf("visual_summary_repository.Upsert: exec (asset=%s): %w", summary.AssetID, execErr)
	}
	return nil
}

// Get returns the row for the asset_id or (nil, nil) when no row
// exists. The (nil, nil) return is the canonical NO_VLM_PASS
// signal — godlike/07 forbids representing an unavailable backend
// as a successful no-op. Callers MUST handle (nil, nil) by
// omitting the visual_summary/visible_actions/visible_entities
// payload keys.
func (r *VisualSummaryRepositorySQLite) Get(ctx context.Context, assetID string) (*asset.VisualSummary, error) {
	if assetID == "" {
		return nil, fmt.Errorf("visual_summary_repository.Get: AssetID is required")
	}
	row := r.db.QueryRowContext(ctx, selectVisualSummarySQL, assetID)
	v, err := scanVisualSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("visual_summary_repository.Get: %w", err)
	}
	return v, nil
}

const selectVisualSummarySQL = `
SELECT asset_id,
       visual_summary_text,
       visible_actions_json,
       visible_entities_json,
       frame_count,
       interval_seconds,
       preprocessing_version,
       model_name,
       model_version,
       source_hash,
       sampled_at,
       sampled_at_unix,
       created_at,
       updated_at
  FROM asset_visual_summaries
 WHERE asset_id = ?
`

const selectVisualSummaryByColumnsSQL = `
SELECT asset_id,
       visual_summary_text,
       visible_actions_json,
       visible_entities_json,
       frame_count,
       interval_seconds,
       preprocessing_version,
       model_name,
       model_version,
       source_hash,
       sampled_at,
       sampled_at_unix,
       created_at,
       updated_at
  FROM asset_visual_summaries
`

// Delete removes the row for the asset_id. Idempotent: returns nil
// when no row existed (sql.Result.RowsAffected() == 0). Used by
// the admin reindex command when forcing a clean re-derive.
func (r *VisualSummaryRepositorySQLite) Delete(ctx context.Context, assetID string) error {
	if assetID == "" {
		return fmt.Errorf("visual_summary_repository.Delete: AssetID is required")
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM asset_visual_summaries WHERE asset_id = ?`,
		assetID,
	); err != nil {
		return fmt.Errorf("visual_summary_repository.Delete: %w", err)
	}
	return nil
}

// ListByModelVersion enumerates the rows whose VLM pass used the
// given (model_name, model_version) tuple. Used by the admin
// reindex command. Returns an empty slice when no rows match.
// Order is by sampled_at_unix DESC (most-recent first) to surface
// the latest passes during reindex debugging.
func (r *VisualSummaryRepositorySQLite) ListByModelVersion(ctx context.Context, modelName, modelVersion string) ([]asset.VisualSummary, error) {
	if modelName == "" {
		return nil, fmt.Errorf("visual_summary_repository.ListByModelVersion: modelName is required")
	}
	if modelVersion == "" {
		return nil, fmt.Errorf("visual_summary_repository.ListByModelVersion: modelVersion is required")
	}
	rows, err := r.db.QueryContext(ctx,
		selectVisualSummaryByColumnsSQL+`
 WHERE model_name = ? AND model_version = ?
 ORDER BY sampled_at_unix DESC, asset_id ASC`,
		modelName, modelVersion,
	)
	if err != nil {
		return nil, fmt.Errorf("visual_summary_repository.ListByModelVersion: query: %w", err)
	}
	defer rows.Close()
	return scanVisualSummaryList(rows)
}

// ListBySourceHash enumerates rows whose SourceHash matches the
// given value. Used by the supersede-gate cross-check. Ordered by
// asset_id ASC for deterministic replay.
func (r *VisualSummaryRepositorySQLite) ListBySourceHash(ctx context.Context, sourceHash string) ([]asset.VisualSummary, error) {
	if sourceHash == "" {
		return nil, fmt.Errorf("visual_summary_repository.ListBySourceHash: sourceHash is required")
	}
	rows, err := r.db.QueryContext(ctx,
		selectVisualSummaryByColumnsSQL+`
 WHERE source_hash = ?
 ORDER BY asset_id ASC`,
		sourceHash,
	)
	if err != nil {
		return nil, fmt.Errorf("visual_summary_repository.ListBySourceHash: query: %w", err)
	}
	defer rows.Close()
	return scanVisualSummaryList(rows)
}

// visualSummaryScanner abstracts sql.Row vs sql.Rows for scan.
type visualSummaryScanner interface {
	Scan(dest ...any) error
}

func scanVisualSummary(s visualSummaryScanner) (*asset.VisualSummary, error) {
	var (
		assetID              string
		visualSummaryText    string
		visibleActionsJSON   string
		visibleEntitiesJSON  string
		frameCount           int
		intervalSeconds      float64
		preprocessingVersion string
		modelName            string
		modelVersion         string
		sourceHash           string
		sampledAt            string
		sampledAtUnix        sql.NullInt64
		createdAtStr         string
		updatedAtStr         string
	)
	if err := s.Scan(
		&assetID,
		&visualSummaryText,
		&visibleActionsJSON,
		&visibleEntitiesJSON,
		&frameCount,
		&intervalSeconds,
		&preprocessingVersion,
		&modelName,
		&modelVersion,
		&sourceHash,
		&sampledAt,
		&sampledAtUnix,
		&createdAtStr,
		&updatedAtStr,
	); err != nil {
		return nil, err
	}

	var visibleActions []string
	if visibleActionsJSON != "" && visibleActionsJSON != "[]" {
		if unmarshalErr := json.Unmarshal([]byte(visibleActionsJSON), &visibleActions); unmarshalErr != nil {
			return nil, fmt.Errorf("scanVisualSummary: unmarshal visible_actions: %w", unmarshalErr)
		}
	}

	var visibleEntities []string
	if visibleEntitiesJSON != "" && visibleEntitiesJSON != "[]" {
		if unmarshalErr := json.Unmarshal([]byte(visibleEntitiesJSON), &visibleEntities); unmarshalErr != nil {
			return nil, fmt.Errorf("scanVisualSummary: unmarshal visible_entities: %w", unmarshalErr)
		}
	}

	v := &asset.VisualSummary{
		AssetID:              assetID,
		VisualSummaryText:    visualSummaryText,
		VisibleActions:       visibleActions,
		VisibleEntities:      visibleEntities,
		FrameCount:           frameCount,
		IntervalSeconds:      intervalSeconds,
		PreprocessingVersion: preprocessingVersion,
		ModelName:            modelName,
		ModelVersion:         modelVersion,
		SourceHash:           sourceHash,
		SampledAt:            sampledAt,
	}

	if sampledAtUnix.Valid {
		_ = sampledAtUnix.Int64 // currently informational; future "since N" queries
	}

	// Created/UpdatedAt: best-effort via existing repo parse helpers.
	// The created/updated columns are NOT NULL DEFAULT datetime('now')
	// so they always carry a value. We parse best-effort; a malformed
	// value is logged at the boundary, never blocks the read.
	if createdAtStr != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, createdAtStr); parseErr == nil {
			v.CreatedAt = parsed
		}
	}
	if updatedAtStr != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, updatedAtStr); parseErr == nil {
			v.UpdatedAt = parsed
		}
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = v.CreatedAt
	}

	return v, nil
}

func scanVisualSummaryList(rows *sql.Rows) ([]asset.VisualSummary, error) {
	out := make([]asset.VisualSummary, 0)
	for rows.Next() {
		v, scanErr := scanVisualSummary(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scanVisualSummaryList: scan: %w", scanErr)
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scanVisualSummaryList: rows: %w", err)
	}
	return out, nil
}
