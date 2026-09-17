package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacommit"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// outboxRepository is the narrow outbox write surface consumed by the
// canonical commit. Production: *pgoutbox.Repository (this package family);
// tests may provide a structural fake.
type outboxRepository interface {
	Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*EnqueueResult, error)
	EnqueueWithPriority(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string, priority int) (*EnqueueResult, error)
}

// registryTxWriter is the transaction-scoped registry surface the
// PostgresMediaCommitter needs (SQLite mirror of
// imagesregistry.RegistryTxWriter). Production: *registry.Tx.
type registryTxWriter interface {
	RegisterSourceTx(ctx context.Context, tx *sql.Tx, src capregistry.AssetSource) error
	LinkContentTx(ctx context.Context, tx *sql.Tx, assetID, contentSHA256 string) error
	UpsertTaxonomyTx(ctx context.Context, tx *sql.Tx, t capregistry.AssetTaxonomy) error
	AppendEventTx(ctx context.Context, tx *sql.Tx, event capregistry.Event) (int64, error)
}

// mediacommitImageDraft is the image projection draft shape. Underlying
// type identity with mediacommit.ImageDraft is preserved (alias, not a new
// struct) so callers pass the capability draft unchanged.
type mediacommitImageDraft = mediacommit.ImageDraft

// PostgresMediaCommitter implements mediacommit.MediaCommitter and the
// full persistence canonical writer family over PostgreSQL.
type PostgresMediaCommitter struct {
	db     *sql.DB
	box    outboxRepository
	ledger registryTxWriter
	assets *PostgresAssetCommitter // reuses the canonical media_assets upsert (step 2)
	log    *zap.Logger
}

// compile-time port assertions: this adapter IS the canonical family.
var (
	_ mediacommit.MediaCommitter         = (*PostgresMediaCommitter)(nil)
	_ persistence.AssetCommitter         = (*PostgresMediaCommitter)(nil)
	_ persistence.AssetMutationCommitter = (*PostgresMediaCommitter)(nil)
)

// NewPostgresMediaCommitter constructs the adapter. db, box and ledger are
// required; nil values panic at construction time so wiring gaps surface at
// boot rather than at first commit.
func NewPostgresMediaCommitter(db *sql.DB, box outboxRepository, ledger registryTxWriter, log *zap.Logger) *PostgresMediaCommitter {
	if db == nil {
		panic("media.NewPostgresMediaCommitter: db is required")
	}
	if box == nil {
		panic("media.NewPostgresMediaCommitter: outbox repository is required")
	}
	if ledger == nil {
		panic("media.NewPostgresMediaCommitter: registry ledger is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &PostgresMediaCommitter{
		db:     db,
		box:    box,
		ledger: ledger,
		assets: NewPostgresAssetCommitter(db, box, log),
		log:    log,
	}
}

// DB returns the underlying *sql.DB handle. Exposed so the wiring layer can
// open caller-owned transactions for port adapters that need a tx-bound
// mutation (SQLite mirror: SQLiteMediaCommitter.DB).
func (c *PostgresMediaCommitter) DB() *sql.DB { return c.db }

// Outbox is the canonical outbox repository for this family.
func (c *PostgresMediaCommitter) Outbox() outboxRepository { return c.box }

// Compile-time constant mirrors: shared with the SQLite canonical writer so
// event keys are provider-scoped identically on both engines.
const (
	indexRequestOperationUpsert = "UPSERT"
)

// deriveSourceID mirrors imagesregistry.deriveSourceID (godlike/06 SSOT:
// the canonical constructor lives in capregistry.DeriveCanonicalSourceID).
func deriveSourceID(sourceType, sourceURI, sourceVersion string) string {
	return capregistry.DeriveCanonicalSourceID(sourceType, sourceURI, sourceVersion)
}

// deterministicCommitEventID mirrors imagesregistry.deterministicCommitEventID
// (SHA-1 UUID over the identity vector; replays keep the original seq).
func deterministicCommitEventID(assetID, sourceType, sourceURI, sourceVersion, contentSHA256 string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(assetID+"|"+sourceType+"|"+sourceURI+"|"+sourceVersion+"|"+contentSHA256)).String()
}

// firstNonEmpty mirrors imagesregistry.firstNonEmpty.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// optionalContent mirrors imagesregistry.optionalContent.
func optionalContent(hash string) *mediacommit.ContentIdentity {
	if hash == "" {
		return nil
	}
	return &mediacommit.ContentIdentity{ContentSHA256: hash}
}

// optionalImageDraft mirrors imagesregistry.optionalImageDraft.
func optionalImageDraft(r persistence.CommitRequest) *mediacommit.ImageDraft {
	if r.Origin == "" && r.Provider == "" {
		return nil
	}
	return &mediacommit.ImageDraft{Origin: r.Origin, Provider: r.Provider}
}

// assetDraftToCommitRequest mirrors imagesregistry.assetDraftToCommitRequest.
func assetDraftToCommitRequest(a mediacommit.AssetDraft) persistence.CommitRequest {
	return persistence.CommitRequest{
		AssetID: a.AssetID, Source: a.Source, Name: a.Name, Filename: a.Filename,
		MediaType: a.MediaType, Category: a.Category, GroupName: a.GroupName,
		DurationMs: a.DurationMs, ContentHash: a.ContentHash,
		Description: a.Description, SearchText: a.SearchText,
		LifecycleState: a.LifecycleState, IndexState: a.IndexState,
		LocalPath: a.LocalPath, FolderID: a.FolderID, FolderPath: a.FolderPath,
		ThumbnailURL: a.ThumbnailURL, DownloadLink: a.DownloadLink,
		SourceURL: a.SourceURL, ClipPageURL: a.ClipPageURL, Title: a.Title,
		SourceProvider: a.SourceProvider, SourceVideoID: a.SourceVideoID,
		StartMs: a.StartMs, EndMs: a.EndMs, Metadata: a.Metadata, Locations: a.Locations,
	}
}

// persistenceToMediaRequest mirrors imagesregistry.persistenceToMediaRequest.
func persistenceToMediaRequest(r persistence.CommitRequest) mediacommit.CommitMediaAssetRequest {
	if r.EmitIndexEvent && r.Taxonomy.IsZero() {
		if taxonomy, err := capregistry.ResolveTaxonomy(capregistry.TaxonomyInput{
			AssetID: r.AssetID, Provider: r.Source, MediaType: capregistry.MediaType(r.MediaType),
		}); err == nil {
			r.Taxonomy = taxonomy
		}
	}
	sourceRef := firstNonEmpty(r.SourceVideoID, firstNonEmpty(r.SourceURL, r.AssetLocation))
	sourceVersion := firstNonEmpty(r.AssetVersion, firstNonEmpty(r.Metadata.SourceVersion, r.ContentHash))
	return mediacommit.CommitMediaAssetRequest{
		Asset: mediacommit.AssetDraft{
			AssetID: r.AssetID, Source: r.Source, Name: r.Name, Filename: r.Filename,
			MediaType: r.MediaType, Category: r.Category, GroupName: r.GroupName,
			DurationMs: r.DurationMs, ContentHash: r.ContentHash,
			Description: r.Description, SearchText: r.SearchText,
			LifecycleState: r.LifecycleState, IndexState: r.IndexState,
			LocalPath: r.LocalPath, FolderID: r.FolderID, FolderPath: r.FolderPath,
			ThumbnailURL: r.ThumbnailURL, DownloadLink: r.DownloadLink,
			SourceURL: r.SourceURL, ClipPageURL: r.ClipPageURL, Title: r.Title,
			SourceProvider: r.SourceProvider, SourceVideoID: r.SourceVideoID,
			StartMs: r.StartMs, EndMs: r.EndMs, Metadata: r.Metadata,
			Locations: r.Locations, Image: optionalImageDraft(r),
		},
		Source: mediacommit.AssetSourceDraft{
			SourceType: r.Source, SourceURI: sourceRef, SourceVersion: sourceVersion, IsPrimary: true,
		},
		Content:                optionalContent(r.ContentHash),
		Taxonomy:               r.Taxonomy,
		IndexPolicy:            mediacommit.IndexPolicy{Indexable: r.EmitIndexEvent, Priority: r.IndexPriority},
		AdditionalOutboxEvents: r.AdditionalOutboxEvents,
		Actor:                  "persistence-compat",
	}
}

// mediaToPersistenceResult mirrors imagesregistry.mediaToPersistenceResult.
func mediaToPersistenceResult(r mediacommit.CommitMediaAssetResult) persistence.CommitResult {
	return persistence.CommitResult{
		AssetRowsAffected: 1, OutboxEventKey: r.OutboxEventKey,
		OutboxInserted: r.OutboxInserted, OutboxExistingStatus: r.OutboxExistingStatus,
		AdditionalOutbox: r.AdditionalOutbox,
	}
}

// legacyToMediaCommitRequest mirrors imagesregistry.legacyToMediaCommitRequest.
func legacyToMediaCommitRequest(r persistence.CommitRequest) mediacommit.CommitMediaAssetRequest {
	taxonomy := r.Taxonomy
	if r.EmitIndexEvent && taxonomy.IsZero() {
		if resolved, err := capregistry.ResolveTaxonomy(capregistry.TaxonomyInput{
			AssetID: r.AssetID, Provider: r.Source, MediaType: capregistry.MediaType(r.MediaType),
		}); err == nil {
			taxonomy = resolved
		}
	}
	return mediacommit.CommitMediaAssetRequest{
		Asset: mediacommit.AssetDraft{
			AssetID: r.AssetID, Source: r.Source, Name: r.Name, Filename: r.Filename,
			MediaType: r.MediaType, Category: r.Category, GroupName: r.GroupName,
			DurationMs: r.DurationMs, ContentHash: r.ContentHash,
			Description: r.Description, SearchText: r.SearchText,
			LifecycleState: r.LifecycleState, IndexState: r.IndexState,
			LocalPath: r.LocalPath, FolderID: r.FolderID, FolderPath: r.FolderPath,
			ThumbnailURL: r.ThumbnailURL, DownloadLink: r.DownloadLink,
			SourceURL: r.SourceURL, ClipPageURL: r.ClipPageURL, Title: r.Title,
			SourceProvider: r.SourceProvider, SourceVideoID: r.SourceVideoID,
			StartMs: r.StartMs, EndMs: r.EndMs, Metadata: r.Metadata, Locations: r.Locations,
		},
		IndexPolicy: mediacommit.IndexPolicy{Indexable: r.EmitIndexEvent, Priority: r.IndexPriority},
		Taxonomy:    taxonomy,
	}
}

// CommitMediaAsset opens a fresh transaction, runs the 8-step canonical
// commit, and commits atomically.
func (c *PostgresMediaCommitter) CommitMediaAsset(ctx context.Context, req mediacommit.CommitMediaAssetRequest) (mediacommit.CommitMediaAssetResult, error) {
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
func (c *PostgresMediaCommitter) commitTx(ctx context.Context, tx *sql.Tx, req mediacommit.CommitMediaAssetRequest) (mediacommit.CommitMediaAssetResult, error) {
	assetID := req.Asset.AssetID
	nowStr := time.Now().UTC().Format(time.RFC3339)

	// 1. Resolve canonical identity.
	exists, err := c.assetExists(ctx, tx, assetID)
	if err != nil {
		return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: resolve identity: %w", err)
	}
	created := !exists
	sourceID := deriveSourceID(req.Source.SourceType, req.Source.SourceURI, req.Source.SourceVersion)
	contentSHA256 := ""
	if req.Content != nil {
		contentSHA256 = req.Content.ContentSHA256
	}

	// 2. Upsert media_assets (+ locations + metadata + taxonomy), no
	// outbox yet. Taxonomy dimensions are written in the SAME upsert.
	commitReq := assetDraftToCommitRequest(req.Asset)
	commitReq.Taxonomy = req.Taxonomy
	commitReq.EmitIndexEvent = false
	if _, err := c.assets.CommitTxRaw(ctx, tx, commitReq); err != nil {
		return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: upsert media_assets: %w", err)
	}
	if req.Asset.Image != nil {
		if err := c.updateImageFields(ctx, tx, assetID, req.Asset.Image); err != nil {
			return mediacommit.CommitMediaAssetResult{}, err
		}
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

	// 5. Upsert text tracks.
	if err := c.upsertTextTracks(ctx, tx, assetID, req.TextTracks, nowStr); err != nil {
		return mediacommit.CommitMediaAssetResult{}, err
	}

	// 6. Append registry event.
	registrySeq, err := c.ledger.AppendEventTx(ctx, tx, capregistry.Event{
		EventID:     deterministicCommitEventID(assetID, req.Source.SourceType, req.Source.SourceURI, req.Source.SourceVersion, contentSHA256),
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

	// 7. Outbox asset.index.requested when indexable.
	var indexResult IndexRequestCommitResult
	if req.IndexPolicy.Indexable {
		sourceVersion := firstNonEmpty(req.Source.SourceVersion, req.Asset.ContentHash)
		indexResult, err = CommitIndexRequestTx(ctx, tx, c.box, IndexRequest{
			AssetID:       assetID,
			Source:        req.Asset.Source,
			MediaType:     req.Asset.MediaType,
			SourceVersion: sourceVersion,
			RequestedAt:   time.Now(),
			Priority:      req.IndexPolicy.Priority,
		})
		if err != nil {
			return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: emit index request: %w", err)
		}
	}

	// Additional external side effects are represented as durable outbox
	// intents and must survive the compatibility conversion above.
	additionalOutbox := make([]persistence.AdditionalOutboxResult, 0, len(req.AdditionalOutboxEvents))
	for i, event := range req.AdditionalOutboxEvents {
		if event.EventType == "" || event.EventKey == "" {
			return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: additional outbox event[%d] requires event type and event key", i)
		}
		enqueueResult, err := c.box.Enqueue(ctx, tx, event.EventType, event.AggregateID, event.AggregateType, event.PayloadJSON, event.EventKey)
		if err != nil {
			return mediacommit.CommitMediaAssetResult{}, fmt.Errorf("media committer: enqueue additional event %q: %w", event.EventType, err)
		}
		additionalOutbox = append(additionalOutbox, persistence.AdditionalOutboxResult{
			EventKey: event.EventKey, Inserted: enqueueResult.Inserted,
			ExistingStatus: enqueueResult.ExistingStatus,
		})
	}

	return mediacommit.CommitMediaAssetResult{
		AssetID:              assetID,
		Created:              created,
		SourceID:             sourceID,
		ContentSHA256:        contentSHA256,
		RegistrySeq:          registrySeq,
		OutboxEventKey:       indexResult.EventKey,
		OutboxInserted:       indexResult.Inserted,
		OutboxExistingStatus: indexResult.ExistingStatus,
		AdditionalOutbox:     additionalOutbox,
	}, nil
}

// assetExists reports whether the asset row already exists inside tx.
func (c *PostgresMediaCommitter) assetExists(ctx context.Context, tx *sql.Tx, assetID string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM media_assets WHERE id = $1`, assetID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// updateImageFields mirrors imagesregistry.updateImageFields; the typed
// draft is persisted through the same projection the SQLite writer uses.
func (c *PostgresMediaCommitter) updateImageFields(ctx context.Context, tx *sql.Tx, assetID string, image *mediacommit.ImageDraft) error {
	if image == nil {
		return nil
	}
	if err := UpdateMediaAssetImageFields(ctx, tx, assetID, image); err != nil {
		return fmt.Errorf("media committer: update image fields: %w", err)
	}
	return nil
}

// upsertTextTracks mirrors imagesregistry.upsertTextTracks: flips any prior
// current row to non-current and inserts the new current row, honoring the
// partial unique index idx_asset_text_tracks_current.
func (c *PostgresMediaCommitter) upsertTextTracks(ctx context.Context, tx *sql.Tx, assetID string, tracks []mediacommit.TextTrack, nowStr string) error {
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
			WHERE asset_id = $1 AND language_code = $2 AND text_kind = $3 AND is_current = 1`,
			assetID, t.LanguageCode, t.TextKind); err != nil {
			return fmt.Errorf("media committer: deactivate prior text track: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO asset_text_tracks
			(asset_id, language_code, text_kind, text_content, source_type, is_current, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, 1, $6, $7)`,
			assetID, t.LanguageCode, t.TextKind, t.TextContent, sourceType, nowStr, nowStr); err != nil {
			return fmt.Errorf("media committer: upsert text track: %w", err)
		}
	}
	return nil
}

// CommitAsset is the SINGLE canonical user-facing entry point for the
// asset commit surface.
func (c *PostgresMediaCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return c.commitPersistence(ctx, persistence.CommitRequest(req))
}

// CommitAndIndex opens a new transaction, calls CommitTx, and commits.
func (c *PostgresMediaCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	return c.commitPersistence(ctx, req)
}

// CommitTx accepts the caller-owned transaction used by orchestrators.
func (c *PostgresMediaCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok || sqlTx == nil {
		return persistence.CommitResult{}, fmt.Errorf("media committer: expected *sql.Tx, got %T", tx)
	}
	result, err := c.commitTx(ctx, sqlTx, persistenceToMediaRequest(req))
	if err != nil {
		return persistence.CommitResult{}, err
	}
	return mediaToPersistenceResult(result), nil
}

func (c *PostgresMediaCommitter) commitPersistence(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	result, err := c.CommitMediaAsset(ctx, persistenceToMediaRequest(req))
	if err != nil {
		return persistence.CommitResult{}, err
	}
	return mediaToPersistenceResult(result), nil
}

// PostgresMediaCommitter's narrow post-commit mutation delegates (CommitLegacy,
// PersistEmbeddingJSON, UpdateAssetMetadata, SetIndexState/SetIndexed,
// Patch/ReplaceMetadataJSON, UpdateFolderPath, UpdateLifecycle,
// UpdateTaxonomy, LinkContent, UpdateSearchText, RefreshUpdatedAt,
// UpdateOrphanMetadata, UpdateDriveDeliveryByLegacyHash) live in the sibling
// media_committer_mutations.go so this file stays under the 600-LOC strict cap.

// discoveryCommitRequest builds the canonical discovery commit request.
// Single translation site shared by the caller-owned-tx and self-owned-tx
// entry points so the two cannot drift.
func (c *PostgresMediaCommitter) discoveryCommitRequest(clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) (mediacommit.CommitMediaAssetRequest, error) {
	if clip == nil || clip.ID == "" {
		return mediacommit.CommitMediaAssetRequest{}, fmt.Errorf("media committer: discovered asset id is required")
	}
	taxonomy, err := capregistry.ResolveTaxonomy(capregistry.TaxonomyInput{
		AssetID:   clip.ID,
		Provider:  string(clip.Source),
		MediaType: capregistry.MediaType(clip.MediaType),
	})
	if err != nil {
		return mediacommit.CommitMediaAssetRequest{}, fmt.Errorf("media committer: resolve discovery taxonomy: %w", err)
	}
	ref := firstNonEmpty(clip.MetadataSourceVideoID(), firstNonEmpty(clip.SourceURL, clip.ID))
	// MEDIA-IDENTITY (media-identity programme, Sept 2026): the commit's
	// ContentHash is the BYTE identity, so it is resolved from the canonical
	// content surfaces — and from legacy_file_md5 only while that column still
	// carries a SHA-256. clip.LegacyFileMD5() is the compatibility accessor and
	// is documented to be able to hold an MD5; an MD5 must never become the
	// content address of a media row (see asset.ContentAddress).
	contentHash := clip.ContentAddress()
	return mediacommit.CommitMediaAssetRequest{
		Asset: mediacommit.AssetDraft{
			AssetID: clip.ID, Source: string(clip.Source), Name: clip.Name, Filename: clip.Filename,
			MediaType: string(clip.MediaType), Category: clip.Category, DurationMs: clip.Duration.Milliseconds(),
			ContentHash: contentHash, SearchText: clip.SearchText, LifecycleState: string(lifecycle),
			IndexState: string(idx), SourceURL: clip.SourceURL, ThumbnailURL: clip.ThumbnailURL, Title: clip.Name,
			Metadata: persistence.TypedMetadata{Extra: clip.Metadata},
		},
		Source:      mediacommit.AssetSourceDraft{SourceType: string(clip.Source), SourceURI: ref, SourceVersion: firstNonEmpty(clip.GetMetadataString("job_key"), ref), IsPrimary: true},
		Content:     optionalContent(contentHash),
		Taxonomy:    taxonomy,
		IndexPolicy: mediacommit.IndexPolicy{Indexable: false}, Actor: "discovery-dispatcher",
	}, nil
}

// CommitDiscoveredAsset records discovery metadata and provenance in the
// caller-owned transaction without scheduling semantic indexing.
func (c *PostgresMediaCommitter) CommitDiscoveredAsset(ctx context.Context, tx *sql.Tx, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	request, err := c.discoveryCommitRequest(clip, lifecycle, idx)
	if err != nil {
		return err
	}
	_, err = c.commitTx(ctx, tx, request)
	return err
}

// CommitDiscoveredAssetAndIndex is the self-owned-transaction counterpart of
// CommitDiscoveredAsset. It exists because the outbox dispatcher's TxManager
// is the SQLite outbox pool while this committer owns the PostgreSQL media
// SSOT: the dispatcher cannot hand this adapter a SQLite *sql.Tx (its
// PostgreSQL statements fail with `unrecognized token: ":"`). The dispatcher
// therefore opts into outbox.PortableCommitPath and calls this method, which
// opens its own PostgreSQL transaction and commits the discovered asset
// atomically. Semantics (taxonomy, provenance, no index request) are
// identical to CommitDiscoveredAsset.
func (c *PostgresMediaCommitter) CommitDiscoveredAssetAndIndex(ctx context.Context, clip *asset.Asset, lifecycle asset.LifecycleState, idx asset.IndexState) error {
	request, err := c.discoveryCommitRequest(clip, lifecycle, idx)
	if err != nil {
		return err
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("media committer: begin discovery tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := c.commitTx(ctx, tx, request); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("media committer: commit discovery: %w", err)
	}
	committed = true
	return nil
}
