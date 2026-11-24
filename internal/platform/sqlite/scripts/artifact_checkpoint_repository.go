// artifact_checkpoint_repository.go — SQLite adapter for item 9's
// per-artifact checkpoint port.
//
// The adapter owns exactly three things the pure contract cannot:
//   - validating/serializing before SQL,
//   - computing payload_sha256 and the write timestamps,
//   - verifying the stored payload digest before returning a decoded body.
//
// It does not own resume policy (ArtifactCheckpoint.ReusableFor), external
// artifact byte verification (checkpoint.ArtifactVerifier), or the publication
// saga. Those remain with their existing owners.
package scripts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SQLiteArtifactCheckpointRepository stores one row per completed artifact in
// the observability plane. The zero value is not usable; construct it with
// NewSQLiteArtifactCheckpointRepository. *sql.DB is safe for concurrent use.
type SQLiteArtifactCheckpointRepository struct {
	db *sql.DB
}

// NewSQLiteArtifactCheckpointRepository wraps the observability database handle
// used by SQLiteRunRepository. A nil handle is rejected at construction rather
// than failing later on the first checkpoint write.
func NewSQLiteArtifactCheckpointRepository(db *sql.DB) (*SQLiteArtifactCheckpointRepository, error) {
	if db == nil {
		return nil, errors.New("scriptgeneration: artifact checkpoint repository requires observability DB")
	}
	return &SQLiteArtifactCheckpointRepository{db: db}, nil
}

const artifactCheckpointColumns = `run_id, artifact_kind, artifact_key, schema_version,
	input_fingerprint, processor_version, artifact_id, artifact_sha256, artifact_uri,
	artifact_size_bytes, artifact_duration_us, payload_json, payload_sha256, attempt,
	completed_at, created_at, updated_at`

// SaveArtifactCheckpoint validates the row, computes its canonical payload
// digest and timestamps, then upserts on the composite resume identity. The
// insert stamps created_at; a conflicting update preserves the original
// created_at and only refreshes updated_at.
func (r *SQLiteArtifactCheckpointRepository) SaveArtifactCheckpoint(ctx context.Context, checkpoint scriptgen.ArtifactCheckpoint) error {
	checkpoint, err := prepareArtifactCheckpointForWrite(checkpoint, time.Now().UTC())
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO run_artifact_checkpoints (
		run_id, artifact_kind, artifact_key, schema_version,
		input_fingerprint, processor_version,
		artifact_id, artifact_sha256, artifact_uri,
		artifact_size_bytes, artifact_duration_us,
		payload_json, payload_sha256, attempt,
		completed_at, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(run_id, artifact_kind, artifact_key) DO UPDATE SET
		schema_version = excluded.schema_version,
		input_fingerprint = excluded.input_fingerprint,
		processor_version = excluded.processor_version,
		artifact_id = excluded.artifact_id,
		artifact_sha256 = excluded.artifact_sha256,
		artifact_uri = excluded.artifact_uri,
		artifact_size_bytes = excluded.artifact_size_bytes,
		artifact_duration_us = excluded.artifact_duration_us,
		payload_json = excluded.payload_json,
		payload_sha256 = excluded.payload_sha256,
		attempt = excluded.attempt,
		completed_at = excluded.completed_at,
		updated_at = excluded.updated_at`,
		checkpoint.RunID,
		checkpoint.Kind,
		checkpoint.Key,
		checkpoint.SchemaVersion,
		checkpoint.InputFingerprint,
		checkpoint.ProcessorVersion,
		checkpoint.ArtifactID,
		checkpoint.ArtifactSHA256,
		checkpoint.ArtifactURI,
		checkpoint.ArtifactSizeBytes,
		checkpoint.ArtifactDurationUS,
		string(checkpoint.CanonicalPayload()),
		checkpoint.PayloadSHA256,
		checkpoint.Attempt,
		formatTime(checkpoint.CompletedAt),
		formatTime(checkpoint.CreatedAt),
		formatTime(checkpoint.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("scriptgeneration: save artifact checkpoint %s/%s/%s: %w",
			checkpoint.RunID, checkpoint.Kind, checkpoint.Key, err)
	}
	return nil
}

// GetArtifactCheckpoint performs the exact composite lookup. A missing row is
// (nil, nil); a row whose stored body no longer matches payload_sha256 is an
// ErrArtifactCheckpointCorrupt error, never a miss.
func (r *SQLiteArtifactCheckpointRepository) GetArtifactCheckpoint(ctx context.Context, runID string, kind scriptgen.ArtifactCheckpointKind, key string) (*scriptgen.ArtifactCheckpoint, error) {
	if err := validateArtifactCheckpointIdentity(runID, kind, key); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+artifactCheckpointColumns+`
		FROM run_artifact_checkpoints
		WHERE run_id = ? AND artifact_kind = ? AND artifact_key = ?`,
		runID, kind, key)
	checkpoint, err := scanArtifactCheckpoint(row)
	if err != nil {
		return nil, fmt.Errorf("scriptgeneration: get artifact checkpoint %s/%s/%s: %w", runID, kind, key, err)
	}
	return checkpoint, nil
}

// ListArtifactCheckpoints returns every checkpoint of one run in the canonical
// resume order. Any corrupt row fails the whole scan: a caller must not resume
// past a body the store cannot prove.
func (r *SQLiteArtifactCheckpointRepository) ListArtifactCheckpoints(ctx context.Context, runID string) ([]scriptgen.ArtifactCheckpoint, error) {
	if runID == "" || strings.TrimSpace(runID) != runID {
		return nil, fmt.Errorf("%w: run_id is required and must not be padded", scriptgen.ErrInvalidArtifactCheckpoint)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+artifactCheckpointColumns+`
		FROM run_artifact_checkpoints
		WHERE run_id = ?
		ORDER BY artifact_kind, artifact_key`, runID)
	if err != nil {
		return nil, fmt.Errorf("scriptgeneration: list artifact checkpoints %s: %w", runID, err)
	}
	defer func() { _ = rows.Close() }()

	checkpoints := make([]scriptgen.ArtifactCheckpoint, 0)
	for rows.Next() {
		checkpoint, scanErr := scanArtifactCheckpoint(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scriptgeneration: list artifact checkpoints %s: %w", runID, scanErr)
		}
		if checkpoint != nil {
			checkpoints = append(checkpoints, *checkpoint)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scriptgeneration: list artifact checkpoints %s: %w", runID, err)
	}
	return checkpoints, nil
}

// InvalidateArtifactCheckpoint deletes the single row. Deleting a missing row
// is a no-op, so cleanup can be retried without a state machine.
func (r *SQLiteArtifactCheckpointRepository) InvalidateArtifactCheckpoint(ctx context.Context, runID string, kind scriptgen.ArtifactCheckpointKind, key string) error {
	if err := validateArtifactCheckpointIdentity(runID, kind, key); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM run_artifact_checkpoints
		WHERE run_id = ? AND artifact_kind = ? AND artifact_key = ?`,
		runID, kind, key)
	if err != nil {
		return fmt.Errorf("scriptgeneration: invalidate artifact checkpoint %s/%s/%s: %w", runID, kind, key, err)
	}
	return nil
}

func prepareArtifactCheckpointForWrite(checkpoint scriptgen.ArtifactCheckpoint, now time.Time) (scriptgen.ArtifactCheckpoint, error) {
	// CanonicalPayload also turns a nil/empty body into the stored JSON
	// object, so the digest and the column always describe the same bytes.
	if checkpoint.PayloadSHA256 == "" {
		checkpoint.PayloadSHA256 = checkpoint.PayloadDigest()
	}
	if err := checkpoint.Validate(); err != nil {
		return checkpoint, err
	}
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = now
	}
	checkpoint.UpdatedAt = now
	return checkpoint, nil
}

func validateArtifactCheckpointIdentity(runID string, kind scriptgen.ArtifactCheckpointKind, key string) error {
	if runID == "" || strings.TrimSpace(runID) != runID {
		return fmt.Errorf("%w: run_id is required and must not be padded", scriptgen.ErrInvalidArtifactCheckpoint)
	}
	if !kind.Valid() {
		return fmt.Errorf("%w: unknown artifact kind %q", scriptgen.ErrInvalidArtifactCheckpoint, kind)
	}
	if key == "" || strings.TrimSpace(key) != key {
		return fmt.Errorf("%w: artifact key is required and must not be padded", scriptgen.ErrInvalidArtifactCheckpoint)
	}
	return nil
}

type artifactCheckpointScanner interface {
	Scan(dest ...any) error
}

func scanArtifactCheckpoint(row artifactCheckpointScanner) (*scriptgen.ArtifactCheckpoint, error) {
	var (
		checkpoint                        scriptgen.ArtifactCheckpoint
		kind                              string
		payload                           string
		completedAt, createdAt, updatedAt string
	)
	err := row.Scan(
		&checkpoint.RunID,
		&kind,
		&checkpoint.Key,
		&checkpoint.SchemaVersion,
		&checkpoint.InputFingerprint,
		&checkpoint.ProcessorVersion,
		&checkpoint.ArtifactID,
		&checkpoint.ArtifactSHA256,
		&checkpoint.ArtifactURI,
		&checkpoint.ArtifactSizeBytes,
		&checkpoint.ArtifactDurationUS,
		&payload,
		&checkpoint.PayloadSHA256,
		&checkpoint.Attempt,
		&completedAt,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	checkpoint.Kind = scriptgen.ArtifactCheckpointKind(kind)
	checkpoint.Payload = json.RawMessage(payload)

	// Verify the exact bytes before trusting the JSON. A matching digest is
	// not enough on its own: malformed JSON or an over-limit body is still a
	// row the reader must not decode, so both are reported as corrupt.
	if checkpoint.PayloadSHA256 == "" ||
		digest.SHA256Bytes([]byte(payload)) != checkpoint.PayloadSHA256 ||
		!json.Valid([]byte(payload)) ||
		len(payload) > scriptgen.MaxArtifactCheckpointPayloadBytes {
		return nil, fmt.Errorf("%w: run=%s kind=%s key=%s", scriptgen.ErrArtifactCheckpointCorrupt,
			checkpoint.RunID, checkpoint.Kind, checkpoint.Key)
	}

	if checkpoint.CompletedAt, err = parseArtifactCheckpointTime(completedAt); err != nil {
		return nil, fmt.Errorf("%w: completed_at: %v", scriptgen.ErrArtifactCheckpointCorrupt, err)
	}
	if checkpoint.CreatedAt, err = parseArtifactCheckpointTime(createdAt); err != nil {
		return nil, fmt.Errorf("%w: created_at: %v", scriptgen.ErrArtifactCheckpointCorrupt, err)
	}
	if checkpoint.UpdatedAt, err = parseArtifactCheckpointTime(updatedAt); err != nil {
		return nil, fmt.Errorf("%w: updated_at: %v", scriptgen.ErrArtifactCheckpointCorrupt, err)
	}
	return &checkpoint, nil
}

func parseArtifactCheckpointTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("timestamp is empty")
	}
	return parseTime(value)
}

var _ scriptgen.ArtifactCheckpointRepository = (*SQLiteArtifactCheckpointRepository)(nil)
