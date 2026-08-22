// Package soundeffect provides thin HTTP handlers for sound effect generation.
//
// PG-003 (June 2026): handler now depends on typed sfxports ports rather
// than concrete *assets.ClipsRepository, *drive.Uploader,
// semantic.MetadataWriterPort, *drive.Resolver. Composition root (in
// internal/app/module_assets.go) injects the adapters — see
// internal/app/adapters_soundeffect.go for the concrete implementations.
package soundeffect

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// errSfxDispatcherUnavailable is the fail-closed sentinel surfaced in the
// HTTP body when the canonical sfxports.DispatcherPort is not wired at
// composition time. The 503 status + this message together mark the
// regression shape the operator checks for when investigating
// "Generate returned empty clip_id" — see
// internal/api/assets/soundeffect/dispatcher_fail_closed_test.go for the
// contract pinning.
const errSfxDispatcherUnavailable = "sound effect generate unavailable: AssetMutationDispatcher not wired (QDRANT-asset-mutation isolation; production composition root must wire *outbox.Dispatcher via sfxDispatcherAdapter)"

// Handler manages sound effect generation via Python synth + ffmpeg.
type Handler struct {
	clipsRepo  sfxports.ClipRepositoryPort
	metaWriter sfxports.SemanticMetadataWriterPort
	resolver   sfxports.DestinationResolverPort
	// dispatcher (PR 6, June 2026, codex/qdrant-api-writers-fail-closed):
	// the canonical narrow port sfxports.DispatcherPort wrapping the
	// production *outbox.Dispatcher. Required for the Generate write
	// path; nil causes Generate to return HTTP 503 (fail-closed) — this
	// is the documented contract replacement for the legacy
	// "if h.clipsRepo != nil { h.clipsRepo.Upsert }" soft-fallback that
	// the Wave 22 task-2 handler migration removes.
	dispatcher             sfxports.DispatcherPort
	publisher              sfxports.PublisherPort // F2.10: legacy driveUploader field RETIRED (override brutal) — Publisher is the single canonical Drive-write canal
	soundEffectsRootFolder string
	processRunner          appassets.ProcessRunner
	log                    *zap.Logger
}

// HandlerDeps carries the dependencies for NewHandler. Grouping them
// keeps the constructor under the archcheck 8-parameter cap while
// making the call sites self-documenting.
type HandlerDeps struct {
	ClipsRepo              sfxports.ClipRepositoryPort
	MetaWriter             sfxports.SemanticMetadataWriterPort
	Resolver               sfxports.DestinationResolverPort
	Dispatcher             sfxports.DispatcherPort
	Publisher              sfxports.PublisherPort
	SoundEffectsRootFolder string
	ProcessRunner          appassets.ProcessRunner
	Log                    *zap.Logger
}

// NewHandler creates a sound effect handler.
//
// All concrete infrastructure collaborators are injected via structural
// ports (sfxports.*). The composition root is responsible for instantiating
// the adapters in internal/app and injecting them here.
//
// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): the dispatcher
// parameter is added so the Generate write path can route through the
// canonical *outbox.Dispatcher.EnqueueAndIndex (atomic UPSERT +
// asset.index.requested outbox event) instead of the legacy direct
// h.clipsRepo.Upsert write. A nil dispatcher is tolerated by the constructor
// so existing test fixtures that omit the dispatcher continue to compile —
// the Generate handler fails closed with HTTP 503 when invoked against a
// nil dispatcher.
func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{
		clipsRepo:              deps.ClipsRepo,
		metaWriter:             deps.MetaWriter,
		resolver:               deps.Resolver,
		dispatcher:             deps.Dispatcher,
		publisher:              deps.Publisher,
		soundEffectsRootFolder: deps.SoundEffectsRootFolder,
		processRunner:          deps.ProcessRunner,
		log:                    deps.Log,
	}
}

// SetMetaWriter updates the metadata writer (late-binding support).
func (h *Handler) SetMetaWriter(mw sfxports.SemanticMetadataWriterPort) {
	h.metaWriter = mw
}

// RegisterRoutes registers the sound_effect sub-routes.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
}

// Generate synthesizes a sound effect and uploads it to Drive.
func (h *Handler) Generate(c *gin.Context) {
	var req struct {
		Name     string  `json:"name" binding:"required"`
		Duration float64 `json:"duration"` // Default/max 3 seconds
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		apiutil.BadRequest(c, "name is required")
		return
	}

	duration := req.Duration
	if duration <= 0 || duration > 3.0 {
		duration = 3.0
	}

	ctx := c.Request.Context()

	// 1. Synthesize the sound effect using the Python synth script
	tempDir := filepath.Join("data", "tmp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to create temp directory %q: %w", tempDir, err))
		return
	}

	tempWav := filepath.Join(tempDir, fmt.Sprintf("sfx_raw_%d.wav", time.Now().UnixNano()))
	tempFile := filepath.Join(tempDir, fmt.Sprintf("sfx_raw_%d.mp3", time.Now().UnixNano()))

	result, err := h.processRunner.Run(ctx, "python3", []string{"scripts/synth_sfx.py",
		"--name", name,
		"--duration", fmt.Sprintf("%f", duration),
		"--output", tempWav,
	}, appassets.DefaultProcessOptions())
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("python synth failed: %w, output: %s", err, result.Output))
		return
	}
	defer os.Remove(tempWav)

	result, err = h.processRunner.Run(ctx, "ffmpeg", []string{"-y", "-i", tempWav,
		"-acodec", "libmp3lame", tempFile,
	}, appassets.DefaultProcessOptions())
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("ffmpeg conversion failed: %w, output: %s", err, result.Output))
		return
	}
	defer os.Remove(tempFile)

	// 2. Compute file hash. COMPAT-ONLY MD5 via the canonical checksum
	// owner (internal/platform/checksum): the persisted file_hash column /
	// sfx_<hash> asset ID predate the digest SSOT and must stay
	// byte-identical — content identity is SHA-256, never MD5.
	hashStr, err := checksum.LegacyMD5File(tempFile)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to compute hash: %w", err))
		return
	}

	// 3. Resolve destination paths (typed-port adapter around drive.Resolver).
	destReq := sfxports.AssetDestinationRequest{
		Source:    "sound_effect",
		MediaType: "sound_effect",
		Group:     name,
		Hash:      hashStr,
		Ext:       ".mp3",
	}
	dest, err := h.resolver.Resolve(destReq)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("destination resolution failed: %w", err))
		return
	}

	// 4. Save local file in final directory
	if err := os.MkdirAll(filepath.Dir(dest.LocalPath), 0755); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to create directory: %w", err))
		return
	}
	if err := os.Rename(tempFile, dest.LocalPath); err != nil {
		inputData, err := os.ReadFile(tempFile)
		if err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to read temp file: %w", err))
			return
		}
		if err := os.WriteFile(dest.LocalPath, inputData, 0644); err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to write local path: %w", err))
			return
		}
	}

	// 5. Generate semantic metadata + upload to Drive via Publisher (FASE 7)
	var driveFileID, driveLink, parentFolderID string
	tags := []string{"sound_effect", name}
	searchText := name + " sound effect sfx audio"

	if h.metaWriter != nil {
		writeReq := sfxports.MetadataWriteRequest{
			AssetID:   "sfx_" + hashStr[:12],
			AssetType: "audio",
			MediaType: "sound_effect",
			Source:    "sound_effect",
			Generator: "ffmpeg",
			Style:     name,
			Prompt:    name + " sound effect",
			LocalPath: dest.LocalPath,
		}
		writeRes, err := h.metaWriter.Write(ctx, writeReq)
		if err != nil {
			h.log.Warn("failed to write semantic metadata.json locally", zap.Error(err))
		} else if writeRes != nil {
			if writeRes.SearchText != "" {
				searchText = writeRes.SearchText
			}
			if len(writeRes.Tags) > 0 {
				tags = writeRes.Tags
			}
		}
	}

	if h.publisher != nil {
		pubReq := delivery.PublishRequest{
			Destination: delivery.DestinationSoundEffect,
			LocalPath:   dest.LocalPath,
			Filename:    filepath.Base(dest.LocalPath),
			Group:       name,
		}
		pubResult, err := h.publisher.Publish(ctx, pubReq)
		if err != nil {
			h.log.Error("failed to publish sound effect to Drive", zap.Error(err))
		} else {
			driveFileID = pubResult.FileID
			driveLink = pubResult.WebViewLink
			parentFolderID = pubResult.FolderID
			h.log.Info("published sound effect to Drive",
				zap.String("file_id", pubResult.FileID),
				zap.String("folder_id", pubResult.FolderID))
		}

		// Upload metadata.json sidecar (best effort). F2.10:
		// the legacy `else if h.driveUploader != nil && h.soundEffectsRootFolder != "" {
		// GetOrCreateFolder + UploadFile }` fallback is RETIRED
		// (override brutal); Publisher is the single canonical
		// canal. When publisher is nil, the sidecar is skipped
		// silently (the parent function fails-closed via the
		// dispatcher's 503 path regardless).
		localMetaPath := filepath.Join(filepath.Dir(dest.LocalPath), "metadata.json")
		if parentFolderID != "" {
			if _, err := os.Stat(localMetaPath); err == nil {
				// PR-P12-SOUND-EFFECT-SIDECAR (July 2026): the sidecar
				// publish now uses the canonical semantic routing via
				// DestinationSoundEffectSidecar + Group=name. The
				// DestinationRegistry maps this key to the same
				// <root>/<name>/ folder as the audio (PathBuilder =
				// SoundEffectPath) but with ConflictOverwrite
				// (regenerable metadata.json — latest wins). The
				// pre-PR-12 ParentFolderID=parentFolderID bypass
				// is RETIRED per godlike/07 NO-FAKE-AVAILABILITY:
				// the canonical Publisher seam now resolves the
				// folder via DestinationRegistry + DestinationPolicy
				// for the sidecar key, identical to the audio path
				// but with a different conflict policy.
				metaPubReq := delivery.PublishRequest{
					Destination: delivery.DestinationSoundEffectSidecar,
					LocalPath:   localMetaPath,
					Filename:    "metadata.json",
					Group:       name, // same as the audio publish — co-locates in <root>/<name>/
				}
				if _, err := h.publisher.Publish(ctx, metaPubReq); err != nil {
					h.log.Error("failed to publish metadata.json to Drive", zap.Error(err))
				} else {
					h.log.Info("metadata.json published to Drive successfully")
				}
			}
		}
	}

	// 6. Save metadata record to SQLite DB
	clip := asset.Asset{
		ID:             "sfx_" + hashStr[:12],
		Name:           name,
		Filename:       filepath.Base(dest.LocalPath),
		Group:          name,
		MediaType:      asset.MediaType("sound_effect"),
		Source:         asset.Source("sound_effect"),
		Duration:       time.Duration(duration) * time.Second,
		LifecycleState: asset.StateActive,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Tags:           tags,
		SearchText:     searchText,
	}
	clip.SetIsFolder(false)
	clip.SetDriveLink(driveLink)
	clip.SetDriveFileID(driveFileID)
	clip.SetParentFolderID(parentFolderID)
	clip.SetLocalPath(dest.LocalPath)
	// Stash the MD5 content hash on the asset so the dispatcher's
	// supersede-gate dedup uses the ingest-time fingerprint (mirrors
	// the contract pinned at
	// internal/application/assets/catalogsync/dispatcher_test.go::TestUpsertPreservingExisting_DispatcherPath).
	clip.SetLegacyFileMD5(hashStr)

	// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): the legacy
	// `if h.clipsRepo != nil { Upsert }` path bypasses the outbox —
	// a freshly-written media_assets row would never reach Qdrant and
	// would corrupt semantic search. Replace with the canonical
	// dispatcher.EnqueueAndIndex: it atomically UPSERTs media_assets
	// and emits asset.index.requested in a single transaction. A nil
	// dispatcher is a wiring error (composition root must supply
	// one), so we fail closed with HTTP 503 rather than swallow the
	// write or fall back to the legacy bypass.
	if h.dispatcher == nil {
		h.log.Error("sfx Generate: dispatcher not wired \u2014 atomic UPSERT + outbox enqueue refused",
			zap.String("clip_id", clip.ID))
		apiutil.Error(c, 503, errSfxDispatcherUnavailable)
		return
	}
	if err := h.dispatcher.EnqueueAndIndex(ctx, &clip, hashStr); err != nil {
		// Mirror the clips writers' sentinel-branch pattern (PR 6
		// consistency): when the SSOT dispatcher returns
		// ErrDispatcherUnavailable, surface 503 verbatim so operators
		// see the canonical "AssetMutationDispatcher not wired"
		// message and can correlate against the upstream sentinel.
		if errors.Is(err, mutations.ErrDispatcherUnavailable) {
			h.log.Error("sfx Generate: dispatcher unavailable",
				zap.String("clip_id", clip.ID), zap.Error(err))
			apiutil.Error(c, 503, errSfxDispatcherUnavailable)
			return
		}
		h.log.Error("sfx Generate: dispatcher.EnqueueAndIndex failed",
			zap.String("clip_id", clip.ID), zap.Error(err))
		apiutil.InternalError(c, fmt.Errorf("dispatcher.EnqueueAndIndex: %w", err))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"clip_id":   clip.ID,
		"name":      clip.Name,
		"local":     clip.LocalPath(),
		"drive_id":  clip.DriveFileID(),
		"drive_url": clip.DriveLink(),
		"duration":  clip.Duration.Milliseconds(),
		"tags":      clip.Tags,
	})
}
