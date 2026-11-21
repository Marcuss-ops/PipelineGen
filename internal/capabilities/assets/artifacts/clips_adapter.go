package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type ClipsRegistry struct {
	log *zap.Logger
	// The generic `assets detail.Repository` seam was DELETED on 2026-09-13
	// (MEDIA-SSOT write-bridge). It carried no information about which
	// database owned the write, so in PostgreSQL mode the composition root
	// could satisfy it with the operational SQLite facade while the canonical
	// committer wrote PostgreSQL: the pre-dispatcher media_assets UPSERT was
	// silently split, and DeleteMedia retired the row on the wrong engine.
	// The UPSERT now routes through `dispatcher.EnqueueAndIndex` (PR 7, June
	// 2026, codex/pgvector-app-writers-fail-closed) and the retirement resolves
	// persistence.CanonicalAssetSoftDeleter from the canonical committer, so
	// this capability names an engine for neither.
	//
	// The locations + processing writes stay on their respective narrow typed
	// ports (asset_locations + asset processing) and are NOT subject to the
	// dispatcher SSOT.
	//
	// committer is the canonical persistence.AssetCommitter SSOT
	// (VECTOR-002 PR7). Required for the media_assets UPSERT path so
	// the production write emits the matching outbox_events row in
	// the same tx (v1 conflation invariant).
	committer persistence.AssetCommitter
	querySvc  AssetDetailsReader
	// processing is the narrow engine-named asset_processing write port
	// (persistence.AssetProcessingWriter). It replaced the generic
	// detail.ProcessingRepository seam, which carried no information about
	// which database owned the write — the composition root satisfied it with
	// the operational SQLite store while PostgreSQL held media_assets, so
	// pipeline-step progress diverged from the media SSOT. asset_processing is
	// now media-authoritative (migrations/postgres/008) and the port is
	// resolved from the canonical committer, so this capability names no
	// engine of its own. Nil is a media-plane-closed signal: the write is
	// skipped as best-effort observability rather than sent to a second
	// database.
	processing  persistence.AssetProcessingWriter
	mediaReader MediaReader
}

// AssetDetailsReader is the narrow media-details read the registry hydrates a
// MediaRecord from. Declaring it here — instead of requiring the concrete
// *detail.Service — is what lets the PostgreSQL media SSOT serve media
// hydration (MEDIA-SSOT P2-9 step 2).
//
// *detail.Service (legacy SQLite) and *pgmedia.AssetDetailsReader both satisfy
// it, so the composition root picks the engine and this capability stops
// naming one. A nil implementation is a media-plane-closed signal, and
// GetMedia fails closed rather than silently reading a divergent catalog.
type AssetDetailsReader interface {
	Get(ctx context.Context, id string) (*asset.Details, error)
}

// MediaReader is the PostgreSQL media-SSOT read port consumed by the registry.
// It is the single owner of the media_assets reads the capability used to run
// itself: Get, pHash lookup, content-identity lookup and the Drive-file sweep.
// A nil reader means the media plane is closed and every method fails closed.
type MediaReader interface {
	Get(ctx context.Context, id string) (*MediaRecord, error)
	FindByPHash(ctx context.Context, phash string) (string, error)
	FindByContentHash(ctx context.Context, sha256 string) (*MediaRecord, error)
	GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error)
}

// ClipsRegistryOption customizes the registry without breaking the existing
// positional constructors. The wiring passes WithMediaReader from the canonical
// EngineHandle; tests that exercise the legacy details reader can omit it.
type ClipsRegistryOption func(*ClipsRegistry)

func WithMediaReader(reader MediaReader) ClipsRegistryOption {
	return func(r *ClipsRegistry) {
		if r != nil {
			r.mediaReader = reader
		}
	}
}

// NewClipsRegistry is the canonical ctor. PR 7 (June 2026) added a 6th
// positional `committer` arg so the registry's UpsertMedia path
// enforces the canonical outbox+tx writer (VECTOR-002 atomicity
// invariant).
func NewClipsRegistry(
	querySvc AssetDetailsReader,
	processing persistence.AssetProcessingWriter,
	committer persistence.AssetCommitter,
	options ...ClipsRegistryOption,
) *ClipsRegistry {
	registry := &ClipsRegistry{
		querySvc:   querySvc,
		processing: processing,
		committer:  committer,
		log:        zap.NewNop(),
	}
	for _, option := range options {
		if option != nil {
			option(registry)
		}
	}
	return registry
}

// NewClipsRegistryWithLogger is the wiring variant that threads the
// composition logger so operational best-effort warnings (P1-5) are
// observable.
func NewClipsRegistryWithLogger(
	querySvc AssetDetailsReader,
	processing persistence.AssetProcessingWriter,
	committer persistence.AssetCommitter,
	log *zap.Logger,
	options ...ClipsRegistryOption,
) *ClipsRegistry {
	r := NewClipsRegistry(querySvc, processing, committer, options...)
	if log != nil {
		r.log = log
	}
	return r
}

// logger returns the wired logger, falling back to a no-op for a zero-value
// ClipsRegistry. Both constructors guarantee a non-nil logger, so this is a
// guard for direct struct literals only — never a silent-logging decision.
func (r *ClipsRegistry) logger() *zap.Logger {
	if r == nil || r.log == nil {
		return zap.NewNop()
	}
	return r.log
}

func (r *ClipsRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	// PR 7 followups (June 2026, codex/pgvector-app-writers-fail-closed): nil
	// contract guards applied in the order (1) dispatcher fail-closed then
	// (2) nil-rec contract violation. Dispatcher-first matches the upstream
	// BulkUploadWorker + ReprocessUseCase convention so a config-broken
	// environment surfaces the actionable ErrDispatcherUnavailable signal
	// before the per-call contract violation. Both checks fire before any
	// asset-derivation so a nil rec cannot panic on rec.ID / rec.Source /
	// rec.ContentSHA256. Strict fail-closed at the lifecycle adapter's error
	// propagation boundary.
	//
	// Rec == nil returns a contract violation error at runtime.
	if r.committer == nil {
		return fmt.Errorf("clips registry committer not configured (VECTOR-asset-mutation isolation required): %w", mutations.ErrDispatcherUnavailable)
	}
	if rec == nil {
		return fmt.Errorf("UpsertMedia: MediaRecord is nil (contract violation)")
	}
	lifecycleState := asset.StateActive
	if rec.Status == "DELETED" {
		lifecycleState = asset.StateDeleted
	}

	// MEDIA-SSOT P0-1 (Sept 2026): asset_locations is part of the media aggregate.
	// The entire media row + locations + outbox MUST commit in ONE PG transaction.
	// The previous PG→SQLite two-phase (CommitAndIndex then LocationRepository.Upsert)
	// produced a partial-commit divergence: PG could succeed while SQLite failed and
	// the caller would retry an already-durable asset. The locations ride inside the
	// CommitRequest so the canonical PG committer upserts media_assets +
	// asset_locations + outbox atomically. The SQLite LocationRepository is
	// intentionally NOT called from this path (legacy backfill only).
	var locations []persistence.LocationCommit
	if rec.LocalPath != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: string(asset.LocationKindLocal), URI: rec.LocalPath,
			ContentSHA256: rec.ContentSHA256, IsPrimary: true,
		})
	}
	if rec.DriveLink != "" || rec.DriveFileID != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: string(asset.LocationKindDrive), URI: "drive://" + rec.DriveFileID,
			ExternalID: rec.DriveFileID, WebViewLink: rec.DriveLink, DownloadURL: rec.DownloadLink,
			IsPrimary: rec.LocalPath == "", ContentSHA256: rec.ContentSHA256,
		})
	}
	// PR 7 (June 2026, codex/pgvector-app-writers-fail-closed): route the
	// media_assets UPSERT through the canonical persistence.AssetCommitter
	// so the VECTOR-002 atomicity invariant (media_assets UPSERT + outbox_events
	// INSERT in one tx) applies uniformly to artifacts-driven write paths.
	//
	// MEDIA-IDENTITY (Sept 2026): the committer's ContentHash is the BYTE
	// identity of the artifact, so it receives rec.ContentHash (the SHA-256 of
	// the bytes, computed by the lifecycle's contentAddress). It previously
	// received rec.ContentSHA256 — the caller's compatibility-only digest —
	// which meant a path could compute the canonical SHA-256 and the very next
	// boundary silently overwrite it with an MD5. ContentSHA256 still rides on
	// the asset_locations rows below (read-only compatibility bucket); no
	// decision reads it, and it must never be the durable content address.
	if _, err := r.committer.CommitAndIndex(ctx, persistence.CommitRequest{
		AssetID: rec.ID, Source: rec.Source, Name: rec.Name, Filename: rec.Filename,
		MediaType: rec.MediaType, GroupName: rec.Group,
		ContentHash: rec.ContentHash, LifecycleState: string(lifecycleState),
		IndexState: mediaIndexState(rec.Metadata), Locations: locations, EmitIndexEvent: true,
	}); err != nil {
		return fmt.Errorf("committer enqueue: %w", err)
	}

	// P1-5 (Sept 2026): this processing write MUST NOT make a durable PG
	// media commit fail closed. The media row + locations + outbox are
	// already durably committed in PG above; a processing write failure is
	// best-effort and is logged but does not return an error (the caller
	// already has a durable asset). This eliminates the PG COMMIT -> second
	// write -> return error partial-commit boundary.
	//
	// MEDIA-SSOT write-bridge (Sept 2026): the write now targets the media
	// SSOT through persistence.AssetProcessingWriter (resolved by the
	// composition root from the canonical committer) instead of the
	// operational SQLite mirror, so best-effort no longer means second-engine.
	if rec.Status != "" && r.processing != nil {
		step := string(asset.StageUpload)
		if rec.MediaType == "audio" {
			step = string(asset.StageDownload)
		}
		if err := persistMediaProcessingState(ctx, r.processing, rec, step); err != nil {
			r.logger().Warn("clips registry: processing state best-effort write failed (media commit already durable)",
				zap.String("asset_id", rec.ID), zap.String("step", step), zap.Error(err))
		}
	}

	return nil
}

// mediaIndexState extracts the media_assets.index_state mirror from a
// metadata_json string. Returns empty on parse failure (same tolerance as the
// metadata port).
//
// It decodes into a typed envelope rather than a map[string]any: this runs on
// EVERY UpsertMedia (the ingest hot path), and the map form allocated a map,
// string keys and interface-boxed values just to read one field.
func mediaIndexState(metadataJSON string) string {
	if metadataJSON == "" {
		return ""
	}
	var probe struct {
		IndexState string `json:"index_state"`
	}
	if json.Unmarshal([]byte(metadataJSON), &probe) != nil {
		return ""
	}
	return probe.IndexState
}

func persistMediaProcessingState(ctx context.Context, processing persistence.AssetProcessingWriter, rec *MediaRecord, step string) error {
	if processing == nil {
		return fmt.Errorf("clips registry: processing writer not configured")
	}
	if err := processing.StartAssetProcessing(ctx, rec.ID, step); err != nil {
		return fmt.Errorf("clips registry: start processing %s/%s: %w", rec.ID, step, err)
	}
	switch rec.Status {
	case "failed":
		if err := processing.FailAssetProcessing(ctx, rec.ID, step, rec.Error); err != nil {
			return fmt.Errorf("clips registry: fail processing %s/%s: %w", rec.ID, step, err)
		}
	case "ACTIVE", "completed":
		if err := processing.CompleteAssetProcessing(ctx, rec.ID, step); err != nil {
			return fmt.Errorf("clips registry: complete processing %s/%s: %w", rec.ID, step, err)
		}
	}
	return nil
}

func (r *ClipsRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	if r != nil && r.mediaReader != nil {
		return r.mediaReader.Get(ctx, id)
	}
	if r == nil || r.querySvc == nil {
		// Fail closed: no media read authority is wired, so reporting "not
		// found" would be a successful no-op for an unreadable catalog.
		return nil, fmt.Errorf("clips registry: no media details reader wired (media SSOT closed)")
	}
	details, err := r.querySvc.Get(ctx, id)
	if err != nil {
		if err == asset.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return detailsToMediaRecord(details), nil
}

// DeleteMedia retires the media asset through the canonical retirement port.
//
// It resolves persistence.CanonicalAssetSoftDeleter — the SINGLE owner of that
// decision — and fails closed when the media plane is closed, exactly as
// UpsertMedia already fails closed without a canonical committer. There is no
// SQLite fallback: the previous degrade branch soft-deleted the media row on
// the operational mirror while the SSOT was PostgreSQL, which is the
// split-brain this removal closes (MEDIA-SSOT write-bridge, September 2026).
func (r *ClipsRegistry) DeleteMedia(ctx context.Context, id string) error {
	retirer := persistence.CanonicalAssetSoftDeleter(r.committer)
	if retirer == nil {
		return fmt.Errorf("clips registry: no canonical media retirement port wired (media SSOT closed): %w", mutations.ErrDispatcherUnavailable)
	}
	if err := retirer.SoftDeleteAsset(ctx, id); err != nil {
		return fmt.Errorf("clips registry: retire %s: %w", id, err)
	}
	return nil
}

func (r *ClipsRegistry) GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error) {
	if r == nil || r.mediaReader == nil {
		// MEDIA-SSOT (Sept 2026): no SQLite fallback. The operational mirror
		// holds no committed media rows, so degrading onto it could only ever
		// answer "empty" while PostgreSQL held the assets.
		return nil, fmt.Errorf("clips registry: no canonical media committer wired (media SSOT closed)")
	}
	return r.mediaReader.GetAllWithDriveFileID(ctx)
}

func (r *ClipsRegistry) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	if r == nil || r.mediaReader == nil {
		return "", fmt.Errorf("clips registry: no canonical media committer wired (media SSOT closed)")
	}
	return r.mediaReader.FindByPHash(ctx, phash)
}

// FindByContentHash resolves the live logical asset that owns a physical
// content identity.
//
// The argument is the SHA-256 hex digest of the BYTES (kernel/digest), and the
// lookup matches only the canonical content columns (binary_sha256,
// content_sha256). It deliberately does NOT fall back to content_sha256: that
// column is a compatibility bucket that may hold an MD5, and matching it here
// would make two assets look content-identical when they are not — the exact
// conflation the media-identity programme removes (asset_id decides WHAT an
// asset is, SHA-256 decides WHICH BYTES it is, MD5 decides nothing).
//
// A match means "these bytes are already stored", NOT "this logical asset
// already exists": callers that want the latter must read the row and compare
// its content identity against the asset ID they are about to write. Reusing
// the storage is fine; collapsing the logical asset is not.
//
// The lookup is PostgreSQL-only: the operational SQLite mirror is not a media
// read authority, so a closed media plane fails closed rather than answering
// from a catalog that holds no committed rows.
func (r *ClipsRegistry) FindByContentHash(ctx context.Context, sha256 string) (*MediaRecord, error) {
	digestValue := strings.ToLower(strings.TrimSpace(sha256))
	if digestValue == "" {
		return nil, nil
	}
	if r == nil {
		return nil, fmt.Errorf("clips registry: content lookup unavailable on nil registry")
	}
	if r.mediaReader == nil {
		return nil, fmt.Errorf("clips registry: no canonical media committer wired (media SSOT closed)")
	}
	return r.mediaReader.FindByContentHash(ctx, digestValue)
}

func detailsToMediaRecord(details *asset.Details) *MediaRecord {
	if details == nil || details.Asset == nil {
		return nil
	}
	rec := &MediaRecord{
		ID:                  details.Asset.ID,
		Name:                details.Asset.Name,
		Filename:            details.Asset.Filename,
		Source:              string(details.Asset.Source),
		Category:            details.Asset.Category,
		MediaType:           string(details.Asset.MediaType),
		ExternalURL:         details.Asset.ExternalURL(),
		FolderID:            details.Asset.FolderID(),
		FolderPath:          details.Asset.FolderPath(),
		Group:               details.Asset.Group,
		Tags:                append([]string(nil), details.Asset.Tags...),
		Duration:            int(details.Asset.Duration.Milliseconds()),
		VisualEmbeddingJSON: details.Asset.VisualEmbeddingJSON(),
	}
	rec.Metadata = details.Asset.MetadataJSON()
	// The content address is the SHA-256 byte identity (see the Asset accessor
	// contract); BinarySHA256 prefers the dedicated projection and falls back
	// to ContentHash. It is a different fact from the legacy digest gathered
	// from the locations below.
	rec.ContentHash = details.Asset.BinarySHA256()

	for _, loc := range details.Locations {
		switch loc.LocationKind {
		case asset.LocationKindLocal:
			rec.LocalPath = loc.URI
			rec.ContentSHA256 = loc.ContentSHA256
		case asset.LocationKindDrive:
			rec.DriveFileID = loc.ExternalID
			rec.DriveLink = loc.AccessURL
			rec.DownloadLink = loc.DownloadURL
		}
	}

	for _, proc := range details.Processing {
		if proc != nil {
			if proc.Status == asset.StatusFailed {
				rec.Status = "failed"
				rec.Error = proc.ErrorMessage
				break
			} else if proc.Status == asset.StatusRunning {
				rec.Status = "processing"
			} else if rec.Status == "" && proc.Status == asset.StatusCompleted {
				rec.Status = "ACTIVE"
			}
		}
	}
	if rec.Status == "" {
		rec.Status = "ACTIVE"
	}

	return rec
}
