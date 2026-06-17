package sources

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/media/models"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/apiutil"
)

type SoundEffectHandler struct {
	clipsRepo              *clips.Repository
	driveUploader          *drive.Uploader
	metaWriter             *semantic.MetadataWriter
	resolver               *storage.Resolver
	soundEffectsRootFolder string
	log                    *zap.Logger
}

func NewSoundEffectHandler(
	clipsRepo *clips.Repository,
	driveUploader *drive.Uploader,
	metaWriter *semantic.MetadataWriter,
	soundEffectsRootFolder string,
	log *zap.Logger,
) *SoundEffectHandler {
	r := storage.NewResolver("data", "")
	return &SoundEffectHandler{
		clipsRepo:              clipsRepo,
		driveUploader:          driveUploader,
		metaWriter:             metaWriter,
		resolver:               r,
		soundEffectsRootFolder: soundEffectsRootFolder,
		log:                    log,
	}
}

func (h *SoundEffectHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/generate", h.Generate)
}

func (h *SoundEffectHandler) Generate(c *gin.Context) {
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
		duration = 3.0 // Cap at 3 seconds
	}

	ctx := c.Request.Context()

	// 1. Synthesize the sound effect using the Python synth script
	tempDir := filepath.Join("data", "tmp")
	_ = os.MkdirAll(tempDir, 0755)

	tempWav := filepath.Join(tempDir, fmt.Sprintf("sfx_raw_%d.wav", time.Now().UnixNano()))
	tempFile := filepath.Join(tempDir, fmt.Sprintf("sfx_raw_%d.mp3", time.Now().UnixNano()))

	synthCmd := exec.CommandContext(ctx, "python3", "scripts/synth_sfx.py",
		"--name", name,
		"--duration", fmt.Sprintf("%f", duration),
		"--output", tempWav,
	)
	if out, err := synthCmd.CombinedOutput(); err != nil {
		apiutil.InternalError(c, fmt.Errorf("python synth failed: %w, output: %s", err, string(out)))
		return
	}
	defer os.Remove(tempWav)

	ffmpegCmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", tempWav,
		"-acodec", "libmp3lame", tempFile,
	)
	if out, err := ffmpegCmd.CombinedOutput(); err != nil {
		apiutil.InternalError(c, fmt.Errorf("ffmpeg conversion failed: %w, output: %s", err, string(out)))
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

	// 3. Resolve destination paths
	destReq := storage.AssetDestinationRequest{
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

	// 5. Generate and write semantic metadata.json locally and upload to Google Drive
	var driveFileID, driveLink, parentFolderID string
	tags := []string{"sound_effect", name}
	searchText := name + " sound effect sfx audio"

	if h.metaWriter != nil {
		writeReq := semantic.WriteRequest{
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
		} else if writeRes != nil && writeRes.Payload != nil {
			if writeRes.Payload.SearchText != "" {
				searchText = writeRes.Payload.SearchText
			}
			if len(writeRes.Payload.Tags) > 0 {
				tags = writeRes.Payload.Tags
			}

			if h.driveUploader != nil && h.soundEffectsRootFolder != "" {
				parentFolderID, err = h.driveUploader.GetOrCreateFolder(ctx, name, h.soundEffectsRootFolder)
				if err == nil {
					// Upload the sound effect MP3 first
					uploadRes, err := h.driveUploader.UploadFile(ctx, dest.LocalPath, parentFolderID, filepath.Base(dest.LocalPath))
					if err == nil {
						driveFileID = uploadRes.FileID
						driveLink = uploadRes.WebViewLink
					} else {
						h.log.Error("failed to upload sound effect to Drive", zap.Error(err))
					}

					// Upload the metadata.json
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
		// Fallback: standard upload without metadata.json if metaWriter is not configured
		if h.driveUploader != nil && h.soundEffectsRootFolder != "" {
			parentFolderID, err = h.driveUploader.GetOrCreateFolder(ctx, name, h.soundEffectsRootFolder)
			if err == nil {
				uploadRes, err := h.driveUploader.UploadFile(ctx, dest.LocalPath, parentFolderID, filepath.Base(dest.LocalPath))
				if err == nil {
					driveFileID = uploadRes.FileID
					driveLink = uploadRes.WebViewLink
				}
			}
		}
	}

	// 6. Save metadata record to SQLite DB
	clip := models.MediaAsset{
		ID:             "sfx_" + hashStr[:12],
		Name:           name,
		Filename:       filepath.Base(dest.LocalPath),
		IsFolder:       false,
		Group:          name,
		MediaType:      "sound_effect",
		DriveLink:      driveLink,
		DriveFileID:    driveFileID,
		ParentFolderID: parentFolderID,
		Source:         "sound_effect",
		Duration:       int(duration * 1000),
		LocalPath:      dest.LocalPath,
		Status:         "ready",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Tags:           tags,
		SearchText:     searchText,
	}

	if h.clipsRepo != nil {
		if err := h.clipsRepo.UpsertClip(ctx, &clip); err != nil {
			apiutil.InternalError(c, fmt.Errorf("failed to save clip record to DB: %w", err))
			return
		}
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"clip_id":   clip.ID,
		"name":      clip.Name,
		"local":     clip.LocalPath,
		"drive_id":  clip.DriveFileID,
		"drive_url": clip.DriveLink,
		"duration":  clip.Duration,
		"tags":      clip.Tags,
	})
}
