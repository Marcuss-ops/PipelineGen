// Package app — Clips API adapters (PG-005, June 2026).
//
// Before PG-005 the 7 files under internal/api/assets/clips/**
// reached through 8 concrete internal/infrastructure/* types:
//
//   - *config.Config                (cfg wiring)
//   - *assets.ClipsRepository       (DB access for clips/artlist/stock sources)
//   - *assets.VoiceoversRepository  (DB access for voiceover source)
//   - *assets.ImagesRepository      (DB access for images source)
//   - *drive.Uploader               (Drive upload + folder + raw Files.List Q query)
//   - *semantic.MetadataWriter      (LLM semantic enrichment payload)
//   - *clipindexer.Service          (Qdrant clip embedding + indexer)
//   - *foldermemory.Service         (manifest regeneration heuristics)
//
// Plus: *artifacts.SourceResolver storing 3 concrete *assets.ClipsRepository
// pointers internally. Plus: hashutil.MD5File.
//
// Every one of those reaches-throughs now flows through a typed port
// declared in `internal/application/clips/ports.go`. The adapter layer
// below is the ONE place allowed to import the concrete infra types
// (per AGENTS.md Pattern 0 + the composition-root convention used by
// PG-002/003/004). The api/ layer is now strictly application+domain+
// package-only.
//
// Each adapter carries a `var _ clips.<Port> = (*<Adapter>)(nil)`
// assertion so future port drift is caught at compile time. Nil-tolerant
// constructors preserve the `if h.xy != nil` discipline callers have
// relied on since the Wave 14 grandfathered era.
package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	clips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// ── Config adapter ────────────────────────────────────────────────

// clipsCfgAdapter wraps *config.Config to satisfy clips.ClipConfigPort.
// Each method exposes exactly the field the handler reads (Pattern 0:
// minimal — never return the whole *Config). The delegation order
// matches the canonical accessors on *config.Config:
//
//	cfg.Drive.X   for ClipsDriveFolder / ArtlistDriveFolder / StockDriveFolder
//	cfg.Storage.X for MediaPath / TempPath / DataDir / YoutubeClipsPath / AssetsPath
type clipsCfgAdapter struct {
	cfg *config.Config
}

// Compile-time assertion: clipsCfgAdapter satisfies clips.ClipConfigPort.
var _ clips.ClipConfigPort = (*clipsCfgAdapter)(nil)

func newClipsCfgAdapter(cfg *config.Config) clips.ClipConfigPort {
	if cfg == nil {
		return nil
	}
	return &clipsCfgAdapter{cfg: cfg}
}

func (a *clipsCfgAdapter) ClipsDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ClipsFolder()
}

func (a *clipsCfgAdapter) RootFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.RootFolder()
}

func (a *clipsCfgAdapter) ArtlistDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ArtlistFolder()
}

func (a *clipsCfgAdapter) StockDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.StockFolder()
}

func (a *clipsCfgAdapter) MediaPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.MediaPath()
}

func (a *clipsCfgAdapter) TempPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.TempPath()
}

func (a *clipsCfgAdapter) DataDir() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.DataDir
}

func (a *clipsCfgAdapter) YoutubeClipsPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.YoutubeClipsPath()
}

func (a *clipsCfgAdapter) AssetsPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.AssetsPath()
}

func (a *clipsCfgAdapter) AssetsStoragePath() string {
	return a.AssetsPath()
}

// ── ClipsRepository adapter ──────────────────────────────────────

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

func (a *clipsRepoAdapter) UpsertClip(ctx context.Context, clip *asset.Asset) error {
	return a.inner.UpsertClip(ctx, clip)
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

// ── Drive Uploader adapter ───────────────────────────────────────

// clipsDriveAdapter wraps *drive.Uploader to satisfy
// clips.ClipDriveUploaderPort. ListFiles propagates the raw Drive
// query string and projects the SDK File slice into the narrower
// ClipDriveFileDTO shape (only ID + Name consumed by the handler).
// GetFileMeta projects the SDK File into ClipDriveFileMetaDTO
// (only MimeType consumed).
type clipsDriveAdapter struct {
	up *drive.Uploader
}

// Compile-time assertion: clipsDriveAdapter satisfies clips.ClipDriveUploaderPort.
var _ clips.ClipDriveUploaderPort = (*clipsDriveAdapter)(nil)

func newClipsDriveAdapter(up *drive.Uploader) clips.ClipDriveUploaderPort {
	if up == nil {
		return nil
	}
	return &clipsDriveAdapter{up: up}
}

func (a *clipsDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error) {
	if a.up == nil {
		return "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.GetOrCreateFolder(ctx, name, parentFolderID)
}

func (a *clipsDriveAdapter) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if a.up == nil {
		return "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.GetFolderName(ctx, folderID)
}

func (a *clipsDriveAdapter) TrashFolder(ctx context.Context, folderID string) error {
	if a.up == nil {
		return fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.TrashFolder(ctx, folderID)
}

func (a *clipsDriveAdapter) DeleteFolder(ctx context.Context, folderID string) error {
	if a.up == nil {
		return fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.DeleteFolder(ctx, folderID)
}

func (a *clipsDriveAdapter) UploadFile(ctx context.Context, localPath, folderID, filename string) (*clips.ClipUploadResultDTO, error) {
	if a.up == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	res, err := a.up.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, err
	}
	return driveUploadToDTO(res), nil
}

func (a *clipsDriveAdapter) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*clips.ClipUploadResultDTO, error) {
	if a.up == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	res, err := a.up.UploadFileWithDescription(ctx, localPath, folderID, filename, description)
	if err != nil {
		return nil, err
	}
	return driveUploadToDTO(res), nil
}

func (a *clipsDriveAdapter) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if a.up == nil {
		return nil, "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.DownloadFile(ctx, fileID)
}

func (a *clipsDriveAdapter) GetFileMD5(ctx context.Context, fileID string) (string, error) {
	if a.up == nil {
		return "", fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.GetFileMD5(ctx, fileID)
}

func (a *clipsDriveAdapter) GetFileMeta(ctx context.Context, fileID string) (*clips.ClipDriveFileMetaDTO, error) {
	if a.up == nil || a.up.Service == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	f, err := a.up.Service.Files.Get(fileID).Context(ctx).Fields("mimeType").Do()
	if err != nil {
		return nil, err
	}
	if f == nil {
		return &clips.ClipDriveFileMetaDTO{}, nil
	}
	return &clips.ClipDriveFileMetaDTO{MimeType: f.MimeType}, nil
}

func (a *clipsDriveAdapter) TrashFile(ctx context.Context, fileID string) error {
	if a.up == nil {
		return fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	return a.up.TrashFile(ctx, fileID)
}

// ListFiles projects drive.Service.Files.List().Q(query)
// .Fields("files(id, name)").Context(ctx).Do() into the narrower
// ClipDriveFileDTO shape. The handler caller is responsible for
// including `trashed = false` in the query string.
func (a *clipsDriveAdapter) ListFiles(ctx context.Context, query string) ([]clips.ClipDriveFileDTO, error) {
	if a.up == nil || a.up.Service == nil {
		return nil, fmt.Errorf("clipsDriveAdapter: uploader not wired")
	}
	list, err := a.up.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	if list == nil {
		return []clips.ClipDriveFileDTO{}, nil
	}
	out := make([]clips.ClipDriveFileDTO, len(list.Files))
	for i, f := range list.Files {
		out[i] = clips.ClipDriveFileDTO{ID: f.Id, Name: f.Name}
	}
	return out, nil
}

// driveUploadToDTO is the projection helper that maps drive.UploadResult
// onto the narrower clips.ClipUploadResultDTO. Drop any field the
// HTTP transport doesn't consume (Pattern 0 minimal projection).
func driveUploadToDTO(res *drive.UploadResult) *clips.ClipUploadResultDTO {
	if res == nil {
		return &clips.ClipUploadResultDTO{}
	}
	return &clips.ClipUploadResultDTO{
		FileID:       res.FileID,
		WebViewLink:  res.WebViewLink,
		DownloadLink: res.DownloadLink,
		MD5Checksum:  res.MD5Checksum,
	}
}

// ── Meta writer adapter ──────────────────────────────────────────

// clipMetaWriterAdapter wraps *semantic.MetadataWriter to satisfy
// clips.ClipMetaWriterPort. GeneratePayload translates the narrowed
// ClipMetaWriteRequest → concrete semantic.WriteRequest at the
// adapter boundary, executes the call, and projects the result onto
// ClipMetaPayload so callers never import the SDK.
type clipMetaWriterAdapter struct {
	inner *semantic.MetadataWriter
}

// Compile-time assertion: clipMetaWriterAdapter satisfies clips.ClipMetaWriterPort.
var _ clips.ClipMetaWriterPort = (*clipMetaWriterAdapter)(nil)

func newClipMetaWriterAdapter(w *semantic.MetadataWriter) clips.ClipMetaWriterPort {
	if w == nil {
		return nil
	}
	return &clipMetaWriterAdapter{inner: w}
}

func (a *clipMetaWriterAdapter) GeneratePayload(ctx context.Context, req clips.ClipMetaWriteRequest) (*clips.ClipMetaPayload, string, error) {
	concreteReq := semantic.WriteRequest{
		AssetID:   req.AssetID,
		AssetType: req.AssetType,
		MediaType: req.MediaType,
		Source:    req.Source,
		Generator: req.Generator,
		Style:     req.Style,
		Prompt:    req.Prompt,
		LocalPath: req.LocalPath,
	}
	payload, status, err := a.inner.GeneratePayload(ctx, concreteReq)
	if err != nil {
		return nil, status, err
	}
	if payload == nil {
		return nil, status, nil
	}
	return &clips.ClipMetaPayload{
		SearchText:          payload.SearchText,
		Tags:                payload.Tags,
		SemanticDescription: payload.SemanticDescription,
		RetrievalScore:      payload.RetrievalScore,
	}, status, nil
}

// ── Clip indexer adapter ─────────────────────────────────────────

// clipsIndexerAdapter wraps *clipindexer.Service to satisfy
// clips.ClipIndexerPort. IsEnabled + IndexClip are the only methods
// the clips bulk-upload worker + EnrichUseCase call.
type clipsIndexerAdapter struct {
	inner *clipindexer.Service
}

// Compile-time assertion: clipsIndexerAdapter satisfies clips.ClipIndexerPort.
var _ clips.ClipIndexerPort = (*clipsIndexerAdapter)(nil)

func newClipsIndexerAdapter(svc *clipindexer.Service) clips.ClipIndexerPort {
	if svc == nil {
		return nil
	}
	return &clipsIndexerAdapter{inner: svc}
}

func (a *clipsIndexerAdapter) IsEnabled() bool {
	if a.inner == nil {
		return false
	}
	return a.inner.IsEnabled()
}

func (a *clipsIndexerAdapter) IndexClip(ctx context.Context, id string) error {
	if a.inner == nil {
		return fmt.Errorf("clipsIndexerAdapter: indexer not wired")
	}
	return a.inner.IndexClip(ctx, id)
}

func (a *clipsIndexerAdapter) BatchReindex(ctx context.Context, source, mediaType string, limit int) (*clips.ClipIndexBatchResultDTO, error) {
	if a.inner == nil {
		return nil, fmt.Errorf("clipsIndexerAdapter: indexer not wired")
	}
	res, err := a.inner.BatchReindex(ctx, source, mediaType, limit)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return &clips.ClipIndexBatchResultDTO{}, nil
	}
	return &clips.ClipIndexBatchResultDTO{
		Total:    res.Total,
		Indexed:  res.Indexed,
		Skipped:  res.Skipped,
		Failed:   res.Failed,
		AssetIDs: res.AssetIDs,
	}, nil
}

// ── Folder memory adapter (empty marker) ─────────────────────────

// clipsFolderMemoryAdapter wraps *foldermemory.Service to satisfy
// clips.ClipFolderMemoryPort. The interface is currently empty —
// the handler stores the dependency but does not call any method,
// and the adapter is the seam that future consumers extend with
// LoadManifest / SaveManifest / UpdateManifestTXT /
// ComputeManifestStats as needed (one PR at a time).
type clipsFolderMemoryAdapter struct {
	inner *foldermemory.Service
}

// Compile-time assertion: clipsFolderMemoryAdapter satisfies clips.ClipFolderMemoryPort.
var _ clips.ClipFolderMemoryPort = (*clipsFolderMemoryAdapter)(nil)

func newClipsFolderMemoryAdapter(svc *foldermemory.Service) clips.ClipFolderMemoryPort {
	if svc == nil {
		return nil
	}
	return &clipsFolderMemoryAdapter{inner: svc}
}

// ── Hash adapter ─────────────────────────────────────────────────

// clipsHashAdapter wraps hashutil.MD5File behind clips.ClipHashPort
// so the bulk_upload_worker code path doesn't have to import
// "internal/infrastructure/files" wholesale. MD5File is the only
// call site; expanding the surface must land via a new port method.
type clipsHashAdapter struct{}

// Compile-time assertion: clipsHashAdapter satisfies clips.ClipHashPort.
var _ clips.ClipHashPort = (*clipsHashAdapter)(nil)

func newClipsHashAdapter() clips.ClipHashPort {
	return &clipsHashAdapter{}
}

func (a *clipsHashAdapter) MD5File(path string) (string, error) {
	return files.MD5File(path)
}

// ── Source resolver adapter ──────────────────────────────────────

// sourceResolverAdapter wraps the composition-root
// *artifacts.SourceResolver and re-projects its 3 internal repo
// pointers through clips.ClipRepositoryPort, so the handler's
// repoForSource(source string) ClipRepositoryPort stays port-pure.
// The 3 adapter slots MUST be created from the same concrete repos
// the resolver holds internally; otherwise the canonical-source
// mime types desync from the actual repo the resolver returns.
type sourceResolverAdapter struct {
	artlist clips.ClipRepositoryPort
	clips   clips.ClipRepositoryPort
	stock   clips.ClipRepositoryPort
}

// Compile-time assertion: sourceResolverAdapter satisfies clips.SourceResolverPort.
var _ clips.SourceResolverPort = (*sourceResolverAdapter)(nil)

func newSourceResolverAdapter(
	artlistRepo clips.ClipRepositoryPort,
	clipsRepo clips.ClipRepositoryPort,
	stockRepo clips.ClipRepositoryPort,
) clips.SourceResolverPort {
	return &sourceResolverAdapter{
		artlist: artlistRepo,
		clips:   clipsRepo,
		stock:   stockRepo,
	}
}

// ResolveRepo returns the canonical-source repo as a port.
// Mirrors *artifacts.SourceResolver.ResolveRepo with one swap:
// the return type is clips.ClipRepositoryPort (port), not
// *assets.ClipsRepository (concrete infra). The handoff table maps
// aliases "youtube"/"clips"/"sound_effect" to the clipsRepo slot and
// "all"/"unified" to the clipsRepo primary access point. Voiceover
// and images resolve to nil (the handler deals with those via the
// separate VoiceoverRepositoryPort + ImageRepositoryPort slots).
func (r *sourceResolverAdapter) ResolveRepo(source string) clips.ClipRepositoryPort {
	canonical := artifacts.CanonicalSource(source)
	switch canonical {
	case "artlist":
		return r.artlist
	case "clips", "youtube", "sound_effect":
		return r.clips
	case "stock":
		return r.stock
	case "all", "unified":
		return r.clips
	default:
		return nil
	}
}

// ── Vector store adapter ─────────────────────────────────────────
//
// clipsVectorAdapter removed — the vector
// capability was deleted.

// ── Asset tree adapter ───────────────────────────────────────────

// clipsAssetTreeAdapter wraps *assettree.Service to satisfy
// clips.ClipTreeBuilderPort. UpsertFromAsset bridges the domain
// *asset.Asset → concrete *assets.AssetNode at the infra seam, so
// internal/application/clips has zero infra imports. The node shape
// conversion lives here (not in the use case) because it touches the
// infrastructure type.
type clipsAssetTreeAdapter struct {
	inner *assettree.Service
}

// Compile-time assertion: clipsAssetTreeAdapter satisfies clips.ClipTreeBuilderPort.
var _ clips.ClipTreeBuilderPort = (*clipsAssetTreeAdapter)(nil)

func newClipsAssetTreeAdapter(svc *assettree.Service) clips.ClipTreeBuilderPort {
	if svc == nil {
		return nil
	}
	return &clipsAssetTreeAdapter{inner: svc}
}

// UpsertFromAsset converts *asset.Asset → *assets.AssetNode and calls
// the underlying assettree.Service.UpsertNode. Nil-tolerant: nil clip
// is a no-op so callers don't need defensive nil checks before every
// call.
func (a *clipsAssetTreeAdapter) UpsertFromAsset(ctx context.Context, clip *asset.Asset) error {
	if a.inner == nil || clip == nil {
		return nil
	}
	return a.inner.UpsertNode(ctx, clipToAssetNode(clip))
}

// clipToAssetNode is the domain-to-infra asset-node converter. PG-005
// (June 2026): moved from internal/application/clips/bulk_tags.go
// into the adapter layer so the use case has zero infra imports.
// Updates to the asset-tree node shape land here as the only place
// that knows about both the domain *asset.Asset and the concrete
// *assets.AssetNode.
func clipToAssetNode(clip *asset.Asset) *assets.AssetNode {
	if clip == nil {
		return nil
	}
	nodeType := "file"
	if clip.IsFolder() {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = string(clip.MediaType)
	}
	return &assets.AssetNode{
		ID:          clip.ID,
		Source:      string(clip.Source),
		AssetID:     clip.ID,
		Name:        clip.Name,
		Type:        nodeType,
		ParentID:    clip.ParentFolderID(),
		Path:        clip.FolderPath(),
		Depth:       clip.Depth(),
		IsFolder:    clip.IsFolder(),
		DriveFileID: clip.DriveFileID(),
		DriveLink:   clip.DriveLink(),
		Metadata:    clip.MetadataJSON(),
		CreatedAt:   clip.CreatedAt,
		UpdatedAt:   clip.UpdatedAt,
		ChildCount:  clip.ChildCount(),
	}
}

// ── Wiring sentinel ──────────────────────────────────────────────
//
// PG-005 (June 2026): newClipsAdapterBundle is the canonical
// composition-root constructor for the clips API deps. It returns
// a bundle of typed ports so module_assets.go can hand them to
// clipsapi.NewHandler in a single struct literal. Nil-tolerant —
// production wiring passes all concrete deps; tests can pass nil
// for any subset and observe the matching `if h.xy != nil` short-
// circuit behaviour the handler code has long relied on.

type clipsAdapterBundle struct {
	Cfg            clips.ClipConfigPort
	SourceResolver clips.SourceResolverPort
	ClipsRepo      clips.ClipRepositoryPort
	StockRepo      clips.ClipRepositoryPort
	ArtlistRepo    clips.ClipRepositoryPort
	VoiceoverRepo  clips.VoiceoverRepositoryPort
	ImagesRepo     clips.ImageRepositoryPort
	DriveUploader  clips.ClipDriveUploaderPort
	MetaWriter     clips.ClipMetaWriterPort
	ClipIndexer    clips.ClipIndexerPort
	FolderMemSvc   clips.ClipFolderMemoryPort
	HashSvc        clips.ClipHashPort
	TreeBuilderSvc clips.ClipTreeBuilderPort
}

// newClipsAdapterBundle wires the 11 concrete deps into typed ports.
// vectorSvc arg removed from this constructor.
//
// The configuration/log parameters are only retained for future
// adapters that need them; today's 11 adapters are bootstrap-pure.
func newClipsAdapterBundle(
	cfg *config.Config,
	log *zap.Logger,
	artlistRepo *assets.ClipsRepository,
	clipsRepo *assets.ClipsRepository,
	stockRepo *assets.ClipsRepository,
	voiceoverRepo *assets.VoiceoversRepository,
	imagesRepo *assets.ImagesRepository,
	driveUp *drive.Uploader,
	metaWriter *semantic.MetadataWriter,
	clipIndexer *clipindexer.Service,
	folderMemSvc *foldermemory.Service,
	assetTreeSvc *assettree.Service,
	_ /* vectorSvc removed PG-034 */ any,
) clipsAdapterBundle {
	_ = log // reserved for future adapters that need a logger
	artPort := newClipsRepoAdapter(artlistRepo)
	clpPort := newClipsRepoAdapter(clipsRepo)
	stockPort := newClipsRepoAdapter(stockRepo)
	return clipsAdapterBundle{
		Cfg:            newClipsCfgAdapter(cfg),
		SourceResolver: newSourceResolverAdapter(artPort, clpPort, stockPort),
		ClipsRepo:      clpPort,
		StockRepo:      stockPort,
		ArtlistRepo:    artPort,
		VoiceoverRepo:  newVoiceoverRepoAdapter(voiceoverRepo),
		ImagesRepo:     newImageRepoAdapter(imagesRepo),
		DriveUploader:  newClipsDriveAdapter(driveUp),
		MetaWriter:     newClipMetaWriterAdapter(metaWriter),
		ClipIndexer:    newClipsIndexerAdapter(clipIndexer),
		FolderMemSvc:   newClipsFolderMemoryAdapter(folderMemSvc),
		HashSvc:        newClipsHashAdapter(),
		TreeBuilderSvc: newClipsAssetTreeAdapter(assetTreeSvc),
	}
}
