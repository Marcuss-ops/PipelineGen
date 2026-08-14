// Package assets — media_committer.go: the canonical SQLite MediaCommitter.
//
// MediaCommitter is the SINGLE transactional commit gate for media assets.
// It runs the full canonical sequence inside ONE SQLite transaction so a
// commit can never leave a half-populated asset (the pre-gate failure mode
// was: insert asset → commit → maybe taxonomy → maybe provenance → maybe
// Qdrant). Every producer (YouTube, Artlist, Stock, Drive/Images, AI,
// Voiceover, Audio, …) must converge here.
//
// Steps (all in one tx):
//
//  1. resolve canonical identity (asset existence → Created, source id)
//  2. upsert media_assets (+ asset_locations + typed metadata) via the
//     legacy asset committer's raw path
//  3. RegisterSource (media_asset_sources)
//  4. LinkContent (content_sha256) when the bytes are known
//  5. UpsertTaxonomy (namespace / asset_kind / source_type / semantic_role)
//  6. upsert transcript/text tracks (asset_text_tracks)
//  7. append registry event (registry_events) → registry seq
//  8. write asset.index.requested outbox event when indexable
package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mediacommit"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// RegistryTxWriter is the transaction-scoped registry surface the
// MediaCommitter needs. Production: *sqlitemediaregistry.Ledger.
type RegistryTxWriter interface {
	RegisterSourceTx(ctx context.Context, tx *sql.Tx, src capregistry.AssetSource) error
	LinkContentTx(ctx context.Context, tx *sql.Tx, assetID, contentSHA256 string) error
	UpsertTaxonomyTx(ctx context.Context, tx *sql.Tx, t capregistry.AssetTaxonomy) error
	AppendEventTx(ctx context.Context, tx *sql.Tx, event capregistry.Event) (int64, error)
}

// SQLiteMediaCommitter implements mediacommit.MediaCommitter.
type SQLiteMediaCommitter struct {
	db     *sql.DB
	box    *outboxevents.Repository
	ledger RegistryTxWriter
	assets *SQLiteAssetCommitter // reuses the canonical media_assets upsert (step 2)
	log    *zap.Logger
}

// NewSQLiteMediaCommitter constructs the adapter. db, box and ledger are
// required; nil values panic at construction time so wiring gaps surface at
// boot rather than at first commit.
func NewSQLiteMediaCommitter(db *sql.DB, box *outboxevents.Repository, ledger RegistryTxWriter, log *zap.Logger) *SQLiteMediaCommitter {
	if db == nil {
		panic("assets.NewSQLiteMediaCommitter: db is required")
	}
	if box == nil {
		panic("assets.NewSQLiteMediaCommitter: outboxevents.Repository is required")
	}
	if ledger == nil {
		panic("assets.NewSQLiteMediaCommitter: registry ledger is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteMediaCommitter{
		db:     db,
		box:    box,
		ledger: ledger,
		assets: NewSQLiteAssetCommitter(db, box, log),
		log:    log,
	}
}

// Compile-time assertion.
var _ mediacommit.MediaCommitter = (*SQLiteMediaCommitter)(nil)

// CommitMediaAsset opens a fresh transaction, runs the 8-step canonical
// commit, and commits atomically.
func (c *SQLiteMediaCommitter) CommitMediaAsset(ctx context.Context, req mediacommit.CommitMediaAssetRequest) (mediacommit.CommitMediaAssetResult, error) {
	if err := req.Validate(); err != nil {
		return mediacommit.CommitMediaAssetResult{}, err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	res, err := c.commitTx(ctx, tx, req)
	if err != nil {
		return mediacommit.CommitMediaAssetResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: commit: %w", err)
	}
	committed = true
	return res, nil
}

// commitTx runs the canonical 8-step commit inside the caller-owned tx.
func (c *SQLiteMediaCommitter) commitTx(ctx context.Context, tx *sql.Tx, req mediacommit.CommitMediaAssetRequest) (mediacommit.CommitMediaAssetResult, error) {
	assetID := req.Asset.AssetID
	nowStr := time.Now().UTC().Format(time.RFC3339)

	// 1. Resolve canonical identity.
	exists, err := c.assetExists(ctx, tx, assetID)
	if err != nil {
		return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: resolve identity: %w", err)
	}
	created := !exists
	sourceID := deriveSourceID(assetID, req.Source.SourceType, req.Source.SourceURI, req.Source.SourceVersion)
	contentSHA256 := ""
	if req.Content != nil {
		contentSHA256 = req.Content.ContentSHA256
	}

	// 2. Upsert media_assets (+ locations + metadata), no outbox yet.
	commitReq := assetDraftToCommitRequest(req.Asset)
	commitReq.EmitIndexEvent = false
	if _, err := c.assets.commitTxRaw(ctx, tx, commitReq); err != nil {
		return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: upsert media_assets: %w", err)
	}

	// 3. RegisterSource.
	if req.Source.SourceType != "" && req.Source.SourceURI != "" {
		if err := c.ledger.RegisterSourceTx(ctx, tx, capregistry.AssetSource{
			SourceID:      sourceID,
			AssetID:       assetID,
			ContentSHA256: contentSHA256,
			SourceType:    req.Source.SourceType,
			SourceURI:     req.Source.SourceURI,
			SourceVersion: req.Source.SourceVersion,
			DiscoveredAt:  nowStr,
			IsPrimary:     req.Source.IsPrimary,
		}); err != nil {
			return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: register source: %w", err)
		}
	}

	// 4. LinkContent when the bytes are known.
	if contentSHA256 != "" {
		if err := c.ledger.LinkContentTx(ctx, tx, assetID, contentSHA256); err != nil {
			return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: link content: %w", err)
		}
	}

	// 5. UpsertTaxonomy (skipped for legacy producers that do not yet
	// supply the canonical dimensions; they converge incrementally).
	if !req.Taxonomy.IsZero() {
		taxonomy := req.Taxonomy
		taxonomy.AssetID = assetID
		if err := c.ledger.UpsertTaxonomyTx(ctx, tx, taxonomy); err != nil {
			return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: upsert taxonomy: %w", err)
		}
	}

	// 6. Upsert text tracks.
	if err := c.upsertTextTracks(ctx, tx, assetID, req.TextTracks, nowStr); err != nil {
		return mediacommit.CommitMediaAssetResult{}, err
	}

	// 7. Append registry event.
	registrySeq, err := c.ledger.AppendEventTx(ctx, tx, capregistry.Event{
		EventID:     uuid.NewString(),
		AssetID:     assetID,
		EventType:   "MEDIA_ASSET_COMMITTED",
		RunID:       req.RunID,
		Actor:       firstNonEmpty(req.Actor, "media-committer"),
		AfterHash:   contentSHA256,
		PayloadJSON: fmt.Sprintf(`{"asset_id":%q,"source_type":%q,"created":%t}`, assetID, req.Asset.Source, created),
		CreatedAt:   nowStr,
	})
	if err != nil {
		return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: append registry event: %w", err)
	}

	// 8. Outbox asset.index.requested when indexable.
	if req.IndexPolicy.Indexable {
		sourceVersion := firstNonEmpty(req.Source.SourceVersion, req.Asset.ContentHash)
		if _, err := CommitIndexRequestTx(ctx, tx, c.box, IndexRequest{
			AssetID:               assetID,
			Source:                req.Asset.Source,
			MediaType:             req.Asset.MediaType,
			SourceVersion:         sourceVersion,
			RequestedAt:           time.Now(),
			UseProviderEventKey:   req.Asset.Source == "artlist",
			IncludeSourceMetadata: req.Asset.Source == "artlist",
			Priority:              req.IndexPolicy.Priority,
		}); err != nil {
			return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: emit index request: %w", err)
		}
	}

	return mediacommit.CommitMediaAssetResult{
		AssetID:       assetID,
		Created:       created,
		SourceID:      sourceID,
		ContentSHA256: contentSHA256,
		RegistrySeq:   registrySeq,
	}, nil
}

// assetExists reports whether the asset row already exists inside tx.
func (c *SQLiteMediaCommitter) assetExists(ctx context.Context, tx *sql.Tx, assetID string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM media_assets WHERE id = ?`, assetID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// upsertTextTracks flips any prior current row to non-current and inserts the
// new current row, honoring the partial UNIQUE INDEX idx_asset_text_tracks_current.
func (c *SQLiteMediaCommitter) upsertTextTracks(ctx context.Context, tx *sql.Tx, assetID string, tracks []mediacommit.TextTrack, nowStr string) error {
	for i, t := range tracks {
		if t.LanguageCode == "" || t.TextKind == "" {
			return fmt.Errorf("media committer: text track[%d] requires language_code and text_kind", i)
		}
		sourceType := t.SourceType
		if sourceType == "" {
			sourceType = "provided"
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE asset_text_tracks SET is_current = 0
			WHERE asset_id = ? AND language_code = ? AND text_kind = ? AND is_current = 1`,
			assetID, t.LanguageCode, t.TextKind); err != nil {
			return fmt.Errorf("media committer: deactivate prior text track: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO asset_text_tracks
			(asset_id, language_code, text_kind, text_content, source_type, is_current, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 1, ?, ?)`,
			assetID, t.LanguageCode, t.TextKind, t.TextContent, sourceType, nowStr, nowStr); err != nil {
			return fmt.Errorf("media committer: upsert text track: %w", err)
		}
	}
	return nil
}

// assetDraftToCommitRequest maps the canonical draft onto the legacy commit
// request shape consumed by the asset committer's raw path.
func assetDraftToCommitRequest(a mediacommit.AssetDraft) persistence.CommitRequest {
	return persistence.CommitRequest{
		AssetID:        a.AssetID,
		Source:         a.Source,
		Name:           a.Name,
		Filename:       a.Filename,
		MediaType:      a.MediaType,
		Category:       a.Category,
		DurationMs:     a.DurationMs,
		ContentHash:    a.ContentHash,
		Description:    a.Description,
		SearchText:     a.SearchText,
		LifecycleState: a.LifecycleState,
		IndexState:     a.IndexState,
		LocalPath:      a.LocalPath,
		FolderID:       a.FolderID,
		FolderPath:     a.FolderPath,
		ThumbnailURL:   a.ThumbnailURL,
		SourceURL:      a.SourceURL,
		Title:          a.Title,
		SourceProvider: a.SourceProvider,
		SourceVideoID:  a.SourceVideoID,
		StartMs:        a.StartMs,
		EndMs:          a.EndMs,
		Metadata:       a.Metadata,
		Locations:      a.Locations,
	}
}

// deriveSourceID derives the deterministic source identity (godlike/06 SSOT:
// the canonical constructor lives in capregistry.DeriveAssetSourceID).
func deriveSourceID(assetID, sourceType, sourceURI, sourceVersion string) string {
	return capregistry.DeriveAssetSourceID(assetID, sourceType, sourceURI, sourceVersion)
}

// CommitLegacy bridges a legacy persistence.CommitRequest onto the canonical
// MediaCommitter so existing writers converge with a one-line change:
//
//	legacyWriter.Commit(...) { return mediaCommitter.CommitLegacy(ctx, req) }
//
// Source/taxonomy/text-tracks/content are left empty (those producers do not
// yet supply them); the commit still runs atomically through the canonical
// gate (asset upsert + registry event + outbox when EmitIndexEvent).
func (c *SQLiteMediaCommitter) CommitLegacy(ctx context.Context, req persistence.CommitRequest) (mediacommit.CommitMediaAssetResult, error) {
	return c.CommitMediaAsset(ctx, legacyToMediaCommitRequest(req))
}

// legacyToMediaCommitRequest maps the legacy request onto the canonical shape.
func legacyToMediaCommitRequest(r persistence.CommitRequest) mediacommit.CommitMediaAssetRequest {
	return mediacommit.CommitMediaAssetRequest{
		Asset: mediacommit.AssetDraft{
			AssetID:        r.AssetID,
			Source:         r.Source,
			Name:           r.Name,
			Filename:       r.Filename,
			MediaType:      r.MediaType,
			Category:       r.Category,
			DurationMs:     r.DurationMs,
			ContentHash:    r.ContentHash,
			Description:    r.Description,
			SearchText:     r.SearchText,
			LifecycleState: r.LifecycleState,
			IndexState:     r.IndexState,
			LocalPath:      r.LocalPath,
			FolderID:       r.FolderID,
			FolderPath:     r.FolderPath,
			ThumbnailURL:   r.ThumbnailURL,
			SourceURL:      r.SourceURL,
			Title:          r.Title,
			SourceProvider: r.SourceProvider,
			SourceVideoID:  r.SourceVideoID,
			StartMs:        r.StartMs,
			EndMs:          r.EndMs,
			Metadata:       r.Metadata,
			Locations:      r.Locations,
		},
		IndexPolicy: mediacommit.IndexPolicy{Indexable: r.EmitIndexEvent, Priority: r.IndexPriority},
	}
}
