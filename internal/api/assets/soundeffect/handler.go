// Package soundeffect provides thin HTTP handlers for sound effect generation.
//
// PG-003 (June 2026): handler now depends on typed sfxports ports rather
// than concrete *assets.ClipsRepository, *drive.Uploader,
// *semantic.MetadataWriter, *drive.Resolver. Composition root (in
// internal/app/module_assets.go) injects the adapters — see
// internal/app/adapters_soundeffect.go for the concrete implementations.
package soundeffect

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

// Handler manages sound effect generation via Python synth + ffmpeg.
type Handler struct {
	clipsRepo              sfxports.ClipRepositoryPort
	driveUploader          sfxports.DriveUploaderPort
	metaWriter             sfxports.SemanticMetadataWriterPort
	resolver               sfxports.DestinationResolverPort
	soundEffectsRootFolder string
	processRunner          appassets.ProcessRunner
	log                    *zap.Logger
}

// NewHandler creates a sound effect handler.
//
// All concrete infrastructure collaborators are injected via structural
// ports (sfxports.*). The composition root is responsible for instantiating
// the adapters in internal/app and wiring them here.
func NewHandler(
	clipsRepo sfxports.ClipRepositoryPort,
	driveUploader sfxports.DriveUploaderPort,
	metaWriter sfxports.SemanticMetadataWriterPort,
	resolver sfxports.DestinationResolverPort,
	soundEffectsRootFolder string,
	processRunner appassets.ProcessRunner,
	log *zap.Logger,
) *Handler {
	return &Handler{
		clipsRepo:              clipsRepo,
		driveUploader:          driveUploader,
		metaWriter:             metaWriter,
		resolver:               resolver,
		soundEffectsRootFolder: soundEffectsRootFolder,
		processRunner:          processRunner,
		log:                    log,
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
	_ = os.MkdirAll(tempDir, 0755)

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

	// 2. Compute file hash
	f, err := os.Open(tempFile)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to open synthesized file: %w", err))
		return
	}
	hsh := md5.New()
	if _, err := io.Copy(hsh, f); err != nil {
		f.Close()
		apiutil.InternalError(c, fmt.Errorf("failed to compute hash: %w", err))
		return
	}
	f.Close()
	hashStr := fmt.Sprintf("%x", hsh.Sum(nil))

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

	// 5. Generate and write semantic metadata.json + upload to Drive
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

			if h.driveUploader != nil && h.soundEffectsRootFolder != "" {
				parentFolderID, err = h.driveUploader.GetOrCreateFolder(ctx, name, h.soundEffectsRootFolder)
				if err == nil {
					uploadRes, err := h.driveUploader.UploadFile(ctx, dest.LocalPath, parentFolderID, filepath.Base(dest.LocalPath))
					if err == nil {
						driveFileID = uploadRes.FileID
						driveLink = uploadRes.WebViewLink
					} else {
						h.log.Error("failed to upload sound effect to Drive", zap.Error(err))
					}

					localMetaPath := filepath.Join(filepath.Dir(dest.LocalPath), "metadata.json")
					if _, err := os.Stat(localMetaPath); err == nil {
						_, err = h.driveUploader.UploadFile(ctx, localMetaPath, parentFolderID, "metadata.json")
						if err != nil {
							h.log.Error("failed to upload metadata.json to Drive", zap.Error(err))
						} else {
							h.log.Info("metadata.json uploaded to Drive successfully")
						}
					}
				}
			}
		}
	} else {
		if h.driveUploader != nil && h.soundEffectsRootFolder != "" {
			parentFolderID, err = h.driveUploader.GetOrCreateFolder(ctx, name, h.soundEffectsRootFolder)
			if err == nil {
				uploadRes, err := h.driveUploader.UploadFile(ctx, dest.LocalPath, parentFolderID, filepath.Base(dest.LocalPath))
				if err == nil { //nolint:revive // preserve original err-shadow
					driveFileID = uploadRes.FileID
					driveLink = uploadRes.WebViewLink
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
		LifecycleState: asset.StateReady,
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

	if h.clipsRepo != nil {
		if err := h.clipsRepo.Upsert(ctx, &clip); err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to save clip record to DB: %w", err))
			return
		}
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
