package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReindexRequester is the canonical compatibility seam for callers that still
// expose an imperative IndexClip/IndexAsset method. In PostgreSQL media mode
// those calls must NOT execute the retired SQLite -> Qdrant indexing pipeline;
// they enqueue the canonical asset.index.requested event in the PostgreSQL
// outbox and let PostgresIndexWorker perform embedding + pgvector indexing.
type ReindexRequester struct {
	db  *sql.DB
	box *Repository
}

// NewReindexRequester binds imperative reindex requests to the same
// PostgreSQL media SSOT/outbox used by canonical asset commits.
func NewReindexRequester(db *sql.DB) *ReindexRequester {
	if db == nil {
		return nil
	}
	return &ReindexRequester{db: db, box: NewOutboxRepository(db)}
}

// RequestIndex implements the narrow compatibility port consumed by the
// legacy-named clipindexer service. It is deliberately an event request, not
// an inline index operation: one canonical worker remains the only writer of
// media_embeddings/index_state in PostgreSQL mode.
func (r *ReindexRequester) RequestIndex(ctx context.Context, assetID string) error {
	if r == nil || r.db == nil || r.box == nil {
		return fmt.Errorf("postgres media reindex requester: not wired")
	}
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return fmt.Errorf("postgres media reindex requester: asset id is required")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres media reindex requester: begin: %w", err)
	}
	defer tx.Rollback()

	var (
		source, mediaType, sourceVersion string
		contentSHA, binarySHA           string
		assetVersion                    string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT source, media_type, source_version,
		       content_sha256, binary_sha256, asset_version
		FROM media_assets
		WHERE id = $1 AND deleted_at = ''
		FOR UPDATE
	`, assetID).Scan(&source, &mediaType, &sourceVersion, &contentSHA, &binarySHA, &assetVersion); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("postgres media reindex requester: asset %q not found", assetID)
		}
		return fmt.Errorf("postgres media reindex requester: load asset %q: %w", assetID, err)
	}

	// Old rows may predate source_version. Preserve fail-closed identity while
	// accepting the strongest canonical fingerprint already stored on the row.
	for _, candidate := range []string{sourceVersion, contentSHA, binarySHA, assetVersion} {
		if strings.TrimSpace(candidate) != "" {
			sourceVersion = strings.TrimSpace(candidate)
			break
		}
	}
	if sourceVersion == "" {
		return fmt.Errorf("postgres media reindex requester: asset %q has no canonical source/content version", assetID)
	}

	req := IndexRequest{
		AssetID:       assetID,
		Source:        source,
		MediaType:     mediaType,
		SourceVersion: sourceVersion,
		RequestedAt:   time.Now(),
		Priority:      PriorityHigh,
	}
	result, err := CommitIndexRequestTx(ctx, tx, r.box, req)
	if err != nil {
		return fmt.Errorf("postgres media reindex requester: enqueue asset %q: %w", assetID, err)
	}

	// A deterministic initial commit event may already be terminal. An
	// imperative reindex request must then create a fresh idempotency key rather
	// than silently succeeding without work. Pending/processing duplicates are
	// intentionally coalesced into the in-flight canonical request.
	if !result.Inserted && isTerminalOutboxStatus(result.ExistingStatus) {
		req.EventKeySuffix = ":reindex:" + uuid.NewString()
		if _, err := CommitIndexRequestTx(ctx, tx, r.box, req); err != nil {
			return fmt.Errorf("postgres media reindex requester: force reindex asset %q: %w", assetID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres media reindex requester: commit asset %q: %w", assetID, err)
	}
	return nil
}

func isTerminalOutboxStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "dead_letter", "superseded":
		return true
	default:
		return false
	}
}
