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

	if err := c.SetIndexStateTx(ctx, tx, assetID, asset.StateDiscovered); err != nil {
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
