package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Processor orchestrates download via yt-dlp or HTTP, optional ffmpeg
// normalization, perceptual deduplication, file hashing, and canonical
// Drive upload via delivery.Publisher. It implements the canonical
// domain/detail.Processor contract directly.
//
// F2.8 (June 2026): the legacy `driveUploader *drive.Uploader` field
// is REMOVED. Every Drive write goes through delivery.Publisher.Publish —
// the DestinationRegistry + RequireSubpath + ConflictPolicy belt is
// the single canal for assets. The pre-F2.8 cumulative metadata.json
// sidecar (which used raw Drive SDK List/Download/Trash/Upload) is
// REMOVED entirely: the canonical metadata ledger is the
// artifacts.Registry → media_assets SQLite table (Wave C SSOT). The
// Drive-side JSON manifest was a parallel-struct anti-pattern that has
// no analogue after the Wave-C consolidation.
//
// capability. A source that is not a direct media file or an HLS playlist
// already did.
type Processor struct {
	dl       YTDLP
	httpDL   HTTPDownloader
	ffmpeg   VideoProcessor
	log      *zap.Logger
	dataDir  string
	tempDir  string
	videoCfg mediaexec.NormalizeOptions
	// REMOVED too; the media processor no longer names any provider.
	embeddingURL  string
	registry      artifacts.Registry
	publisher     delivery.Publisher
	artifactCache capcache.Cache
}

var _ detail.MediaProcessor = (*Processor)(nil)

// SetArtifactCache attaches the shared CAS-backed derived-artifact cache.
// Cache failures never replace the media processor's fail-closed execution
// errors: a cache is disposable, so generation falls through to the real
// processor and logs the cache degradation.
func (p *Processor) SetArtifactCache(cache capcache.Cache) *Processor {
	if p != nil {
		p.artifactCache = cache
	}
	return p
}

// ProcessorConfig holds the constructor dependencies for Processor.
type ProcessorConfig struct {
	DataDir            string
	TempDir            string
	VideoCfg           mediaexec.NormalizeOptions
	EmbeddingServerURL string // Python embedding/phash server (config.LoopbackBaseURL(8001))
}

// NewProcessor creates a new media processor with the given dependencies.
//
// F2.8 (June 2026): the trailing arg swaps from `*drive.Uploader` to
// `delivery.Publisher`. Composition-time fail-fast: a nil publisher
// surfaces at the construction site (boot) rather than at first
// upload — a wiring gap is loud in the operator log instead of silent
// in a worker failure. mirror of NewPublisher (F1.4) fail-fast pattern.
func NewProcessor(
	dl YTDLP,
	httpDL HTTPDownloader,
	ff VideoProcessor,
	log *zap.Logger,
	cfg ProcessorConfig,
	registry artifacts.Registry,
	publisher delivery.Publisher,
) *Processor {
	if publisher == nil {
		panic("processor.NewProcessor: publisher is required (composition root must inject delivery.Publisher from DriveBundle.Publisher)")
	}
	embeddingURL := cfg.EmbeddingServerURL
	if embeddingURL == "" {
		embeddingURL = config.LoopbackBaseURL(8001)
	}
	return &Processor{
		dl:           dl,
		httpDL:       httpDL,
		ffmpeg:       ff,
		log:          log,
		dataDir:      cfg.DataDir,
		tempDir:      cfg.TempDir,
		videoCfg:     cfg.VideoCfg,
		embeddingURL: embeddingURL,
		registry:     registry,
		publisher:    publisher,
	}
}

// Process orchestrates the full pipeline: download, process, hash, and upload.
// It validates inputs, downloads the asset, optionally normalizes via ffmpeg,
// checks for perceptual duplicates, computes the file hash, and uploads to
// Drive via the canonical delivery.Publisher.
func (p *Processor) Process(ctx context.Context, input *detail.ProcessInput) (*detail.ProcessResult, error) {
	if input == nil {
		err := fmt.Errorf("detail.ProcessInput is required")
		return &detail.ProcessResult{Status: "failed", Error: err.Error()}, err
	}

	result := &detail.ProcessResult{
		ID:     input.ID,
		Status: "failed",
	}

	if err := p.validateProcessInput(input); err != nil {
		return result, err
	}

	// Setup paths.
	tmpDir, saveDir := p.setupDirectories(input)
	processedPath := OutputPath(saveDir, processOutputFilename(input))

	actualRawPath, err := p.acquireRawSource(ctx, input, tmpDir)
	if err != nil {
		result.Error = fmt.Sprintf("download failed: %v", err)
		return result, err
	}

	if input.RenditionLayout {
		// Rendition layout (July 2026): preserve the immutable master and
		// generate preview/thumbnail/storyboard/manifest renditions. The master
		// IS the canonical processed output (Sept 2026: the redundant mezzanine
		// copy was retired).
		masterPath, stop, err := p.processRenditionLayout(ctx, input, actualRawPath, result)
		if err != nil {
			return result, err
		}
		if stop {
			return result, nil
		}
		processedPath = masterPath
	} else {
		normalizedPath, stop, err := p.processNormalized(ctx, input, actualRawPath, processedPath, result)
		if err != nil {
			return result, err
		}
		if stop {
			return result, nil
		}
		processedPath = normalizedPath
	}

	p.publishProcessedAsset(ctx, input, result, processedPath)

	// Cleanup raw file after processing.
	// Skip when LocalPath was caller-provided (Step 9/12 wire-up): the caller
	// (e.g. shared SourceStager) owns the staged file's lifecycle. Also skip in
	// rendition layout mode: the master rendition is the preserved raw source
	// and must remain on disk.
	if input.LocalPath == "" && !input.RenditionLayout {
		_ = os.Remove(actualRawPath)
	}

	result.Status = "processed"
	return result, nil
}

func (p *Processor) validateProcessInput(input *detail.ProcessInput) error {
	if input.ID == "" {
		return fmt.Errorf("ProcessInput.ID is required")
	}
	if input.Name == "" {
		return fmt.Errorf("ProcessInput.Name is required")
	}
	// Step 9/12 wire-up (July 2026): relaxed from "SourceURL==\"\" → error" to
	// OR-relationship with LocalPath. Either field is valid; if both are set,
	// LocalPath takes precedence (download skipped).
	if input.SourceURL == "" && input.LocalPath == "" {
		return fmt.Errorf("ProcessInput.SourceURL or LocalPath is required")
	}
	if p.dl == nil && input.LocalPath == "" {
		return fmt.Errorf("processor.dl (YTDLP) is nil - cannot download and LocalPath not set")
	}
	return nil
}

// processOutputFilename builds the output name from the clip name, capped so the
// final filename (and its atomic temp sibling) stays under the 255-byte
// filesystem component limit — long clip names otherwise make reprocess fail
// with ENAMETOOLONG at temp-file creation.
//
// Reprocess contract fix (August 2026): honor input.Filename when the caller
// supplies the canonical clip filename
// (yt_<videoID>_<start>_<end>_<policy>_<slug>.mp4). Matching the original Drive
// upload name lets the publisher's ConflictOverwrite lookup find the existing
// file and update it in place instead of creating a fresh (orphaned) Drive file
// on every reprocess.
func processOutputFilename(input *detail.ProcessInput) string {
	finalFilename := filepath.Base(input.Filename)
	if finalFilename == "" || finalFilename == "." {
		namePart := textutil.SafeName(input.Name)
		const maxNamePartLen = 150
		if len(namePart) > maxNamePartLen {
			namePart = namePart[:maxNamePartLen]
		}
		finalFilename = namePart + " " + input.ID + ".mp4"
	}
	return finalFilename
}

// acquireRawSource returns the source bytes to process: the caller's staged file
// when present, otherwise a fresh yt-dlp download at the canonical raw path.
// Step 9/12 wire-up (July 2026): a caller-provided LocalPath BYPASSES the
// download step entirely, eliminating the redundant bandwidth double-download
// that was previously only a probed pre-flight.
func (p *Processor) acquireRawSource(ctx context.Context, input *detail.ProcessInput, tmpDir string) (string, error) {
	if input.LocalPath != "" {
		p.log.Info("Process: bypassing downloadStep — using caller-provided LocalPath",
			zap.String("id", input.ID),
			zap.String("local_path", input.LocalPath))
		return input.LocalPath, nil
	}
	// Use a path without extension so yt-dlp can add %(ext)s correctly.
	rawPath := TmpPath(tmpDir, fmt.Sprintf("raw_%s", input.ID))
	return p.downloadStep(ctx, input, rawPath)
}

// processRenditionLayout materializes the rendition set and promotes the master
// to the canonical output. stop=true means the run is already concluded (a
// perceptual duplicate was found and reused); err carries a failure with
// result.Error stamped.
func (p *Processor) processRenditionLayout(ctx context.Context, input *detail.ProcessInput, actualRawPath string, result *detail.ProcessResult) (string, bool, error) {
	renditions, err := p.processRenditions(ctx, input, actualRawPath)
	if err != nil {
		if input.LocalPath == "" {
			_ = os.Remove(actualRawPath)
		}
		result.Error = fmt.Sprintf("rendition processing failed: %v", err)
		return "", false, err
	}
	result.Renditions = renditions

	// The master is the canonical processed output.
	master := p.findRendition(renditions, detail.RenditionKindMaster)
	if master == nil {
		if input.LocalPath == "" {
			_ = os.Remove(actualRawPath)
		}
		result.Error = "master rendition missing after processing"
		return "", false, fmt.Errorf("%s", result.Error)
	}
	result.ContentSHA256 = master.ContentSHA256
	result.LocalPath = master.LocalPath
	result.Filename = master.Filename

	if p.applyPerceptualDuplicate(ctx, input, master.LocalPath, actualRawPath, result) {
		return "", true, nil
	}

	// Remove the temporary downloaded raw file; the immutable master copy under
	// OutputDir/master/ is the preserved source.
	if input.LocalPath == "" {
		_ = os.Remove(actualRawPath)
	}
	return master.LocalPath, false, nil
}

// processNormalized normalizes (or passes through) the source, then hashes it.
// Normalize=false bypasses both the cache (a raw passthrough must never be
// served from or stored under the "normalize" key) and the ffmpeg normalize.
// stop=true means a perceptual duplicate was reused.
func (p *Processor) processNormalized(ctx context.Context, input *detail.ProcessInput, actualRawPath, processedPath string, result *detail.ProcessResult) (string, bool, error) {
	normalizeEnabled := input.Normalize == nil || *input.Normalize
	sourceSHA := hashFileSHA256(actualRawPath)
	normalizeKey := capcache.Key{}
	normalizeCached := false
	normalizeLeaseID := ""
	if normalizeEnabled && sourceSHA != "" {
		normalizeKey = capcacheKey(sourceSHA, "normalize", p.videoCfg, "media-normalize/v1")
		normalizeCached, normalizeLeaseID = p.materializeCachedFile(ctx, normalizeKey, processedPath)
	}
	if !normalizeCached {
		normalizedPath, err := p.processStep(ctx, input, actualRawPath, processedPath)
		if err != nil {
			p.releaseCachedClaim(ctx, normalizeKey, normalizeLeaseID, err.Error())
			if input.LocalPath == "" {
				_ = os.Remove(actualRawPath)
			}
			result.Error = fmt.Sprintf("process failed: %v", err)
			return "", false, err
		}
		processedPath = normalizedPath
		if normalizeEnabled && sourceSHA != "" {
			p.storeCachedFile(ctx, normalizeKey, normalizeLeaseID, processedPath, "video/mp4")
		}
	}

	// Perceptual deduplication.
	if p.applyPerceptualDuplicate(ctx, input, processedPath, actualRawPath, result) {
		return "", true, nil
	}

	// Step 3: Hash. Caller-provided LocalPath cleanup skipped below (Step 9/12
	// wire-up): the stager owns the staged file lifecycle.
	fileHash, err := p.hashStep(ctx, processedPath)
	if err != nil {
		if input.LocalPath == "" {
			_ = os.Remove(actualRawPath)
		}
		_ = os.Remove(processedPath)
		result.Error = fmt.Sprintf("hash failed: %v", err)
		return "", false, err
	}
	result.ContentSHA256 = fileHash
	result.LocalPath = processedPath
	result.Filename = filepath.Base(processedPath)
	return processedPath, false, nil
}

// applyPerceptualDuplicate reuses an existing asset when the processed bytes are
// a perceptual duplicate. A hit removes the local raw and processed copies so a
// duplicate never creates a second Drive file, and reports true: the caller must
// NOT publish or continue the pipeline.
func (p *Processor) applyPerceptualDuplicate(ctx context.Context, input *detail.ProcessInput, processedPath, rawPath string, result *detail.ProcessResult) bool {
	duplicateID, _ := p.checkPHashDeduplication(ctx, input.ID, processedPath)
	if duplicateID == "" {
		return false
	}
	p.log.Info("perceptual duplicate found", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
	result.DuplicateOf = duplicateID
	existing, err := p.registry.GetMedia(ctx, duplicateID)
	if err != nil || existing == nil {
		return false
	}
	result.DriveLink = existing.DriveLink
	result.DriveFileID = existing.DriveFileID
	result.DownloadLink = existing.DownloadLink
	result.Status = "duplicate"

	// Clean up local files to avoid duplicate drive.
	_ = os.Remove(rawPath)
	_ = os.Remove(processedPath)

	p.log.Info("Reusing Drive details from duplicate asset", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
	return true
}

// publishProcessedAsset performs the canonical Drive upload via
// delivery.Publisher.
//
// F2.8 (June 2026): pre-F2.8 the upload went through the legacy
// `p.driveUploader.UploadFile` bypass which skipped the DestinationRegistry +
// RequireSubpath + ConflictPolicy belt. Post-migration every Drive write from the
// asset processor routes through the canonical Publisher — the sidecar
// metadata.json upload is REMOVED entirely: the canonical metadata ledger is the
// artifacts.Registry → media_assets SQLite table (Wave C SSOT).
//
// Fail-closed semantics: the processor is UPSTREAM of the lifecycle.Service /
// assets/lifecycle.Finalize layer, which is where RequireDrive is enforced. The
// processor ATTEMPTS the upload and stamps Result.Error on failure while Status
// stays "processed", so the lifecycle gate remains the single canonical place
// that flips to a required Drive-upload error.
func (p *Processor) publishProcessedAsset(ctx context.Context, input *detail.ProcessInput, result *detail.ProcessResult, processedPath string) {
	if input.FolderID == "" {
		return
	}
	if input.SkipPublish {
		// SkipPublish (July 2026, reprocess contract fix): upload_drive=false
		// skips the canonical publish; the local rendition + hash still stand.
		p.log.Info("Drive publish skipped by caller (upload_drive=false)", zap.String("id", input.ID))
		return
	}

	destKey, parentFolderID := p.resolvePublishDestination(input)
	group := strings.TrimSpace(input.Group)
	if group == "" {
		group = input.Term
	}
	pubReq := delivery.PublishRequest{
		Destination:    destKey,
		LocalPath:      processedPath,
		Filename:       result.Filename,
		Description:    fmt.Sprintf("PipelineGen processed: %s (id=%s)", input.Name, input.ID),
		AssetID:        input.ID,
		Group:          group, // legacy search term or explicit group path segment
		Subject:        strings.TrimSpace(input.Subject),
		ParentFolderID: parentFolderID,
		// Regenerable output: the latest rendition always wins on Drive.
		// Leaving the policy unset consulted the P1.1 immutable-curated-assets
		// rule and silently kept Drive pinned to the FIRST rendition forever
		// (every re-render logged publish_action=skipped with the stale md5).
		ConflictPolicy: delivery.ConflictOverwrite,
	}
	pubRes, pubErr := p.publisher.Publish(ctx, pubReq)
	if pubErr != nil {
		p.log.Warn("Drive upload failed (continuing with local only; lifecycle.RequireDrive reports the operational error)",
			zap.String("id", input.ID),
			zap.String("destination", string(destKey)),
			zap.Error(pubErr),
		)
		// Failure stamping so a caller surfacing Result.Status=="processed"
		// doesn't have to grep the log to discover why Drive fields are empty.
		result.Error = fmt.Sprintf("drive upload failed: %v", pubErr)
		return
	}
	result.DriveLink = pubRes.WebViewLink
	result.DriveFileID = pubRes.FileID
	// Strict canonical-URL policy (F2.7): PublishResult.DownloadLink is the
	// single source of truth for the drive download URL. Empty means a Publisher
	// bug, NOT an opportunity for silent reconstruction (the F1.6 Canon-URL test
	// pins this).
	result.DownloadLink = pubRes.DownloadLink
	result.MD5 = pubRes.MD5Checksum
	result.PublishAction = string(pubRes.Action)
	p.log.Info("File uploaded to Drive via Publisher (F2.8)",
		zap.String("id", input.ID),
		zap.String("file_id", pubRes.FileID),
		zap.String("folder_id", input.FolderID),
		zap.String("destination", string(destKey)),
		zap.String("publish_action", string(pubRes.Action)),
		zap.String("md5", pubRes.MD5Checksum),
	)
}

// resolvePublishDestination maps a TransformSpec.Destination into the
// delivery.DestinationKey + ParentFolderID for the canonical publish.
//
// ParentFolderID = input.FolderID (the legacy explicit-folder escape
// hatch) is preserved for backward compatibility.
//
// Explicit destination (Destination set, e.g. "youtube_clip"): the
// DestinationRegistry is the sole authority for the canonical root +
// path hierarchy, so ParentFolderID is dropped. This prevents a stale
// FolderID from drifting a reprocess upload into the wrong Drive folder
// (reprocess folder-alignment, August 2026) — the clip's real folder is
// resolved from the registry PathBuilder instead.
//
// Artlist retirement: the implicit default used to be the Artlist
// destination, whose path builder accepted a group alone. The assets now
// belong to DestinationStock, whose builder also needs a Subject, so an
// implicit caller with a ParentFolderID keeps uploading straight into that
// folder (the publisher's documented direct-to-root fallback) while a
// caller with neither Subject nor folder fails closed instead of landing
// in root.
func (p *Processor) resolvePublishDestination(input *detail.ProcessInput) (delivery.DestinationKey, string) {
	dest := strings.TrimSpace(input.Destination)
	if dest == "" {
		// The legacy default used to be the Artlist destination, whose
		// dedicated Drive folder is retired. Its assets belong to the
		// canonical stock destination, so the default resolves there now.
		return delivery.DestinationStock, input.FolderID
	}
	return delivery.CanonicalizeDestination(delivery.DestinationKey(dest)), ""
}

// findRendition returns the first rendition with the given kind, or nil.
func (p *Processor) findRendition(renditions []detail.RenditionOutput, kind detail.RenditionKind) *detail.RenditionOutput {
	for i := range renditions {
		if renditions[i].Kind == kind {
			return &renditions[i]
		}
	}
	return nil
}

// setupDirectories creates temp and save directories, returning their paths.
func (p *Processor) setupDirectories(input *detail.ProcessInput) (tmpDir, saveDir string) {
	tmpDir = filepath.Join(p.dataDir, p.tempDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		// Fallback to os.TempDir() so the rest of the pipeline can
		// still proceed even on permission/FS failures; the operator
		// log captures the actual MkdirAll error for triage.
		p.log.Error("failed to create temp directory; falling back to os.TempDir", zap.String("dir", tmpDir), zap.Error(err))
		tmpDir = os.TempDir()
	}

	saveDir = input.OutputDir
	if saveDir == "" {
		saveDir = filepath.Join(p.dataDir, "mediaassets", textutil.SafeName(input.Term))
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		p.log.Error("failed to create save directory; falling back to tmpDir", zap.String("dir", saveDir), zap.Error(err))
		saveDir = tmpDir
	}

	return tmpDir, saveDir
}
