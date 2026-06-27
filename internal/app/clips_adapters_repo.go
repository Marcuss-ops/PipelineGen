package app

import (
	"context"
	"time"

	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// clipsRepoAdapter wraps *assets.ClipsRepository to satisfy
// clips.ClipRepositoryPort. All 8 methods are exact delegations; the
// adapter intentionally exposes ONLY the surface the handler uses
// (Pattern 0). Three instances are wired by the composition root —
// one per source (artlist, clips, stock) — because the API handler
// uses three separate repo pointers for its cross-source routes.
type clipsRepoAdapter struct {
	inner *assets.ClipsRepository
}

// Compile-time assertion: clipsRepoAdapter satisfies clips.ClipRepositoryPort.
var _ clips.ClipRepositoryPort = (*clipsRepoAdapter)(nil)

func newClipsRepoAdapter(r *assets.ClipsRepository) clips.ClipRepositoryPort {
	if r == nil {
		return nil
	}
	return &clipsRepoAdapter{inner: r}
}

func (a *clipsRepoAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.inner.Upsert(ctx, clip)
}

func (a *clipsRepoAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.Get(ctx, id)
}

func (a *clipsRepoAdapter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.GetClip(ctx, id)
}

func (a *clipsRepoAdapter) ListFolders(ctx context.Context, source string) ([]*asset.ClipFolder, error) {
	return a.inner.ListFolders(ctx, source)
}

func (a *clipsRepoAdapter) GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error) {
	return a.inner.GetFolder(ctx, folderID)
}

func (a *clipsRepoAdapter) GetFolderChildren(ctx context.Context, parentID string) ([]*asset.Asset, error) {
	return a.inner.GetFolderChildren(ctx, parentID)
}

func (a *clipsRepoAdapter) ListByFolderID(ctx context.Context, folderID string) ([]*asset.Asset, error) {
	return a.inner.ListByFolderID(ctx, folderID)
}

func (a *clipsRepoAdapter) ListByFolderPath(ctx context.Context, folderPath string) ([]*asset.Asset, error) {
	return a.inner.ListByFolderPath(ctx, folderPath)
}

func (a *clipsRepoAdapter) DeleteFolder(ctx context.Context, id string) error {
	return a.inner.DeleteFolder(ctx, id)
}

func (a *clipsRepoAdapter) BulkAddTags(ctx context.Context, ids, tags []string) error {
	return a.inner.BulkAddTags(ctx, ids, tags)
}

func (a *clipsRepoAdapter) BulkRemoveTags(ctx context.Context, ids, tags []string) error {
	return a.inner.BulkRemoveTags(ctx, ids, tags)
}

func (a *clipsRepoAdapter) ListClipsPaged(ctx context.Context, source string, limit, offset int, query string) ([]*asset.Asset, error) {
	return a.inner.ListClipsPaged(ctx, source, limit, offset, query)
}

func (a *clipsRepoAdapter) FindClipsByHash(ctx context.Context, hash string) ([]*asset.Asset, error) {
	return a.inner.FindClipsByHash(ctx, hash)
}

// ── VoiceoverRepository adapter ──────────────────────────────────

// voiceoverRepoAdapter wraps *assets.VoiceoversRepository to satisfy
// clips.VoiceoverRepositoryPort. Only 3 methods are exposed —
// GetByID + ListAll + Upsert — because those are the only ones the
// clips handler dispatches voiceover source through.
//
// PG-005 (June 2026): returns/takes clips.ClipVoiceoverRecordDTO (a
// application-layer DTO that mirrors the 22-column voiceovers
// schema). The converter is inlined here as voiceoverRecordToDTO /
// voiceoverDTOToRecord so the api/ + application/ layers see only the
// DTO while the adapter keeps the concrete SQLite contract at the
// infra seam.
type voiceoverRepoAdapter struct {
	inner *assets.VoiceoversRepository
}

// Compile-time assertion: voiceoverRepoAdapter satisfies clips.VoiceoverRepositoryPort.
var _ clips.VoiceoverRepositoryPort = (*voiceoverRepoAdapter)(nil)

func newVoiceoverRepoAdapter(r *assets.VoiceoversRepository) clips.VoiceoverRepositoryPort {
	if r == nil {
		return nil
	}
	return &voiceoverRepoAdapter{inner: r}
}

func (a *voiceoverRepoAdapter) GetByID(ctx context.Context, id string) (*clips.ClipVoiceoverRecordDTO, error) {
	rec, err := a.inner.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return voiceoverRecordToDTO(rec), nil
}

func (a *voiceoverRepoAdapter) ListAll(ctx context.Context) ([]*clips.ClipVoiceoverRecordDTO, error) {
	recs, err := a.inner.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*clips.ClipVoiceoverRecordDTO, len(recs))
	for i, r := range recs {
		out[i] = voiceoverRecordToDTO(r)
	}
	return out, nil
}

func (a *voiceoverRepoAdapter) Upsert(ctx context.Context, dto *clips.ClipVoiceoverRecordDTO) error {
	if dto == nil {
		return nil
	}
	return a.inner.Upsert(ctx, voiceoverDTOToRecord(dto))
}

// voiceoverRecordToDTO projects the concrete infra *assets.Record
// onto the canonical application-layer *ClipVoiceoverRecordDTO. The
// 22 fields are in 1:1 correspondence; RFC3339 timestamps are kept
// as strings to keep the DTO free of time.Time (a typed value the
// api layer cares nothing about).
func voiceoverRecordToDTO(rec *assets.Record) *clips.ClipVoiceoverRecordDTO {
	if rec == nil {
		return nil
	}
	return &clips.ClipVoiceoverRecordDTO{
		ID:              rec.ID,
		RequestID:       rec.RequestID,
		TextHash:        rec.TextHash,
		TextPreview:     rec.TextPreview,
		Language:        rec.Language,
		Voice:           rec.Voice,
		Filename:        rec.Filename,
		LocalPath:       rec.LocalPath,
		CleanedPath:     rec.CleanedPath,
		FolderID:        rec.FolderID,
		FolderPath:      rec.FolderPath,
		DriveFileID:     rec.DriveFileID,
		DriveLink:       rec.DriveLink,
		DownloadLink:    rec.DownloadLink,
		FileHash:        rec.FileHash,
		DurationSeconds: rec.DurationSeconds,
		Status:          rec.Status,
		Error:           rec.Error,
		Strategy:        rec.Strategy,
		Metadata:        rec.Metadata,
		CreatedAtRFC:    rec.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAtRFC:    rec.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// voiceoverDTOToRecord inverts the projection above. Empty Created/Updated
// timestamps cause the repository to populate them on insert (matching
// the existing Upsert semantics in voiceovers_repository.go).
func voiceoverDTOToRecord(dto *clips.ClipVoiceoverRecordDTO) *assets.Record {
	return &assets.Record{
		ID:              dto.ID,
		RequestID:       dto.RequestID,
		TextHash:        dto.TextHash,
		TextPreview:     dto.TextPreview,
		Language:        dto.Language,
		Voice:           dto.Voice,
		Filename:        dto.Filename,
		LocalPath:       dto.LocalPath,
		CleanedPath:     dto.CleanedPath,
		FolderID:        dto.FolderID,
		FolderPath:      dto.FolderPath,
		DriveFileID:     dto.DriveFileID,
		DriveLink:       dto.DriveLink,
		DownloadLink:    dto.DownloadLink,
		FileHash:        dto.FileHash,
		DurationSeconds: dto.DurationSeconds,
		Status:          dto.Status,
		Error:           dto.Error,
		Strategy:        dto.Strategy,
		Metadata:        dto.Metadata,
	}
}

// ── ImageRepository adapter ──────────────────────────────────────

// imageRepoAdapter wraps *assets.ImagesRepository to satisfy
// clips.ImageRepositoryPort. Only ListAll is exposed because Cleanup()
// is the only callsite for images source on the clips route surface.
type imageRepoAdapter struct {
	inner *assets.ImagesRepository
}

// Compile-time assertion: imageRepoAdapter satisfies clips.ImageRepositoryPort.
var _ clips.ImageRepositoryPort = (*imageRepoAdapter)(nil)

func newImageRepoAdapter(r *assets.ImagesRepository) clips.ImageRepositoryPort {
	if r == nil {
		return nil
	}
	return &imageRepoAdapter{inner: r}
}

func (a *imageRepoAdapter) ListAll(ctx context.Context) ([]*asset.ImageAsset, error) {
	return a.inner.ListAll(ctx)
}

// ── CleanupService adapter ────────────────────────────────────────

// clipsCleanupAdapter wraps *deletion.DeletionService to satisfy
// clips.CleanupServicePort. The port exposes only CleanupOrphanFiles
// and DeleteClip — the two methods ClipOpsService needs.
type clipsCleanupAdapter struct {
	inner *deletion.DeletionService
}

var _ clips.CleanupServicePort = (*clipsCleanupAdapter)(nil)

func (a *clipsCleanupAdapter) CleanupOrphanFiles(ctx context.Context, path string, dryRun bool) (int, error) {
	return a.inner.CleanupOrphanFiles(ctx, path, dryRun)
}

func (a *clipsCleanupAdapter) DeleteClip(ctx context.Context, source, clipID string, hardDelete bool) error {
	return a.inner.DeleteClip(ctx, source, clipID, hardDelete)
}

// ── JobsService adapter ───────────────────────────────────────────

// clipsJobsServiceAdapter wraps job.Service to satisfy
// clips.JobsServicePort. Translates the canonical EnqueueRequest
// into the minimal DTO the port exposes.
type clipsJobsServiceAdapter struct {
	inner job.Service
}

var _ clips.JobsServicePort = (*clipsJobsServiceAdapter)(nil)

func (a *clipsJobsServiceAdapter) Enqueue(ctx context.Context, req clips.JobsEnqueueRequest) (*clips.JobsEnqueueResponse, error) {
	domainReq := &job.EnqueueRequest{
		Type:      req.Type,
		Payload:   req.Payload,
		Priority:  req.Priority,
		ActiveKey: req.ActiveKey,
	}
	j, err := a.inner.Enqueue(ctx, domainReq)
	if err != nil {
		return nil, err
	}
	return &clips.JobsEnqueueResponse{ID: j.ID}, nil
}

// ── SourceResolver adapter for ClipOpsService ─────────────────────

// clipOpsSourceResolverAdapter wraps a map of source→ClipRepositoryPort
// to satisfy clips.SourceResolverPort. Used by ClipOpsService to resolve
// the correct repository for a given source string.
type clipOpsSourceResolverAdapter struct {
	repos map[string]clips.ClipRepositoryPort
}

var _ clips.SourceResolverPort = (*clipOpsSourceResolverAdapter)(nil)

func (a *clipOpsSourceResolverAdapter) ResolveRepo(source string) clips.ClipRepositoryPort {
	if a == nil || a.repos == nil {
		return nil
	}
	return a.repos[source]
}
