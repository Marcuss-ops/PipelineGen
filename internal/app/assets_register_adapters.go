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
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/batch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/drivesync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/localimport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing/youtube"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
)

// newAssetRegisterService builds the SourcingService façade. After P0-1 /
// commit 1 (June 2026) it first constructs the YouTubeRegistrar sub-service
// (with v2 adapters that wrap legacy ports) and then injects that, plus the
// remaining JobsPort/FileScannerPort needed by SyncDriveFolder + LocalToDrive
// (not yet extracted, planned in commits 3 and 4 of P0-1), into the slim
// sourcing.NewService ctor (now 4 args, was 14 historically).

var (
	_ youtube.IndexDispatcherPort = (*youtubeIndexDispatcherAdapter)(nil)
	_ youtube.EnrichmentPort      = (*youtubeEnrichmentAdapter)(nil)
	// Drift guard: youtube.Service implements sourcing.YouTubeRegistrar.
	// This assertion lives at the composition root (rather than in
	// sourcing/service.go) because the latter would re-introduce the
	// import cycle that P0-1 / commit 1 broke (sourcing imports youtube
	// for the (*youtube.Service) reference; youtube imports sourcing
	// for shared types like RegisterClipCommand — cycle).
	_ sourcing.YouTubeRegistrar = (*youtube.Service)(nil)
	// P0-1 / commit 2: batch.Service implements sourcing.BatchRegistrar.
	// Same drift-guard rationale as the YouTube assertion above; the
	// composition root can transitively import both sourcing and batch
	// without re-introducing the cycle (batch is a sub-package of
	// sourcing; sourcing does not import batch).
	_ sourcing.BatchRegistrar = (*batch.Service)(nil)
	// P0-1 / commit 3 (this commit): drivesync.Service implements
	// sourcing.DriveFolderSynchronizer. Same drift-guard rationale;
	// composition root is the only place where both sourcing and
	// drivesync are reachable without a cycle.
	_ sourcing.DriveFolderSynchronizer = (*drivesync.Service)(nil)
	// P0-1 / commit 4 (this commit): localimport.Service implements
	// sourcing.LocalImporter. Composition root transitively imports
	// both sourcing and localimport (the latter is a sub-package of
	// the former; sourcing itself never imports localimport, so no
	// cycle).
	_ sourcing.LocalImporter = (*localimport.Service)(nil)
	// FASE 5: sourcingPublisherAdapter satisfies sourcing.PublisherPort.
	_ sourcing.PublisherPort = (*sourcingPublisherAdapter)(nil)
)

// youtubeIndexDispatcherAdapter implements youtube.IndexDispatcherPort by
// composing the legacy outbox.Dispatcher with the asset-tree service.
//
// Behaviour (per thinker audit June 2026 / P0-1 commit 1 correction):
//   - Dispathcer upsert failures BUBBLE to the caller (fail-closed;
//     preserves QDRANT-asset-mutation isolation discipline)
//   - Asset-tree upsert failures are SWALLOWED at the adapter boundary
//     (fail-open; mirrors the historical `_ = s.assetTree.UpsertNode(...)`
//     warn-only behaviour of the god-method. Returns nil).
type youtubeIndexDispatcherAdapter struct {
	disp *outbox.Dispatcher
	tree *assettree.Service
}

func (a *youtubeIndexDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *sourcing.ExistingClip, contentHash string) error {
	if a.disp == nil {
		return fmt.Errorf("youtubeIndexDispatcherAdapter: dispatcher is nil (compose-time bug — wire outbox.Dispatcher in newAssetRegisterService)")
	}
	if clip == nil {
		return fmt.Errorf("youtubeIndexDispatcherAdapter: clip is nil")
	}
	domainAsset := fromExistingClip(clip)
	if err := a.disp.EnqueueAndIndex(ctx, domainAsset, contentHash); err != nil {
		return fmt.Errorf("dispatcher upsert+outbox: %w", err)
	}
	// Asset-tree upsert is best-effort post-dispatcher. The historical
	// god-method called `_ = s.assetTree.UpsertNode(...)` ignoring
	// errors and discarding the warn log; we mirror that exact
	// behaviour in the adapter. Tree drift is a separate concern
	// tracked by PR-ASSETS-MONITOR-CONTRACT-AUDIT-2026-06-28, not the
	// YouTubeRegistrar flow.
	if a.tree != nil {
		now := time.Now().UTC()
		node := &assetsrepo.AssetNode{
			ID:        domainAsset.ID,
			Source:    string(domainAsset.Source),
			AssetID:   domainAsset.ID,
			Name:      domainAsset.Name,
			Type:      "file",
			Path:      domainAsset.Name,
			IsFolder:  false,
			DriveLink: domainAsset.DriveLink(),
			Metadata:  "{}",
			CreatedAt: now,
			UpdatedAt: now,
		}
		_ = a.tree.UpsertNode(ctx, node) // matches historical warn-only behaviour
	}
	return nil
}

// youtubeEnrichmentAdapter implements youtube.EnrichmentPort by composing
// the legacy EnrichmentAdapter (used for the indexed-detection boolean),
// ConfigAdapter (folder defaults), SearchAdapter (related clips), and an
// optional JobsPort for the post-register media.enrich dispatch.
//
// Per thinker audit:
//   - IndexingEnabled() returns true iff enrichment AND (jobs via this
//     adapter has an internal nil-aware path — equivalent to historical
//     `indexed := s.enrichment != nil && s.jobs != nil`).
//   - DispatchPostRegister no-ops when the internal jobs port is nil
//     (preserves historical test-fixture path which logged at Debug
//     level rather than failing closed).
type youtubeEnrichmentAdapter struct {
	jobs       sourcing.JobsPort // nil today; optional wiring in future composition sites
	enrichment sourcing.EnrichmentPort
	search     sourcing.SearchProviderPort
	config     sourcing.ConfigPort
}

func (a *youtubeEnrichmentAdapter) IndexingEnabled() bool {
	// Mirrors historical `indexed := s.enrichment != nil && s.jobs != nil`.
	// When jobs port is nil (current production composition site), this
	// returns false and the YouTubeRegistrar falls back to "not_configured"
	// indexing_status — matching historical behaviour for the same path.
	return a.enrichment != nil && a.jobs != nil
}

func (a *youtubeEnrichmentAdapter) DispatchPostRegister(ctx context.Context, clipID, source, localPath string) error {
	if a.jobs == nil {
		return nil // matches historical fallback: log.Debug("jobs port not wired...")
	}
	_, err := a.jobs.Enqueue(ctx, sourcing.EnqueueRequest{
		Type:       "media.enrich",
		MaxRetries: 1,
		Payload: sourcing.JobPayload{
			"asset_id":   clipID,
			"source":     source,
			"local_path": localPath,
		},
	})
	return err
}

func (a *youtubeEnrichmentAdapter) SearchRelated(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error) {
	if a.search == nil {
		return nil, nil
	}
	return a.search.Search(ctx, query, limit)
}

func (a *youtubeEnrichmentAdapter) FolderDefaults() (clipsFolder, rootFolder string) {
	if a.config == nil {
		return "", ""
	}
	return a.config.ClipsFolder(), a.config.RootFolder()
}

// ── legacy adapters reused on the YouTubeService ctor (no v2 surface needed) ─

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
	drive driveutil.Admin
}

func (a *sourcingDriveAdapter) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*sourcing.DriveUploadResult, error) {
	if a.drive == nil {
		return nil, fmt.Errorf("drive not configured")
	}
	res, err := a.drive.UploadFileWithDescription(ctx, localPath, folderID, filename, description)
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
	if a.drive == nil {
		return parentID, fmt.Errorf("drive not configured")
	}
	return a.drive.GetOrCreateFolder(ctx, name, parentID)
}

func (a *sourcingDriveAdapter) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if a.drive == nil {
		return "", fmt.Errorf("drive not configured")
	}
	return a.drive.GetFolderName(ctx, folderID)
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

// QDRANT-asset-mutation isolation (June 2026): UpsertClip was
// REMOVED from sourcing.ClipStorePort. The adapter above no longer
// exposes UpsertClip because there is no legitimate production caller;
// sourcing callers MUST go through IndexDispatcherPort. The
// non-dispatcher fallback in sourcing.Service.RegisterFromYouTube was
// also removed and replaced with a typed error so a missing
// dispatcher is loud at runtime, not silent.

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
	cfg    *config.Config
	admin  driveutil.Admin
	reader driveutil.Reader
	log    *zap.Logger
}

func (a *sourcingMetadataAdapter) UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error {
	if a.admin == nil || a.cfg == nil {
		return nil
	}
	appclips.UpdateCumulativeMetadataJSON(ctx, newClipsDriveAdapter(a.admin, a.reader), a.cfg.Storage.TempPath(), folderID, clipID, entry, a.log)
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
// Kept for legacy callers that still reference sourcing.IndexDispatcherPort
// directly (e.g. test fixtures and the queue-completion audit hook).
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

// Compile-time assertion retained for legacy hash adapter callers (kept
// here for further integration tests even though the YouTubeRegistrar
// now inlines pkg/hashutil.MD5File directly). Permits the file's
// `*sourcingHashAdapter` to be removed in a follow-up cleanup PR if
// confirmed unused across the production composition chain.
// sourcingPublisherAdapter implements sourcing.PublisherPort by wrapping
// delivery.Publisher. FASE 5 (June 2026): this adapter bridges the
// composition-root's delivery.Publisher (from DriveBundle.Publisher) into
// the sourcing layer so the YouTubeRegistrar can use the canonical
// Publisher path instead of direct DrivePort calls.
type sourcingPublisherAdapter struct {
	publisher delivery.Publisher
}

func (a *sourcingPublisherAdapter) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	if a.publisher == nil {
		return nil, fmt.Errorf("sourcingPublisherAdapter: publisher not wired")
	}
	return a.publisher.Publish(ctx, req)
}

type sourcingHashAdapter struct{}

func (a *sourcingHashAdapter) MD5File(path string) (string, error) {
	return hashutil.MD5File(path)
}

var _ sourcing.HashPort = (*sourcingHashAdapter)(nil)
