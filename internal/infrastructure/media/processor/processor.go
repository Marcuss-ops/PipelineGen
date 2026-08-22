package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Processor orchestrates download via yt-dlp or HTTP, optional ffmpeg
// normalization, perceptual deduplication, file hashing, and canonical
// Drive upload via delivery.Publisher. It implements the canonical
// domain/asset.Processor contract directly.
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
// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY-CUTOVER (July 2026): the optional
// ArtlistDownloader field routes Artlist-clip downloads through the
// canonical downloader.Resolver. When nil, Artlist clips fall through
// to yt-dlp (Rule 4). PR-ARTLIST-SCRAPER-RETIRE (July 2026): the
// legacy downloadViaScraper path is RETIRED.
type Processor struct {
	dl       YTDLP
	httpDL   HTTPDownloader
	ffmpeg   VideoProcessor
	log      *zap.Logger
	dataDir  string
	tempDir  string
	videoCfg mediaexec.NormalizeOptions
	// PR-ARTLIST-SCRAPER-RETIRE (July 2026): scraperURL field REMOVED.
	// Artlist downloads now route exclusively through the
	// ArtlistDownloader port (wired via build_bundles_artlist.go).
	embeddingURL string
	registry     artifacts.Registry
	publisher    delivery.Publisher
	// ArtlistDownloader is the canonical Resolver-backed Artlist
	// download path. nil-safe: when nil, downloadStep falls through
	// to yt-dlp (Rule 4). Wired in build_bundles_artlist.go via an
	// adapter wrapping downloader.Resolver.Download().
	artlistDL     ArtlistDownloader
	artifactCache capcache.Cache
}

var _ asset.Processor = (*Processor)(nil)

// ArtlistDownloader is the narrow port for Artlist-clip downloads
// routed through the canonical downloader.Resolver. Nil-safe: when
// nil, the processor falls through to yt-dlp (Rule 4).
//
// Wired in build_bundles_artlist.go via an adapter wrapping
// downloader.Resolver.Download(artapp.DownloadRequest).
//
// godlike/06 SSOT: this interface + the Resolver adapter are the
// SINGLE canonical bridge between the generic media processor and
// the Artlist-specific download routing.
type ArtlistDownloader interface {
	DownloadArtlistClip(ctx context.Context, sourceURL, clipPageURL, clipID, destDir, filename string) (localPath string, err error)
}

// SetArtlistDownloader injects the Artlist download bridge.
// Nil-safe (compiles to no-op when dl is nil).
func (p *Processor) SetArtlistDownloader(dl ArtlistDownloader) {
	if dl != nil {
		p.artlistDL = dl
	}
}

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
	DataDir  string
	TempDir  string
	VideoCfg mediaexec.NormalizeOptions
	// PR-ARTLIST-SCRAPER-RETIRE (July 2026): ScraperServerURL REMOVED.
	EmbeddingServerURL string // Python embedding/phash server (e.g. http://127.0.0.1:8001)
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
	// PR-ARTLIST-SCRAPER-RETIRE (July 2026): scraperURL init REMOVED.
	embeddingURL := cfg.EmbeddingServerURL
	if embeddingURL == "" {
		embeddingURL = "http://127.0.0.1:8001"
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
func (p *Processor) Process(ctx context.Context, input *asset.ProcessInput) (*asset.ProcessResult, error) {
	if input == nil {
		err := fmt.Errorf("asset.ProcessInput is required")
		return &asset.ProcessResult{Status: "failed", Error: err.Error()}, err
	}

	result := &asset.ProcessResult{
		ID:     input.ID,
		Status: "failed",
	}

	// Validate required inputs.
	if input.ID == "" {
		return result, fmt.Errorf("ProcessInput.ID is required")
	}
	if input.Name == "" {
		return result, fmt.Errorf("ProcessInput.Name is required")
	}
	// Step 9/12 wire-up (July 2026): relaxed from "SourceURL==" → error"
	// to OR-relationship with LocalPath. Either field is valid; if both
	// are set, LocalPath takes precedence (download skipped).
	if input.SourceURL == "" && input.LocalPath == "" {
		return result, fmt.Errorf("ProcessInput.SourceURL or LocalPath is required")
	}
	if p.dl == nil && input.LocalPath == "" {
		return result, fmt.Errorf("Processor.dl (YTDLP) is nil - cannot download and LocalPath not set")
	}

	// Setup paths.
	tmpDir, saveDir := p.setupDirectories(input)
	// Output filenames are built from the clip name; cap the name portion so
	// the final filename (and its atomic temp sibling) stays under the
	// 255-byte filesystem component limit. Long clip names otherwise make
	// reprocess fail with ENAMETOOLONG at temp-file creation.
	//
	// Reprocess contract fix (August 2026): honor input.Filename when the
	// caller supplies the canonical clip filename
	// (yt_<videoID>_<start>_<end>_<policy>_<slug>.mp4). Matching the
	// original Drive upload name lets the publisher's ConflictOverwrite
	// lookup find the existing file and update it in place instead of
	// creating a fresh (orphaned) Drive file on every reprocess.
	finalFilename := filepath.Base(input.Filename)
	if finalFilename == "" || finalFilename == "." {
		namePart := textutil.SafeName(input.Name)
		const maxNamePartLen = 150
		if len(namePart) > maxNamePartLen {
			namePart = namePart[:maxNamePartLen]
		}
		finalFilename = namePart + " " + input.ID + ".mp4"
	}
	processedPath := OutputPath(saveDir, finalFilename)

	// Step 1: Download (use path without extension so yt-dlp can add %(ext)s correctly).
	// Step 9/12 wire-up (July 2026): when input.LocalPath != "", the download
	// step is BYPASSED — the caller (typically the shared SourceStager port)
	// already staged the source bytes. This eliminates the redundant
	// bandwidth double-download that was previously a probed pre-flight only.
	// The caller owns cleanup of the staged file; Processor must NOT delete it.
	var (
		actualRawPath string
		err           error
	)
	if input.LocalPath != "" {
		actualRawPath = input.LocalPath
		p.log.Info("Process: bypassing downloadStep — using caller-provided LocalPath",
			zap.String("id", input.ID),
			zap.String("local_path", input.LocalPath))
	} else {
		rawPath := TmpPath(tmpDir, fmt.Sprintf("raw_%s", input.ID))
		actualRawPath, err = p.downloadStep(ctx, input, rawPath)
		if err != nil {
			result.Error = fmt.Sprintf("download failed: %v", err)
			return result, err
		}
	}

	// Rendition layout (July 2026): preserve immutable master and generate
	// mezzanine/proxy/thumbnail/storyboard renditions. The canonical
	// processed file becomes the mezzanine.
	if input.RenditionLayout {
		renditions, err := p.processRenditions(ctx, input, actualRawPath)
		if err != nil {
			if input.LocalPath == "" {
				_ = os.Remove(actualRawPath)
			}
			result.Error = fmt.Sprintf("rendition processing failed: %v", err)
			return result, err
		}
		result.Renditions = renditions

		// The mezzanine is the canonical processed output.
		mezzanine := p.findRendition(renditions, asset.RenditionKindMezzanine)
		if mezzanine == nil {
			if input.LocalPath == "" {
				_ = os.Remove(actualRawPath)
			}
			result.Error = "mezzanine rendition missing after processing"
			return result, fmt.Errorf("%s", result.Error)
		}
		processedPath = mezzanine.LocalPath
		result.LegacyFileMD5 = mezzanine.LegacyFileMD5
		result.LocalPath = mezzanine.LocalPath
		result.Filename = mezzanine.Filename

		// Perceptual deduplication on the mezzanine.
		duplicateID, _ := p.checkPHashDeduplication(ctx, input.ID, processedPath)
		if duplicateID != "" {
			p.log.Info("perceptual duplicate found", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
			result.DuplicateOf = duplicateID
			if existing, err := p.registry.GetMedia(ctx, duplicateID); err == nil && existing != nil {
				result.DriveLink = existing.DriveLink
				result.DriveFileID = existing.DriveFileID
				result.DownloadLink = existing.DownloadLink
				result.Status = "duplicate"

				// Clean up local files to avoid duplicate drive.
				_ = os.Remove(actualRawPath)
				_ = os.Remove(processedPath)

				p.log.Info("Reusing Drive details from duplicate asset", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
				return result, nil
			}
		}

		// Remove the temporary downloaded raw file; the immutable master
		// copy under OutputDir/master/ is the preserved source.
		if input.LocalPath == "" {
			_ = os.Remove(actualRawPath)
		}
	} else {
		// Step 2: Process/Normalize. The normalized output is a deterministic
		// derived artifact keyed by source bytes + encoder policy/version.
		// Normalize=false bypasses both the cache (a raw passthrough must
		// never be served from or stored under the "normalize" key) and
		// the ffmpeg normalize itself.
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
			processedPath, err = p.processStep(ctx, input, actualRawPath, processedPath)
			if err != nil {
				p.releaseCachedClaim(ctx, normalizeKey, normalizeLeaseID, err.Error())
				if input.LocalPath == "" {
					_ = os.Remove(actualRawPath)
				}
				result.Error = fmt.Sprintf("process failed: %v", err)
				return result, err
			}
			if normalizeEnabled && sourceSHA != "" {
				p.storeCachedFile(ctx, normalizeKey, normalizeLeaseID, processedPath, "video/mp4")
			}
		}

		// Perceptual deduplication.
		duplicateID, _ := p.checkPHashDeduplication(ctx, input.ID, processedPath)
		if duplicateID != "" {
			p.log.Info("perceptual duplicate found", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
			result.DuplicateOf = duplicateID
			if existing, err := p.registry.GetMedia(ctx, duplicateID); err == nil && existing != nil {
				result.DriveLink = existing.DriveLink
				result.DriveFileID = existing.DriveFileID
				result.DownloadLink = existing.DownloadLink
				result.Status = "duplicate"

				// Clean up local files to avoid duplicate drive.
				_ = os.Remove(actualRawPath)
				_ = os.Remove(processedPath)

				p.log.Info("Reusing Drive details from duplicate asset", zap.String("id", input.ID), zap.String("duplicate_of", duplicateID))
				return result, nil
			}
		}

		// Step 3: Hash. Caller-provided LocalPath cleanup skipped below
		// (Step 9/12 wire-up): the stager owns the staged file lifecycle.
		fileHash, err := p.hashStep(ctx, processedPath)
		if err != nil {
			if input.LocalPath == "" {
				_ = os.Remove(actualRawPath)
			}
			_ = os.Remove(processedPath)
			result.Error = fmt.Sprintf("hash failed: %v", err)
			return result, err
		}
		result.LegacyFileMD5 = fileHash
		result.LocalPath = processedPath
		result.Filename = filepath.Base(processedPath)
	}

	// Step 4: Canonical Drive upload via delivery.Publisher.
	//
	// F2.8 (June 2026): pre-F2.8 the upload went through the
	// legacy `p.driveUploader.UploadFile(ctx, localPath, FolderID,
	// filename)` bypass which skipped the DestinationRegistry +
	// RequireSubpath + ConflictPolicy belt. Post-migration every
	// Drive write from the asset processor routes through the
	// canonical Publisher — the sidecar metadata.json upload
	// (which used raw Drive SDK List/Download/Trash/Upload) is
	// REMOVED entirely: the canonical metadata ledger is the
	// artifacts.Registry → media_assets SQLite table (Wave C SSOT).
	// The pre-F2.8 step "build metaData + write local metadata.json
	// + update cumulative Drive metadata.json" is gone.
	//
	// DestinationKey defaulting: input has no Destination field today
	// (asset.ProcessInput is the canonical DTO and is owned by the
	// domain layer — adding Destination is a follow-up wave when a
	// non-artlist caller emerges). The processor's canonical caller
	// is the artlist ingest pipeline, so DestinationArtlist is the
	// correct default.
	//
	// ConflictPolicy (August 2026, reprocess certification fix):
	// explicitly ConflictOverwrite. The processor's outputs are
	// regenerable renditions — a reprocess MUST replace the previous
	// rendition on Drive. Leaving the policy unset consults the
	// registry, whose DestinationArtlist default is ConflictSkip
	// (P1.1, July 2026: "immutable curated assets") — that silently
	// kept Drive pinned to the FIRST rendition forever (every
	// re-render logged publish_action=skipped with the stale md5).
	// This processor is the upload seam for generated outputs, so
	// overwrite is the correct explicit policy here.
	// ParentFolderID is input.FolderID so callers that explicitly
	// target a specific Drive folder (e.g. legacy pipeline scripts)
	// keep working.
	//
	// Subject defaulting (reviewer-feedback Q1): Subject defaults to
	// empty string, NOT input.ID. The pre-F2.7 implementation had no
	// Subject concept (this is greenfield). Defaulting Subject to the
	// media_assets.id leaks an opaque UUID into Drive-side folder
	// metadata that humans see via PathSegments. Empty Subject lets
	// the Publisher derive uniqueness from PublisherRequest alone
	// per the canonical resolution rules (Publisher must handle
	// empty Subject). A real caller that knows a meaningful Subject
	// (artlist asset UUID, YouTube video ID) populates it explicitly
	// via a follow-up F2.9 field plumb (tracked in
	// architecture/current.yaml).
	//
	// DownloadLink strict policy (reviewer-feedback Q2): NO
	// fallback interpolation. F2.7 closure made PublishResult.
	// DownloadLink the canonical single-source-of-truth URL per
	// godlike/06 "one owner per fact". A Publisher that returns
	// empty DownloadLink on success is a Publisher BUG and MUST
	// surface loudly (already pinned at
	// internal/infrastructure/drive/publisher_policies_test.go,
	// the F1.6 Canon-URL test). Allowing a silent reconstruction
	// via "https://drive.google.com/uc?id="+FileID would produce a
	// URL Drive never actually surfaced — a worse outcome than the
	// visible failure. Empty DownloadLink ⇄ Result.DownloadLink=""
	// ⇄ downstream can branch on the empty value.
	//
	// Fail-closed semantics on Publish failure: the processor is
	// UPSTREAM of the lifecycle.Service / assets/lifecycle.Finalize
	// layer, which is where RequireDrive is enforced. The processor's
	// job is to ATTEMPT the canonical upload and surface success or
	// warn+continue on failure; the lifecycle layer decides whether
	// to persist. To avoid silent lossy uploads (where a caller
	// looks at Result.Status=="processed"+empty DriveLink and has to
	// grep the log to find why), the processor ALSO stamps
	// Result.Error with the publisher error message at non-zero status.
	// Status itself stays "processed" so the lifecycle layer's
	// RequireDrive gate is the single canonical place that flips
	// to a required Drive-upload error (per the lifecycle contract).
	if input.FolderID != "" && !input.SkipPublish {
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
			Group:          group, // artlist search term (legacy) or explicit group path segment
			Subject:        strings.TrimSpace(input.Subject),
			ParentFolderID: parentFolderID,
			// Regenerable output: the latest rendition always wins on
			// Drive (see the ConflictPolicy doc above).
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
			// Status stays "processed" so the lifecycle.RequireDrive gate is
			// the SINGLE canonical authority that flips to UPLOAD_FAILED.
			result.Error = fmt.Sprintf("drive upload failed: %v", pubErr)
		} else {
			result.DriveLink = pubRes.WebViewLink
			result.DriveFileID = pubRes.FileID
			// Strict canonical-URL policy (F2.7): PublishResult.DownloadLink
			// is the single source of truth for the drive download URL.
			// Empty if Publisher didn't surface one — that is a Publisher
			// bug, NOT an opportunity for silent reconstruction.
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
	} else if input.FolderID != "" {
		// SkipPublish (July 2026, reprocess contract fix): upload_drive=false
		// skips the canonical publish; the local rendition + hash still stand.
		p.log.Info("Drive publish skipped by caller (upload_drive=false)", zap.String("id", input.ID))
	}

	// Cleanup raw file after processing.
	// Skip when LocalPath was caller-provided (Step 9/12 wire-up): the
	// caller (e.g. shared SourceStager) owns the staged file's lifecycle.
	// Also skip in rendition layout mode: the master rendition is the
	// preserved raw source and must remain on disk.
	if input.LocalPath == "" && !input.RenditionLayout {
		_ = os.Remove(actualRawPath)
	}

	result.Status = "processed"
	return result, nil
}

// resolvePublishDestination maps a TransformSpec.Destination into the
// delivery.DestinationKey + ParentFolderID for the canonical publish.
//
// Legacy path (Destination empty): the artlist ingest pipeline is the
// canonical caller, so DestinationArtlist is the default and
// ParentFolderID = input.FolderID (the legacy explicit-folder escape
// hatch) is preserved for backward compatibility.
//
// Explicit destination (Destination set, e.g. "youtube_clip"): the
// DestinationRegistry is the sole authority for the canonical root +
// path hierarchy, so ParentFolderID is dropped. This prevents a stale
// FolderID from drifting a reprocess upload into the wrong Drive folder
// (reprocess folder-alignment, August 2026) — the clip's real folder is
// resolved from the registry PathBuilder instead.
func (p *Processor) resolvePublishDestination(input *asset.ProcessInput) (delivery.DestinationKey, string) {
	dest := strings.TrimSpace(input.Destination)
	if dest == "" {
		return delivery.DestinationArtlist, input.FolderID
	}
	return delivery.DestinationKey(dest), ""
}

// findRendition returns the first rendition with the given kind, or nil.
func (p *Processor) findRendition(renditions []asset.RenditionOutput, kind asset.RenditionKind) *asset.RenditionOutput {
	for i := range renditions {
		if renditions[i].Kind == kind {
			return &renditions[i]
		}
	}
	return nil
}

// setupDirectories creates temp and save directories, returning their paths.
func (p *Processor) setupDirectories(input *asset.ProcessInput) (tmpDir, saveDir string) {
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
