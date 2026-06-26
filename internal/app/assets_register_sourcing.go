package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
)

func newAssetRegisterService(
	cfg *config.Config,
	log *zap.Logger,
	clipsRepo *assetsrepo.ClipsRepository,
	driveUploader *driveutil.Uploader,
	assetTreeSvc *assettree.Service,
	providerRegistry *providers.Registry,
	clipsHandler *clipsapi.Handler,
	dispatcher *outbox.Dispatcher,
) *sourcing.Service {
	var indexDisp sourcing.IndexDispatcherPort
	if dispatcher != nil {
		indexDisp = &sourcingDispatcherAdapter{disp: dispatcher}
	}
	return sourcing.NewService(
		&sourcingFetchAdapter{registry: providerRegistry},
		&sourcingDriveAdapter{uploader: driveUploader},
		&sourcingClipStoreAdapter{repo: clipsRepo},
		nil,
		nil,
		&sourcingHashAdapter{},
		&sourcingTranscriberAdapter{cfg: cfg, log: log},
		&sourcingAssetTreeAdapter{svc: assetTreeSvc},
		&sourcingSearchAdapter{registry: providerRegistry},
		&sourcingConfigAdapter{cfg: cfg},
		&sourcingEnrichmentAdapter{handler: clipsHandler},
		&sourcingMetadataAdapter{cfg: cfg, uploader: driveUploader, log: log},
		indexDisp,
		&zapSourcingLogger{log: log},
	)
}

type sourcingFetchAdapter struct {
	registry *providers.Registry
}

func (a *sourcingFetchAdapter) Fetch(ctx context.Context, req sourcing.FetchRequest) (*sourcing.FetchedAsset, error) {
	if a.registry == nil {
		return nil, fmt.Errorf("register fetch provider registry not configured")
	}
	for _, p := range a.registry.ByCapability(providers.CapabilityFetch) {
		if p.Name() != "youtube" {
			continue
		}
		fp, ok := p.(providers.FetchProvider)
		if !ok {
			continue
		}
		res, err := fp.Fetch(ctx, providers.FetchRequest{
			AssetID:      req.AssetID,
			SourceRef:    req.SourceRef,
			SegmentStart: req.SegmentStart,
			SegmentEnd:   req.SegmentEnd,
		})
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, nil
		}
		out := &sourcing.FetchedAsset{
			LocalPath: res.LocalPath,
			AssetID:   req.AssetID,
			Name:      "",
			Duration:  0,
			Bytes:     res.Bytes,
			Metadata:  map[string]string{},
		}
		if res.Asset != nil {
			out.AssetID = res.Asset.ID
			out.Name = res.Asset.Name
			out.Duration = res.Asset.Duration
			out.Metadata = map[string]string{}
		}
		return out, nil
	}
	return nil, fmt.Errorf("youtube fetch provider not registered")
}

type sourcingDriveAdapter struct {
	uploader *driveutil.Uploader
}

func (a *sourcingDriveAdapter) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*sourcing.DriveUploadResult, error) {
	if a.uploader == nil {
		return nil, fmt.Errorf("drive uploader not configured")
	}
	res, err := a.uploader.UploadFileWithDescription(ctx, localPath, folderID, filename, description)
	if err != nil || res == nil {
		return nil, err
	}
	return &sourcing.DriveUploadResult{
		FileID:       res.FileID,
		WebViewLink:  res.WebViewLink,
		DownloadLink: res.DownloadLink,
	}, nil
}

func (a *sourcingDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if a.uploader == nil {
		return parentID, fmt.Errorf("drive uploader not configured")
	}
	return a.uploader.GetOrCreateFolder(ctx, name, parentID)
}

func (a *sourcingDriveAdapter) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if a.uploader == nil {
		return "", fmt.Errorf("drive uploader not configured")
	}
	return a.uploader.GetFolderName(ctx, folderID)
}

type sourcingClipStoreAdapter struct {
	repo *assetsrepo.ClipsRepository
}

func (a *sourcingClipStoreAdapter) FindByName(ctx context.Context, name string) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	return a.repo.FindByName(ctx, name)
}

func (a *sourcingClipStoreAdapter) FindExisting(ctx context.Context, videoID, url string, startSec, endSec float64) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	hasSegment := endSec > startSec
	if videoID != "" {
		if id, err := a.repo.FindByYouTubeVideoID(ctx, videoID, hasSegment, startSec, endSec); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	if url != "" && !hasSegment {
		if id, err := a.repo.FindBySourceURL(ctx, url); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (a *sourcingClipStoreAdapter) GetClip(ctx context.Context, id string) (*sourcing.ExistingClip, error) {
	if a.repo == nil {
		return nil, nil
	}
	clip, err := a.repo.GetClip(ctx, id)
	if err != nil || clip == nil {
		return nil, err
	}
	return toExistingClip(clip), nil
}

func (a *sourcingClipStoreAdapter) UpsertClip(ctx context.Context, clip *sourcing.ExistingClip) error {
	if a.repo == nil || clip == nil {
		return nil
	}
	return a.repo.UpsertClip(ctx, fromExistingClip(clip))
}

type sourcingHashAdapter struct{}

func (a *sourcingHashAdapter) MD5File(path string) (string, error) {
	return hashutil.MD5File(path)
}

type sourcingTranscriberAdapter struct {
	cfg *config.Config
	log *zap.Logger
}

func (a *sourcingTranscriberAdapter) Transcribe(ctx context.Context, audioPath string) (string, string, error) {
	if a.cfg == nil {
		return "", "", fmt.Errorf("register transcriber config not configured")
	}
	if audioPath == "" {
		return "", "", nil
	}
	if _, err := executil.LookPath("python3"); err != nil {
		return "", "", err
	}
	scriptPath := filepath.Join(a.cfg.Paths.PythonScriptsDir, "tools", "transcribe_detect_lang.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return "", "", err
	}
	res, err := executil.RunSimple(ctx, "python3", scriptPath, "--transcribe", "--model", "tiny", "--json-only", audioPath)
	if err != nil {
		return "", "", err
	}
	type transcriptResult struct {
		Language       string `json:"language"`
		TranscriptFull string `json:"transcript_full"`
		Error          string `json:"error"`
	}
	var parsed transcriptResult
	if err := json.Unmarshal([]byte(res.Output), &parsed); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(parsed.Error) != "" {
		return "", "", fmt.Errorf("%s", parsed.Error)
	}
	return strings.TrimSpace(parsed.TranscriptFull), strings.TrimSpace(parsed.Language), nil
}

type sourcingAssetTreeAdapter struct {
	svc *assettree.Service
}

func (a *sourcingAssetTreeAdapter) UpsertNode(ctx context.Context, node sourcing.AssetTreeNode) error {
	if a.svc == nil {
		return nil
	}
	now := time.Now().UTC()
	return a.svc.UpsertNode(ctx, &assetsrepo.AssetNode{
		ID:        node.ID,
		Source:    node.Source,
		AssetID:   node.ID,
		Name:      node.Name,
		Type:      "file",
		Path:      node.Name,
		IsFolder:  false,
		DriveLink: node.DriveLink,
		Metadata:  "{}",
		CreatedAt: now,
		UpdatedAt: now,
	})
}

type sourcingSearchAdapter struct {
	registry *providers.Registry
}

func (a *sourcingSearchAdapter) Search(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error) {
	if a.registry == nil {
		return nil, nil
	}
	var out []sourcing.SearchCandidate
	for _, p := range a.registry.ByCapability(providers.CapabilitySearch) {
		sp, ok := p.(providers.SearchProvider)
		if !ok {
			continue
		}
		res, err := sp.Search(ctx, providers.SearchRequest{Query: query, Limit: limit})
		if err != nil {
			continue
		}
		for _, cand := range res.Candidates {
			out = append(out, sourcing.SearchCandidate{
				SourceRef: cand.SourceRef,
				Title:     cand.Title,
				Score:     cand.Score,
			})
		}
	}
	return out, nil
}

type sourcingConfigAdapter struct {
	cfg *config.Config
}

func (a *sourcingConfigAdapter) ClipsFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ClipsFolder()
}

func (a *sourcingConfigAdapter) RootFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.RootFolder()
}

type sourcingEnrichmentAdapter struct {
	handler *clipsapi.Handler
}

func (a *sourcingEnrichmentAdapter) EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error {
	if a.handler == nil {
		return nil
	}
	clip := &asset.Asset{
		ID:        clipID,
		Source:    asset.Source(source),
		Name:      clipID,
		MediaType: asset.MediaType("video"),
	}
	clip.SetLocalPath(localPath)
	a.handler.EnrichAndIndexClip(ctx, clip, source)
	return nil
}

type sourcingMetadataAdapter struct {
	cfg      *config.Config
	uploader *driveutil.Uploader
	log      *zap.Logger
}

func (a *sourcingMetadataAdapter) UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error {
	if a.uploader == nil || a.cfg == nil {
		return nil
	}
	clipsapi.UpdateCumulativeMetadataJSON(ctx, newClipsDriveAdapter(a.uploader), a.cfg.Storage.TempPath(), folderID, clipID, entry, a.log)
	return nil
}

type zapSourcingLogger struct {
	log *zap.Logger
}

func (a *zapSourcingLogger) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}

func fromExistingClip(c *sourcing.ExistingClip) *asset.Asset {
	if c == nil {
		return nil
	}
	out := &asset.Asset{
		ID:       c.ID,
		Name:     c.Name,
		Filename: c.Filename,
		Source:   asset.Source(c.Source),
		Category: c.Category,
		Tags:     append([]string(nil), c.Tags...),
		Duration: c.Duration,
	}
	out.SetLocalPath(c.LocalPath)
	out.SetDriveLink(c.DriveLink)
	out.SetDriveFileID(c.DriveFileID)
	out.SetFileHash(c.FileHash)
	return out
}

// sourcingDispatcherAdapter adapts outbox.Dispatcher to sourcing.IndexDispatcherPort.
// Converts sourcing.ExistingClip → asset.Asset before delegating to the dispatcher.
type sourcingDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// Compile-time assertion: sourcingDispatcherAdapter satisfies sourcing.IndexDispatcherPort.
var _ sourcing.IndexDispatcherPort = (*sourcingDispatcherAdapter)(nil)

func (a *sourcingDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *sourcing.ExistingClip, contentHash string) error {
	if a.disp == nil {
		return nil
	}
	if clip == nil {
		return fmt.Errorf("sourcingDispatcherAdapter: clip is nil")
	}
	domainAsset := fromExistingClip(clip)
	return a.disp.EnqueueAndIndex(ctx, domainAsset, contentHash)
}

func toExistingClip(c *asset.Asset) *sourcing.ExistingClip {
	if c == nil {
		return nil
	}
	return &sourcing.ExistingClip{
		ID:          c.ID,
		Name:        c.Name,
		Filename:    c.Filename,
		Duration:    c.Duration,
		Source:      string(c.Source),
		Category:    c.Category,
		Tags:        append([]string(nil), c.Tags...),
		LocalPath:   c.LocalPath(),
		DriveLink:   c.DriveLink(),
		DriveFileID: c.DriveFileID(),
		FileHash:    c.FileHash(),
	}
}
