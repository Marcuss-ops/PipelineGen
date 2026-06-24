package app

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// ── ClipStoreAdapter wraps *assets.ClipsRepository to satisfy youtubeports.ClipStorePort ──

type clipStoreAdapter struct {
	inner *assets.ClipsRepository
}

func newClipStoreAdapter(r *assets.ClipsRepository) youtubeports.ClipStorePort {
	if r == nil {
		return nil
	}
	return &clipStoreAdapter{inner: r}
}

// Compile-time assertion: clipStoreAdapter satisfies youtubeports.ClipStorePort.
// Pattern 0 (AGENTS.md): explicit structural-port assertion so future
// port-extension drift surfaces at compile time, not at first runtime call.
// PG-003 (June 2026): confirmed the adapter satisfies the extended port
// after SearchClipsAdvanced + CountClips were added.
var _ youtubeports.ClipStorePort = (*clipStoreAdapter)(nil)

func (a *clipStoreAdapter) Get(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.Get(ctx, id)
}
func (a *clipStoreAdapter) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	return a.inner.GetClip(ctx, id)
}
func (a *clipStoreAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.inner.Upsert(ctx, clip)
}
func (a *clipStoreAdapter) UpsertFolder(ctx context.Context, f *asset.ClipFolder) error {
	return a.inner.UpsertFolder(ctx, f)
}
func (a *clipStoreAdapter) DeleteClip(ctx context.Context, id string) error {
	return a.inner.DeleteClip(ctx, id)
}
func (a *clipStoreAdapter) GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error) {
	return a.inner.GetFolder(ctx, folderID)
}

// SearchClipsAdvanced routes the orchestrator's domain-typed advanced
// search straight to the concrete repository. Added in PG-003 (June
// 2026) so the youtube handler no longer imports *assets.ClipsRepository.
func (a *clipStoreAdapter) SearchClipsAdvanced(ctx context.Context, req asset.AdvancedSearchRequest) (*asset.AdvancedSearchResult, error) {
	return a.inner.SearchClipsAdvanced(ctx, req)
}

// CountClips counts all non-deleted clips in the store. Added in
// PG-003 (June 2026) so the youtube handler Stats endpoint no longer
// imports *assets.ClipsRepository.
func (a *clipStoreAdapter) CountClips(ctx context.Context) (int, error) {
	return a.inner.CountClips(ctx)
}

func (a *clipStoreAdapter) ListYouTubeClipIDsForSearchText(ctx context.Context, limit, offset int) ([]string, error) {
	query := `SELECT id FROM media_assets WHERE source = 'youtube' AND json_extract(metadata_json, '$.youtube_title') != '' ORDER BY id`
	args := []any{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	if offset > 0 {
		if limit <= 0 {
			query += " LIMIT -1"
		}
		query += " OFFSET ?"
		args = append(args, offset)
	}

	rows, err := a.inner.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ── MonitorsStoreAdapter wraps *assets.MonitorsRepository to satisfy youtubeports.MonitorsStorePort ──

type monitorsStoreAdapter struct {
	inner *assets.MonitorsRepository
}

func newMonitorsStoreAdapter(r *assets.MonitorsRepository) youtubeports.MonitorsStorePort {
	if r == nil {
		return nil
	}
	return &monitorsStoreAdapter{inner: r}
}

func (a *monitorsStoreAdapter) UpsertSource(ctx context.Context, ms *asset.MonitoredSource) error {
	return a.inner.UpsertSource(ctx, ms)
}
func (a *monitorsStoreAdapter) IncrementProcessed(ctx context.Context, id string) error {
	return a.inner.IncrementProcessed(ctx, id)
}

// ── ClipIndexerAdapter wraps *clipindexer.Service to satisfy youtubeports.ClipIndexerPort ──
//
// youtubeports.ClipIndexerPort is an empty-marker port — the struct conformance to
// the interface is at the *clipIndexerAdapter assignment site in
// composition.go. Methods below are present because the application code
// may call .IsEnabled() / .IndexClip() on the port.

type clipIndexerAdapter struct {
	inner *clipindexer.Service
}

func (a *clipIndexerAdapter) IsEnabled() bool { return a.inner.IsEnabled() }
func (a *clipIndexerAdapter) IndexClip(ctx context.Context, id string) error {
	return a.inner.IndexClip(ctx, id)
}

// ── OllamaClientAdapter wraps *client.Client (composition injects the real
//    *client.Client directly; this adapter is reserved for tests/mocks that
//    predate the structural port semantics). ──

type ollamaClientAdapter struct {
	inner interface {
		SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error)
	}
}

func (a *ollamaClientAdapter) SimpleGenerate(ctx context.Context, model, prompt string, timeout time.Duration, opts map[string]any) (string, error) {
	return a.inner.SimpleGenerate(ctx, model, prompt, timeout, opts)
}

// ── DriveFolderMgrAdapter wraps *drive.Uploader to satisfy youtubeports.DriveFolderManagerPort ──
//
// Per PR2 followup (June 2026): the port mandates a *UploadResultDTO return
// type (defined in ports.go) so the application layer never imports
// internal/infrastructure/drive. This adapter converts infra UploadResult →
// DTO at the seam.

type driveFolderMgrAdapter struct {
	uploader *drive.Uploader
	log      *zap.Logger
}

func newDriveFolderMgrAdapter(uploader *drive.Uploader, log *zap.Logger) youtubeports.DriveFolderManagerPort {
	if uploader == nil {
		return nil
	}
	return &driveFolderMgrAdapter{uploader: uploader, log: log}
}

func (a *driveFolderMgrAdapter) GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if a.uploader == nil {
		return parentFolderID, fmt.Errorf("driveFolderMgr: uploader not wired")
	}
	return a.uploader.GetOrCreateFolder(ctx, channelName, parentFolderID)
}

func (a *driveFolderMgrAdapter) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtubeports.UploadResultDTO, bool, error) {
	if a.uploader == nil {
		return nil, false, fmt.Errorf("driveFolderMgr: uploader not wired")
	}
	res, skipped, err := a.uploader.UploadFileIfChanged(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, skipped, err
	}
	if res == nil {
		return nil, skipped, nil
	}
	return &youtubeports.UploadResultDTO{FileID: res.FileID, WebViewLink: res.WebViewLink}, skipped, nil
}

// ── FolderMemoryAdapter wraps *foldermemory.Service to satisfy youtubeports.FolderMemoryPort ──
//
// *foldermemory.Service satisfies the port structurally (LoadManifest,
// SaveManifest, UpdateManifestTXT, ComputeManifestStats). This adapter is
// retained as a stable composition seam so future cache logic can layer
// without changing the port signature.

type folderMemoryAdapter struct {
	inner *foldermemory.Service
}

func newFolderMemoryAdapter(svc *foldermemory.Service) youtubeports.FolderMemoryPort {
	if svc == nil {
		return nil
	}
	return &folderMemoryAdapter{inner: svc}
}

func (a *folderMemoryAdapter) LoadManifest(manifestPath string) (*asset.ClipManifest, error) {
	return a.inner.LoadManifest(manifestPath)
}

func (a *folderMemoryAdapter) SaveManifest(manifestPath string, manifest *asset.ClipManifest) error {
	return a.inner.SaveManifest(manifestPath, manifest)
}

func (a *folderMemoryAdapter) UpdateManifestTXT(folder *asset.ClipFolder, manifest *asset.ClipManifest) error {
	return a.inner.UpdateManifestTXT(folder, manifest)
}

func (a *folderMemoryAdapter) ComputeManifestStats(manifest *asset.ClipManifest) asset.ClipFolderStats {
	return a.inner.ComputeManifestStats(manifest)
}

// ── PR2 (June 2026): SearchRunnerStub removed.
//
// The previous `searchRunnerStub` was a silent-empty fallback that returned
// `[]youtube.SearchLiveResult{}, nil` and `&youtube.DownloaderMetadata{}, nil`
// when the underlying infrastructure was unavailable. That behaviour was
// indistinguishable from a successful empty search and is the failure mode
// explicitly killed by PR2.
//
// The production wiring is now `ytinfra.NewSearchRunnerAdapter(cfg, log)`
// (see internal/infrastructure/youtube/search_runner_adapter.go), which:
//
//   - returns nil at construction time when cfg or log is nil (composition
//     root detects this and returns an error from BuildDomainBundle);
//   - wraps subprocess errors with `ports.ErrSearchRunnerUnavailable`
//     (search) or `ports.ErrSearchRunnerVideoInfoUnavailable` (info) so
//     callers can branch on the cause via `errors.Is`;
//   - propagates ctx.Err() unwrapped so cancellation is detected faithfully.
//
// The previous `newSearchRunnerStub(log)` constructor and its type are
// intentionally NOT retained here. The test file `youtube_adapters_test.go`
// has been migrated to `internal/infrastructure/youtube/search_runner_adapter_test.go`.
// ──
