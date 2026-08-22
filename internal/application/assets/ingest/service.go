package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/enrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

type Pipeline struct {
	Kind          Kind
	DefaultSource string
	RootFolderID  string
	RootFolder    func(*Request) string
	Lifecycle     *lifecycle.Service
}

type Service struct {
	cfg        *config.Config
	log        *zap.Logger
	downloader assets.MediaDownloader
	// PR-WAVE-1-DRIVE-SSOT (July 2026): the legacy `driveAdmin
	// drive.Admin` field is RETIRED. Confirmed unused by every
	// public method (Ingest, the post-resolution branches, the
	// commit stage all reference typed-port pipeline field, not
	// this dead Admin reference). The canonical ingest-side Drive
	// surface is the per-pipeline `Pipeline.Lifecycle` (the
	// domain-owned lifecycle invariant) and the composition-root
	// scoped `*driveutil.Uploader` (held by the higher-level
	// MediaIngestBundle / buildIngestService for the
	// LifecycleFromDeps + ReversalPaths surface, NOT threaded
	// through this application-layer Service). Composition-root
	// wire sites (internal/app/module_media_ingest.go +
	// internal/app/build_bundles_ingest.go) updated to the 5-arg
	// ctor signature.
	pipelines map[Kind]*Pipeline
	imagesDir string
	tempDir   string
	// enrichState is the typed state-machine wrapper
	// (PR-ENRICHMENT-STATE-MACHINE, July 2026, godlike/06 SSOT).
	// NewService stamps PENDING on ingested rows post-ProcessAsset
	// success so no future row is "mai classificato". Optional
	// during EXPAND phase: nil is permitted (constructor FailClosed
	// gate below) so composition-root wiring can land incrementally.
	enrichState *enrichment.EnrichStateMachine
}

// NewService constructs the canonical ingest service.
// enrichState may be nil during EXPAND phase (composition-root wires
// it incrementally). When nil, Service.Ingest skips the canonical
// PENDING stamp — the VLM 15-min sweeper still recovers via its
// scrape-candidate filter (backfill path). When non-nil, the
// canonical PENDING stamp fires immediately on ingest success
// (godlike/06 SSOT: every freshly-ingested row gets explicit
// enrich_state).
//
// PR-WAVE-1-DRIVE-SSOT (July 2026): the legacy `driveAdmin drive.Admin`
// parameter is REMOVED from the canonical ctor signature — the field
// was unused by every method (Ingest / branches / commit never read
// `s.driveAdmin`). Composition-root wire sites updated to drop the
// argument.
func NewService(cfg *config.Config, log *zap.Logger, downloader assets.MediaDownloader, pipelines map[Kind]*Pipeline, enrichState *enrichment.EnrichStateMachine) *Service {
	return &Service{
		cfg:         cfg,
		log:         log,
		downloader:  downloader,
		pipelines:   pipelines,
		imagesDir:   cfg.Storage.ImagesPath(),
		tempDir:     cfg.Storage.TempPath(),
		enrichState: enrichState,
	}
}

func (s *Service) Ingest(ctx context.Context, req *Request) (*Result, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	kind := normalizeKind(req.Kind)
	if kind == "" {
		return nil, fmt.Errorf("kind is required")
	}

	pipeline := s.pipelines[kind]
	if pipeline == nil || pipeline.Lifecycle == nil {
		return nil, fmt.Errorf("ingest pipeline not configured for kind: %s", kind)
	}

	localPath, filename, cleanup, err := s.acquireLocalPath(ctx, kind, req)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	if shouldRejectAssetInput(req.Filename) || shouldRejectAssetInput(localPath) {
		return nil, fmt.Errorf("unsupported non-media ingest input: %q", strings.TrimSpace(req.Filename))
	}
	if info, statErr := os.Stat(localPath); statErr == nil && info.IsDir() {
		return nil, fmt.Errorf("unsupported directory ingest input: %q", localPath)
	}

	if kind == KindImage {
		localPath, filename, cleanup, err = s.materializeImage(localPath, filename, req)
		if err != nil {
			return nil, err
		}
		if cleanup != nil {
			defer cleanup()
		}
	}

	fileHash, err := checksum.LegacyMD5File(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash media file: %w", err)
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = pipeline.DefaultSource
	}
	if source == "" {
		source = string(kind)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.Filename)
	}
	if name == "" {
		name = strings.TrimSpace(filename)
	}
	if name == "" {
		name = strings.TrimSpace(req.SourceID)
	}
	if name == "" {
		name = strings.TrimSpace(fileHash)
	}

	if filename == "" {
		filename = strings.TrimSpace(req.Filename)
	}
	if filename == "" {
		filename = filepath.Base(localPath)
	}
	if filename == "" {
		filename = name
	}

	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(req.URL)
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(req.LocalPath)
	}
	if sourceID == "" {
		sourceID = fileHash
	}

	rootFolderID := pipeline.RootFolderID
	if pipeline.RootFolder != nil {
		rootFolderID = pipeline.RootFolder(req)
	}
	resolvedFolderID, resolvedFolderPath, err := s.resolveDriveFolder(ctx, kind, rootFolderID, req)
	if err != nil {
		return nil, err
	}

	metadata := mergeMetadata(req.Metadata, map[string]any{
		"kind":          string(kind),
		"source":        source,
		"source_id":     sourceID,
		"filename":      filename,
		"local_path":    localPath,
		"folder_id":     resolvedFolderID,
		"folder_path":   resolvedFolderPath,
		"legacy_file_md5":     fileHash,
		"content_hash":  fileHash,
		"source_url":    req.URL,
		"drive_link":    req.DriveLink,
		"drive_file_id": req.DriveFileID,
		"download_link": req.DownloadLink,
		"tags":          req.Tags,
	})
	metaJSON, _ := json.Marshal(metadata)

	id := buildAssetID(kind, fileHash)
	input := &lifecycle.FinalizeInput{
		ID:           id,
		Name:         name,
		Filename:     filename,
		Kind:         toAssetKind(kind),
		Source:       source,
		SourceID:     sourceID,
		Group:        strings.TrimSpace(req.Group),
		Subfolder:    strings.TrimSpace(req.Subfolder),
		LocalPath:    localPath,
		FolderID:     resolvedFolderID,
		FolderPath:   resolvedFolderPath,
		DriveLink:    strings.TrimSpace(req.DriveLink),
		DriveFileID:  strings.TrimSpace(req.DriveFileID),
		DownloadLink: strings.TrimSpace(req.DownloadLink),
		LegacyFileMD5:     fileHash,
		Metadata:     string(metaJSON),
		Destination:  destinationForKind(kind),
		Subject:      name,
		Style:        imageStyle(req),
		Duration:     req.Duration,
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: true,
		VerifyDB:     true,
	}

	result, err := pipeline.Lifecycle.ProcessAsset(ctx, input, fileHash)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("empty ingest result")
	}
	// Canonical godlike/06 SSOT enrichment-stamp: every freshly-ingested
	// row is born with explicit enrich_state="PENDING" (PR-ENRICHMENT-
	// STATE-MACHINE, July 2026). The typed state-machine wrapper
	// preserves the godlike/06 SSOT contract by writing through
	// EnrichStateMachine.MarkPending (which routes through
	// EnrichRepositoryPort.SetEnrichState — the canonical write
	// surface) and stamping enrich_state_updated_at atomically with
	// enrich_state.
	//
	// EXPAND-phase discipline: when enrichState is nil (composition-
	// root wiring deferred), the canonical stamp is skipped and the
	// VLM 15-min sweeper still recovers via the scrape-candidate
	// filter (backfill path, per godlike/07). When non-nil, the
	// canonical stamp fires immediately so the row is never "mai
	// classificato".
	//
	// Why stamp only after a successful lifecycle call: an operational
	// error means the canonical asset was not completed, so there is no
	// stable enrich_state to update.
	if s.enrichState != nil {
		if stampErr := s.enrichState.MarkPending(ctx, id); stampErr != nil {
			// godlike/07 no-fake-availability: the stamp failure
			// MUST surface as a typed error envelope, NOT be silently
			// swallowed (the row would then be born without an
			// enrich_state and the VLM sweeper would see it as a
			// legitimate scrape candidate on every tick, defeating
			// the no-fake-availability contract).
			s.log.Warn("canonical enrich_state stamp failed (ingest result still returned OK)",
				zap.String("asset_id", id),
				zap.Error(stampErr))
			return nil, fmt.Errorf("canonical enrich_state stamp failed for asset %q: %w", id, stampErr)
		}
	}
	status := result.Status
	if status == "" {
		status = "processed"
	}

	return &Result{
		OK:               true,
		Status:           status,
		Kind:             string(kind),
		ID:               id,
		Source:           source,
		SourceID:         sourceID,
		Name:             name,
		Filename:         filename,
		FolderID:         resolvedFolderID,
		FolderPath:       resolvedFolderPath,
		LocalPath:        localPath,
		DriveLink:        result.DriveLink,
		DriveFileID:      result.DriveFileID,
		DownloadLink:     result.DownloadLink,
		LegacyFileMD5:         fileHash,
		ContentHash:      fileHash,
		SkippedDuplicate: status == "skipped_duplicate" || status == "would_skip_duplicate",
		Metadata:         metadata,
	}, nil
}

func destinationForKind(kind Kind) delivery.DestinationKey {
	switch kind {
	case KindImage:
		return delivery.DestinationImage
	case KindVoiceover:
		return delivery.DestinationVoiceover
	case KindClip:
		return delivery.DestinationYouTubeClip
	case KindStock:
		return delivery.DestinationStock
	default:
		return ""
	}
}

func imageStyle(req *Request) string {
	if req == nil || req.Metadata == nil {
		return "retrieved"
	}
	if style, ok := req.Metadata["style"].(string); ok && strings.TrimSpace(style) != "" {
		return strings.TrimSpace(style)
	}
	return "retrieved"
}
