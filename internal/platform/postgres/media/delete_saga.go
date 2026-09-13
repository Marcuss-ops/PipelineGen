// Package media — delete_saga.go (MEDIA-SSOT P0-2, September 2026).
//
// The delete/restore saga's DURABLE STATE moves to the PostgreSQL media
// SSOT: the lifecycle stamps (DELETE_REQUESTED / DRIVE_DELETE_PENDING /
// DELETED / restore) and the saga outbox emissions
// (asset.drive.delete_requested / asset.index.delete_requested /
// asset.index.restore_requested) commit in ONE PG transaction against
// media_assets + pg outbox_events.
//
// The Drive worker (DriveDeleteHandler) stays where it is — it performs
// the Drive API side effect — but its state reads/writes and its
// advance+emit hops route through the ports implemented here so the saga
// state no longer diverges across engines.
//
// Rationale (P0 cross-engine partial commit): the previous SQLite
// dispatcher_delete.go wrote lifecycle_state + outbox_events under the
// SQLite engine while the asset row itself lives in PostgreSQL. A PG
// authoritative asset could be flagged DELETE_REQUESTED in SQLite (or
// vice versa) and the two engines could diverge silently.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// PG outbox event types for the delete/restore saga (byte-identical with
// the SQLite outboxevents constants — one fact family, two adapters).
// OWNED by internal/kernel/event (godlike/06); compile-time re-exports.
const (
	EventAssetDriveDeleteRequested  = event.AssetDriveDeleteRequested
	EventAssetIndexDeleteRequested  = event.AssetIndexDeleteRequested
	EventAssetIndexRestoreRequested = event.AssetIndexRestoreRequested
)

// DeleteSagaStateReader reads the current lifecycle state of an asset
// from the PG media SSOT (narrow port for the DriveDeleteHandler
// pre-flight check).
type DeleteSagaStateReader interface {
	GetLifecycleState(ctx context.Context, assetID string) (asset.LifecycleState, error)
}

// GetLifecycleState implements DeleteSagaStateReader.
func (c *PostgresMediaCommitter) GetLifecycleState(ctx context.Context, assetID string) (asset.LifecycleState, error) {
	if c == nil || c.db == nil {
		return "", errors.New("media delete saga: committer not wired")
	}
	var state string
	err := c.db.QueryRowContext(ctx,
		`SELECT lifecycle_state FROM media_assets WHERE id = $1`, assetID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", asset.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("media delete saga: read lifecycle_state %s: %w", assetID, err)
	}
	return asset.LifecycleState(state), nil
}

// GetClip implements the jobs.LifecycleStateReader contract surface at the
// PG boundary: it returns the minimal *asset.Asset view the DriveDeleteHandler
// needs — ID, LifecycleState and the Drive identifiers used by
// extractDriveFileID (drive_file_id column first, then the links).
func (c *PostgresMediaCommitter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("media delete saga: committer not wired")
	}
	var (
		state, driveFileID, driveLink, downloadLink string
	)
	err := c.db.QueryRowContext(ctx, `
		SELECT COALESCE(lifecycle_state,''), COALESCE(drive_file_id,''),
		       COALESCE(drive_link,''), COALESCE(download_link,'')
		FROM media_assets WHERE id = $1`, id).
		Scan(&state, &driveFileID, &driveLink, &downloadLink)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media delete saga: GetClip %s: %w", id, err)
	}
	clip := &asset.Asset{ID: id, LifecycleState: asset.LifecycleState(state)}
	if driveFileID != "" {
		clip.SetMetadataString("drive_file_id", driveFileID)
	}
	if driveLink != "" {
		clip.SetMetadataString("drive_link", driveLink)
	}
	if downloadLink != "" {
		clip.SetMetadataString("download_link", downloadLink)
	}
	return clip, nil
}

// GetClipByDriveFileID resolves the minimal deletion view of an asset from
// its Google Drive identity. It mirrors the legacy SQLite reader
// (imagesregistry.AssetStoreSQLite.GetClipByDriveFileID) field-for-field:
// drive_file_id, drive_link and download_link are matched with LIKE
// containment, and a miss returns (nil, nil) — never a fake match.
func (c *PostgresMediaCommitter) GetClipByDriveFileID(ctx context.Context, fileID string) (*asset.Asset, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("media delete saga: committer not wired")
	}
	trimmed := strings.TrimSpace(fileID)
	if trimmed == "" {
		return nil, errors.New("drive file id is required")
	}
	pattern := "%" + trimmed + "%"
	var (
		id, state, driveFileID, driveLink, downloadLink string
	)
	err := c.db.QueryRowContext(ctx, `
		SELECT COALESCE(id,''), COALESCE(lifecycle_state,''), COALESCE(drive_file_id,''),
		       COALESCE(drive_link,''), COALESCE(download_link,'')
		FROM media_assets
		WHERE drive_file_id LIKE $1 OR drive_link LIKE $1 OR download_link LIKE $1
		LIMIT 1`, pattern).
		Scan(&id, &state, &driveFileID, &driveLink, &downloadLink)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("media delete saga: GetClipByDriveFileID %s: %w", fileID, err)
	}
	clip := &asset.Asset{ID: id, LifecycleState: asset.LifecycleState(state)}
	if driveFileID != "" {
		clip.SetMetadataString("drive_file_id", driveFileID)
	}
	if driveLink != "" {
		clip.SetMetadataString("drive_link", driveLink)
	}
	if downloadLink != "" {
		clip.SetMetadataString("download_link", downloadLink)
	}
	return clip, nil
}

// SoftDeleteAsset retires the asset row in the PostgreSQL media SSOT: it
// stamps lifecycle_state=DELETED and deleted_at with the same timestamp. It is
// the exact mirror of imagesregistry.ClipsRepository.SoftDelete (which
// delegates to UpdateMediaAssetLifecycle with that same pair), so the
// index-delete hop of the deletion saga behaves identically on both engines.
// Consumed through the jobs.AssetDeleter port.
func (c *PostgresMediaCommitter) SoftDeleteAsset(ctx context.Context, assetID string) error {
	if c == nil || c.db == nil {
		return errors.New("media delete saga: committer not wired")
	}
	if strings.TrimSpace(assetID) == "" {
		return errors.New("media delete saga: asset id is required")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	return c.UpdateLifecycle(ctx, assetID, string(asset.StateDeleted), nowStr, nowStr)
}

// DeleteAssetIndexPoints removes the pgvector index surfaces for the supplied
// assets. This is the PostgreSQL media plane's equivalent of the retired
// Qdrant DeletePoints call inside IndexDeleteHandler: media_embeddings is the
// ONLY vector index for the media domain, so deleting those rows IS deleting
// the asset's index points (the FK cascades from media_assets, but the explicit
// delete is what makes the parallel-index semantics observable and idempotent).
//
// Scoped to the index plane only: media_asset_features is enrichment, not the
// search index, and is deliberately left untouched. A zero-row delete is
// success (idempotent re-run), never an error.
func (c *PostgresMediaCommitter) DeleteAssetIndexPoints(ctx context.Context, assetIDs []string) error {
	if c == nil || c.db == nil {
		return errors.New("media delete saga: committer not wired")
	}
	ids := dedupeHydrationIDs(assetIDs)
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	if _, err := c.db.ExecContext(ctx,
		`DELETE FROM media_embeddings WHERE asset_id IN (`+strings.Join(placeholders, ", ")+`)`,
		args...); err != nil {
		return fmt.Errorf("media delete saga: delete index points for %d asset(s): %w", len(ids), err)
	}
	return nil
}

// SetLifecycleState stamps lifecycle_state (no deletion-chain guard —
// used by the DriveDeleteHandler visibility stamp DRIVE_DELETE_PENDING
// and the terminal DELETED flip).
func (c *PostgresMediaCommitter) SetLifecycleState(ctx context.Context, assetID string, state asset.LifecycleState) error {
	return c.UpdateLifecycle(ctx, assetID, string(state), "", timeutil.FormatRFC3339(time.Now()))
}

// SetLifecycleStateIfNotInDeletionChain stamps lifecycle_state only when
// the row is NOT already inside a deletion chain (idempotency envelope at
// the state-machine layer). PG mirror of
// imagesregistry.UpdateMediaAssetLifecycleIfNotInDeletionChain.
// Returns the number of rows affected.
func (c *PostgresMediaCommitter) SetLifecycleStateIfNotInDeletionChain(ctx context.Context, assetID string, state asset.LifecycleState) (int64, error) {
	if c == nil || c.db == nil {
		return 0, errors.New("media delete saga: committer not wired")
	}
	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := c.db.ExecContext(ctx, `
		UPDATE media_assets SET lifecycle_state = $1, updated_at = $2, updated_at_ts = NULLIF($2, '')::timestamptz
		WHERE id = $3
		  AND lifecycle_state NOT IN ('DELETE_REQUESTED', 'DELETE_PENDING', 'DRIVE_DELETE_PENDING', 'INDEX_DELETE_PENDING', 'DELETED')`,
		string(state), nowStr, assetID)
	if err != nil {
		return 0, fmt.Errorf("media delete saga: stamp lifecycle_state=%s %s: %w", state, assetID, err)
	}
	return res.RowsAffected()
}

// setLifecycleStateIfNotInDeletionChainTx stamps lifecycle_state using the
// caller-owned PostgreSQL transaction. Saga state and its outbox event must
// never cross a transaction boundary.
func (c *PostgresMediaCommitter) setLifecycleStateIfNotInDeletionChainTx(ctx context.Context, tx *sql.Tx, assetID string, state asset.LifecycleState) (int64, error) {
	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets SET lifecycle_state = $1, updated_at = $2, updated_at_ts = NULLIF($2, '')::timestamptz
		WHERE id = $3
		  AND lifecycle_state NOT IN ('DELETE_REQUESTED', 'DELETE_PENDING', 'DRIVE_DELETE_PENDING', 'INDEX_DELETE_PENDING', 'DELETED')`,
		string(state), nowStr, assetID)
	if err != nil {
		return 0, fmt.Errorf("media delete saga: stamp lifecycle_state=%s %s: %w", state, assetID, err)
	}
	return res.RowsAffected()
}

// against the PG media SSOT — ONE transaction:
//
//	BEGIN (PG)
//	  UPDATE media_assets SET lifecycle_state='DELETE_REQUESTED'
//	    WHERE id=$1 AND lifecycle_state NOT IN (deletion chain)
//	  INSERT INTO outbox_events (asset.drive.delete_requested)
//	COMMIT
//
// The payload envelope is byte-compatible with the SQLite
// asset.drive.delete_requested.v1 envelope so the existing
// DriveDeleteHandler consumes it unchanged.
func (c *PostgresMediaCommitter) EnqueueDriveDelete(ctx context.Context, assetID string, permanently bool) error {
	if c == nil || c.db == nil {
		return errors.New("media delete saga: committer not wired")
	}
	if assetID == "" {
		return errors.New("media delete saga: assetID is required")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media delete saga: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := c.setLifecycleStateIfNotInDeletionChainTx(ctx, tx, assetID, asset.StateDeleteRequested); err != nil {
		return err
	}

	payload, eventKey, err := buildPGDriveDeleteEnvelope(assetID, permanently)
	if err != nil {
		return err
	}
	if _, err := c.box.Enqueue(ctx, tx, EventAssetDriveDeleteRequested, assetID, "media_asset", payload, eventKey); err != nil {
		return fmt.Errorf("media delete saga: enqueue drive-delete event %s: %w", assetID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media delete saga: commit drive-delete: %w", err)
	}
	committed = true
	c.log.Debug("media delete saga: enqueued asset.drive.delete_requested (PG SSOT)",
		zap.String("asset_id", assetID), zap.Bool("permanently", permanently))
	return nil
}

// EnqueueIndexDelete re-emits an asset.index.delete_requested event
// WITHOUT advancing lifecycle state (reconciler recovery path), with the
// updated_at circuit-breaker re-stamp. ONE PG transaction.
func (c *PostgresMediaCommitter) EnqueueIndexDelete(ctx context.Context, assetID string) error {
	if c == nil || c.db == nil {
		return errors.New("media delete saga: committer not wired")
	}
	if assetID == "" {
		return errors.New("media delete saga: assetID is required")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media delete saga: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	nowStr := timeutil.FormatRFC3339(time.Now())
	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET updated_at = $1, updated_at_ts = NULLIF($1, '')::timestamptz WHERE id = $2`,
		nowStr, assetID); err != nil {
		return fmt.Errorf("media delete saga: re-stamp updated_at %s: %w", assetID, err)
	}

	payload, eventKey, err := buildPGIndexDeleteEnvelope(assetID)
	if err != nil {
		return err
	}
	if _, err := c.box.Enqueue(ctx, tx, EventAssetIndexDeleteRequested, assetID, "media_asset", payload, eventKey); err != nil {
		return fmt.Errorf("media delete saga: enqueue index-delete event %s: %w", assetID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media delete saga: commit index-delete: %w", err)
	}
	committed = true
	return nil
}

// EnqueueAndRestore performs the restore dispatch step against the PG
// media SSOT — ONE transaction:
//
//	BEGIN (PG)
//	  UPDATE media_assets SET index_state='PENDING' (DISCOVERED)
//	  INSERT INTO outbox_events (asset.index.restore_requested)
//	COMMIT
func (c *PostgresMediaCommitter) EnqueueAndRestore(ctx context.Context, assetID string) error {
	if c == nil || c.db == nil {
		return errors.New("media delete saga: committer not wired")
	}
	if assetID == "" {
		return errors.New("media delete saga: assetID is required")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media delete saga: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := c.setIndexStateTx(ctx, tx, assetID, asset.StateDiscovered); err != nil {
		return fmt.Errorf("media delete saga: restore SetIndexStateTx=DISCOVERED %s: %w", assetID, err)
	}

	eventKey := "restore:" + assetID
	payload, err := json.Marshal(map[string]any{
		"schema_version":  event.AssetIndexRestoreRequestedV1Schema,
		"event_id":        eventKey,
		"asset_id":        assetID,
		"operation":       "RESTORE",
		"idempotency_key": eventKey,
		"requested_at":    timeutil.FormatRFC3339(time.Now()),
	})
	if err != nil {
		return fmt.Errorf("media delete saga: marshal restore payload %s: %w", assetID, err)
	}
	if _, err := c.box.Enqueue(ctx, tx, EventAssetIndexRestoreRequested, assetID, "media_asset", string(payload), eventKey); err != nil {
		return fmt.Errorf("media delete saga: enqueue restore event %s: %w", assetID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media delete saga: commit restore: %w", err)
	}
	committed = true
	return nil
}

// RestoreIndexedAsset performs the CONSUMER half of the restore saga step on
// the PostgreSQL media SSOT: it re-emits the canonical asset.index.requested
// envelope for the asset in ONE PostgreSQL transaction, so the pgvector index
// plane rebuilds the projection from scratch (the documented contract of
// asset.index.restore_requested: "outbox handler re-indexes from scratch").
//
// The producer half is EnqueueAndRestore, which already stamped
// index_state=DISCOVERED. A row that has vanished since is treated as an
// idempotent success — there is nothing left to index, and retrying cannot
// bring the row back.
//
// The deterministic event key comes from the canonical CommitIndexRequestTx
// helper plus the ":restore" suffix, so a redelivered restore_requested (whose
// own event key is the fixed "restore:<assetID>") collapses onto the same
// outbox row instead of duplicating index work.
func (c *PostgresMediaCommitter) RestoreIndexedAsset(ctx context.Context, assetID string) error {
	if c == nil || c.db == nil {
		return errors.New("media delete saga: committer not wired")
	}
	if strings.TrimSpace(assetID) == "" {
		return errors.New("media delete saga: asset id is required")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media restore: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var source, mediaType, sourceVersion, contentHash string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(source,''), COALESCE(media_type,''), COALESCE(source_version,''),
		       COALESCE(NULLIF(binary_sha256,''), NULLIF(content_sha256,''), COALESCE(legacy_file_md5,''))
		FROM media_assets WHERE id = $1`, assetID).
		Scan(&source, &mediaType, &sourceVersion, &contentHash)
	if errors.Is(err, sql.ErrNoRows) {
		// Row gone → idempotent success, nothing to re-index.
		return nil
	}
	if err != nil {
		return fmt.Errorf("media restore: read asset %s: %w", assetID, err)
	}
	if sourceVersion == "" {
		sourceVersion = contentHash
	}
	if sourceVersion == "" {
		return fmt.Errorf("media restore: asset %s has no source_version or content hash to key the index request", assetID)
	}

	if _, err := CommitIndexRequestTx(ctx, tx, c.box, IndexRequest{
		AssetID:        assetID,
		Source:         source,
		MediaType:      mediaType,
		SourceVersion:  sourceVersion,
		RequestedAt:    time.Now(),
		EventKeySuffix: ":restore",
	}); err != nil {
		return fmt.Errorf("media restore: re-emit index request %s: %w", assetID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media restore: commit %s: %w", assetID, err)
	}
	committed = true
	c.log.Debug("media delete saga: restore re-emitted asset.index.requested (PG SSOT)", zap.String("asset_id", assetID))
	return nil
}

// AdvanceAndEmit performs the compare-and-set lifecycle advance + next
// event emission atomically in the PG media SSOT. Implements the
// jobs.StateAdvancer contract surface at the PG boundary: the row is
// advanced ONLY when its current lifecycle_state equals expectedState
// (lease-fence equivalent at the state-machine layer).
func (c *PostgresMediaCommitter) AdvanceAndEmit(
	ctx context.Context,
	assetID string,
	expectedState, newState asset.LifecycleState,
	eventType string,
	payloadJSON []byte,
	eventKey string,
) error {
	if c == nil || c.db == nil {
		return errors.New("media delete saga: committer not wired")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media delete saga: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	nowStr := timeutil.FormatRFC3339(time.Now())
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets SET lifecycle_state = $1, updated_at = $2, updated_at_ts = NULLIF($2, '')::timestamptz
		WHERE id = $3 AND lifecycle_state = $4`,
		string(newState), nowStr, assetID, string(expectedState))
	if err != nil {
		return fmt.Errorf("media delete saga: advance lifecycle %s: %w", assetID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("media delete saga: advance lifecycle rows %s: %w", assetID, err)
	}
	if affected == 0 {
		return fmt.Errorf("media delete saga: advance lifecycle %s: expected_state %q not current (lease-fence mismatch)", assetID, expectedState)
	}

	if _, err := c.box.Enqueue(ctx, tx, eventType, assetID, "media_asset", string(payloadJSON), eventKey); err != nil {
		return fmt.Errorf("media delete saga: advance+emit %s: %w", assetID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media delete saga: commit advance+emit: %w", err)
	}
	committed = true
	return nil
}

// ── v1 envelope builders (byte-compatible with the SQLite envelopes) ────

func buildPGDriveDeleteEnvelope(assetID string, permanently bool) (string, string, error) {
	eventKey := "drive_delete:" + assetID
	if permanently {
		eventKey = "drive_delete_perm:" + assetID
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version":  event.AssetDriveDeleteRequestedV1Schema,
		"event_id":        eventKey,
		"asset_id":        assetID,
		"permanently":     permanently,
		"requested_at":    timeutil.FormatRFC3339(time.Now()),
		"idempotency_key": eventKey,
	})
	if err != nil {
		return "", "", fmt.Errorf("media delete saga: marshal drive-delete payload %s: %w", assetID, err)
	}
	return string(payload), eventKey, nil
}

func buildPGIndexDeleteEnvelope(assetID string) (string, string, error) {
	eventKey := "delete:" + assetID
	payload, err := json.Marshal(map[string]any{
		"schema_version":  event.AssetIndexDeleteRequestedV1Schema,
		"event_id":        eventKey,
		"asset_id":        assetID,
		"operation":       "DELETE",
		"idempotency_key": eventKey,
		"requested_at":    timeutil.FormatRFC3339(time.Now()),
	})
	if err != nil {
		return "", "", fmt.Errorf("media delete saga: marshal index-delete payload %s: %w", assetID, err)
	}
	return string(payload), eventKey, nil
}
