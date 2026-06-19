package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/destination"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	ptrutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

	"go.uber.org/zap"
)

// resolveDriveDestination resolves the Google Drive folder for the extraction output.
//
// The FolderID provided by the caller is used as-is — the caller (monitor or
// API handler) is responsible for pre-resolving the per-channel subfolder if
// needed. This function only creates a video-level subfolder inside the
// resolved FolderID (e.g. Root/<channel>/video-title/).
func (s *Service) resolveDriveDestination(ctx context.Context, req *ExtractRequest, videoID string) (string, string) {
	if s.assetDestResolver == nil || req.Destination == nil {
		return "", ""
	}

	destReq := &destination.ResolveRequest{
		Source:          "youtube",
		Group:           req.Destination.Group,
		FolderID:        req.Destination.FolderID,
		FolderPath:      req.Destination.FolderPath,
		SubfolderName:   strings.TrimPrefix(req.Destination.SubfolderName, "yt_"),
		CreateSubfolder: req.Destination.CreateSubfolder,
	}

	// Auto-assign a video subfolder inside the resolved parent.
	// When FolderID is NOT set: the resolver creates a category folder under clips root,
	// then a subfolder for this video inside it (e.g. clips/rap/50-cent).
	//
	// When FolderID IS set (now the channel subfolder, pre-resolved by caller):
	// the video subfolder is created INSIDE the channel folder (e.g. AmeliaDimoldenberg/paul-mccartney-chicken-shop-date).
	// This keeps clips organized by video inside the correct channel folder.
	if destReq.FolderID == "" || destReq.Group != "" {
		if destReq.SubfolderName == "" {
			destReq.SubfolderName = strings.TrimPrefix(videoID, "yt_")
			destReq.CreateSubfolder = true
			s.log.Info("auto-assigning video subfolder", zap.String("subfolder", destReq.SubfolderName))
		} else {
			destReq.SubfolderName = strings.TrimPrefix(destReq.SubfolderName, "yt_")
			destReq.CreateSubfolder = true
			s.log.Info("using user-specified video subfolder", zap.String("subfolder", destReq.SubfolderName))
		}
	} else if destReq.SubfolderName != "" {
		// FolderID provided without Group — user wants a subfolder inside it
		destReq.SubfolderName = strings.TrimPrefix(destReq.SubfolderName, "yt_")
		destReq.CreateSubfolder = true
		s.log.Info("creating subfolder inside explicit Drive folder", zap.String("folder_id", destReq.FolderID), zap.String("subfolder", destReq.SubfolderName))
	}

	resolved, err := s.assetDestResolver.Resolve(ctx, destReq)
	if err != nil {
		s.log.Warn("failed to resolve drive destination", zap.Error(err))
		return "", ""
	}
	return resolved.FolderID, resolved.FolderPath
}

// loadClipFolder loads an existing clip folder from DB or creates a new one.
func (s *Service) loadClipFolder(ctx context.Context, videoID, outDir, driveFolderID, resolvedPath string,
	resp *ExtractResponse, req *ExtractRequest) *models.ClipFolder {
	if s.clipsRepo == nil {
		return nil
	}

	folderID := fmt.Sprintf("clipfolder_youtube_%s", videoID)
	existingFolder, err := s.clipsRepo.GetFolder(ctx, folderID)
	if err == nil && existingFolder != nil {
		s.log.Info("loaded existing clip folder", zap.String("folder_id", folderID))

		// Always update FolderID from the resolved destination — previous runs may
		// have stored the parent folder ID instead of the correct subfolder ID.
		if driveFolderID != "" {
			existingFolder.FolderID = driveFolderID
			existingFolder.FolderPath = resolvedPath
			existingFolder.Group = getGroupFromDestination(req.Destination)
		}
		if existingFolder.LocalFolderPath != outDir {
			existingFolder.LocalFolderPath = outDir
			existingFolder.ManifestTXTPath = filepath.Join(outDir, "clip_manifest.txt")
			existingFolder.ManifestJSONPath = filepath.Join(outDir, "clip_manifest.json")
		}
		return existingFolder
	}

	clipFolder := &models.ClipFolder{
		ID:               folderID,
		Source:           "youtube",
		SourceURL:        resp.SourceURL,
		VideoID:          resp.VideoID, // real YouTube video ID, not folderSlug
		FolderID:         driveFolderID,
		FolderPath:       resolvedPath,
		LocalFolderPath:  outDir,
		Group:            getGroupFromDestination(req.Destination),
		ManifestTXTPath:  filepath.Join(outDir, "clip_manifest.txt"),
		ManifestJSONPath: filepath.Join(outDir, "clip_manifest.json"),
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	s.log.Info("created new clip folder", zap.String("folder_id", folderID))
	return clipFolder
}

// loadManifest loads an existing clip manifest from disk or creates a new one.
func (s *Service) loadManifest(clipFolder *models.ClipFolder, folderSlug, outDir, driveFolderID, resolvedPath, sourceURL, youtubeVideoID string, mergeExisting bool) *models.ClipManifest {
	folderID := fmt.Sprintf("clipfolder_youtube_%s", folderSlug)
	manifest := &models.ClipManifest{
		ID:              folderID,
		FolderID:        driveFolderID,
		FolderPath:      resolvedPath,
		Source:          "youtube",
		SourceURL:       sourceURL,
		VideoID:         youtubeVideoID,
		FolderSlug:      folderSlug,
		LocalFolderPath: outDir,
		Clips:           []models.ClipManifestItem{},
	}

	if !mergeExisting || clipFolder == nil || clipFolder.ManifestJSONPath == "" {
		return manifest
	}

	loadedManifest, err := s.folderMemory.LoadManifest(clipFolder.ManifestJSONPath)
	if err != nil || loadedManifest == nil {
		return manifest
	}

	if loadedManifest.FolderID == "" && driveFolderID != "" {
		loadedManifest.FolderID = driveFolderID
		loadedManifest.FolderPath = resolvedPath
	}
	if loadedManifest.ID == "" {
		loadedManifest.ID = folderID
	}
	if loadedManifest.Source == "" {
		loadedManifest.Source = "youtube"
	}
	if loadedManifest.SourceURL == "" {
		loadedManifest.SourceURL = sourceURL
	}
	if youtubeVideoID != "" {
		loadedManifest.VideoID = youtubeVideoID
	}
	loadedManifest.FolderSlug = folderSlug
	if loadedManifest.LocalFolderPath == "" {
		loadedManifest.LocalFolderPath = outDir
	}
	s.log.Info("loaded existing manifest", zap.Int("clip_count", len(loadedManifest.Clips)))
	return loadedManifest
}

// saveManifest writes the manifest JSON and TXT files, uploads the manifest
// to Drive as a single combined metadata file, and updates the clip folder in DB.
func (s *Service) saveManifest(ctx context.Context, clipFolder *models.ClipFolder, manifest *models.ClipManifest,
	req *ExtractRequest, outDir string) {
	if clipFolder == nil {
		return
	}

	s.enrichManifestIntelligence(ctx, clipFolder, manifest)

	stats := s.folderMemory.ComputeManifestStats(manifest)
	manifest.Stats = stats

	clipFolder.ClipCount = stats.ClipCount
	clipFolder.ProcessedCount = stats.ProcessedCount
	clipFolder.FailedCount = stats.FailedCount
	clipFolder.SkippedCount = stats.SkippedCount
	clipFolder.UpdatedAt = time.Now().UTC()

	// Save manifest JSON locally
	if manifest != nil {
		if err := s.folderMemory.SaveManifest(clipFolder.ManifestJSONPath, manifest); err != nil {
			s.log.Warn("failed to write manifest JSON", zap.Error(err))
		} else {
			s.log.Info("manifest JSON updated", zap.String("path", clipFolder.ManifestJSONPath))
		}
	}

	// Save manifest TXT (respect WriteSummary flag)
	writeSummary := ptrutil.BoolDefault(req.WriteSummary, true)
	if writeSummary && clipFolder.ManifestTXTPath != "" {
		if err := s.folderMemory.UpdateManifestTXT(clipFolder, manifest); err != nil {
			s.log.Warn("failed to write manifest TXT", zap.Error(err))
		} else {
			s.log.Info("manifest TXT updated", zap.String("path", clipFolder.ManifestTXTPath))
		}
	}

	uploader := &drive.Uploader{
		Service: s.driveClient,
		Log:     s.log,
	}

	targetFolderID := clipFolder.FolderID

	// If no explicit folder ID, resolve category folder same way as writeClipMetadataFile
	if targetFolderID == "" && s.driveClient != nil {
		clipsRoot := s.cfg.Drive.ClipsFolder()
		if clipsRoot != "" && clipFolder.LocalFolderPath != "" {
			categoryDir := filepath.Base(filepath.Dir(clipFolder.LocalFolderPath))
			clipsRootRel := filepath.Base(filepath.Dir(filepath.Dir(clipFolder.LocalFolderPath)))
			if clipsRootRel == "clips" && categoryDir != "" && categoryDir != "." && categoryDir != "clips" {
				if catID, err := uploader.GetOrCreateFolder(ctx, categoryDir, clipsRoot); err == nil {
					targetFolderID = catID
				}
			}
		}
	}

	// ── Create and write metadata_unified.json ────────
	if manifest != nil {
		unifiedMetaPath := filepath.Join(outDir, "metadata_unified.json")

		type UnifiedClip struct {
			ID              string   `json:"id"`
			Filename        string   `json:"filename"`
			Start           string   `json:"start"`
			End             string   `json:"end"`
			StartSeconds    int      `json:"start_seconds"`
			EndSeconds      int      `json:"end_seconds"`
			DurationSeconds int      `json:"duration_seconds"`
			Title           string   `json:"title"`
			Summary         string   `json:"summary"`
			Hook            string   `json:"hook,omitempty"`
			Transcript      string   `json:"transcript,omitempty"`
			EmbeddingText   string   `json:"embedding_text,omitempty"`
			Tags            []string `json:"tags,omitempty"`
			Topics          []string `json:"topics,omitempty"`
			Speakers        []string `json:"speakers,omitempty"`
			People          []string `json:"people,omitempty"`
			DriveLink       string   `json:"drive_link,omitempty"`
		}

		type UnifiedMetadata struct {
			FolderID   string        `json:"folder_id,omitempty"`
			SourceURL  string        `json:"source_url"`
			VideoID    string        `json:"video_id"`
			VideoTitle string        `json:"video_title,omitempty"`
			Clips      []UnifiedClip `json:"clips"`
		}

		unified := UnifiedMetadata{
			FolderID:  targetFolderID,
			SourceURL: manifest.SourceURL,
			VideoID:   manifest.VideoID,
			Clips:     []UnifiedClip{},
		}

		// Try to find video title from first clip or search
		for _, c := range manifest.Clips {
			if c.VideoTitle != "" {
				unified.VideoTitle = c.VideoTitle
				break
			}
		}

		for _, c := range manifest.Clips {
			title := c.CleanTitle
			if title == "" {
				title = c.Name
			}
			unified.Clips = append(unified.Clips, UnifiedClip{
				ID:              c.ID,
				Filename:        c.Filename,
				Start:           c.Start,
				End:             c.End,
				StartSeconds:    c.StartSeconds,
				EndSeconds:      c.EndSeconds,
				DurationSeconds: c.DurationSeconds,
				Title:           title,
				Summary:         c.ClipSummary,
				Hook:            c.Hook,
				Transcript:      c.CleanTranscript,
				EmbeddingText:   c.EmbeddingText,
				Tags:            c.Tags,
				Topics:          c.Topics,
				Speakers:        c.Speakers,
				People:          c.People,
				DriveLink:       c.DriveLink,
			})
		}

		if data, err := json.MarshalIndent(unified, "", "  "); err == nil {
			if err := os.WriteFile(unifiedMetaPath, data, 0644); err != nil {
				s.log.Warn("failed to write metadata_unified.json locally", zap.Error(err))
			} else {
				s.log.Info("metadata_unified.json updated locally", zap.String("path", unifiedMetaPath))

				// Upload metadata_unified.json to Drive
				if s.driveClient != nil && targetFolderID != "" {
					if result, skipped, err := uploader.UploadFileIfChanged(ctx, unifiedMetaPath, targetFolderID, "metadata_unified.json"); err != nil {
						s.log.Warn("failed to upload metadata_unified.json to Drive", zap.Error(err))
					} else if skipped {
						s.log.Info("metadata_unified.json unchanged on Drive, skipped re-upload")
					} else {
						s.log.Info("metadata_unified.json uploaded to Drive successfully", zap.String("drive_file_id", result.FileID))
					}
				}
			}
		}
	}

	// ── Upload manifest to Drive as single combined metadata file (backward compatibility) ────────
	if s.driveClient != nil && clipFolder.ManifestJSONPath != "" && targetFolderID != "" {
		result, skipped, err := uploader.UploadFileIfChanged(ctx, clipFolder.ManifestJSONPath, targetFolderID, "metadata.json")
		if err != nil {
			s.log.Warn("failed to upload manifest as metadata.json to Drive",
				zap.String("folder_id", targetFolderID),
				zap.Error(err))
		} else if skipped {
			s.log.Info("manifest unchanged, skipped Drive re-upload",
				zap.String("filename", "metadata.json"),
				zap.String("folder_id", targetFolderID),
				zap.String("drive_file_id", result.FileID))
		} else {
			s.log.Info("manifest uploaded to Drive as single metadata file",
				zap.String("filename", "metadata.json"),
				zap.String("drive_file_id", result.FileID),
				zap.String("drive_link", result.WebViewLink))
		}
	}

	// Upsert clip folder to DB
	if err := s.folderMemory.UpsertFolder(ctx, clipFolder); err != nil {
		s.log.Warn("failed to upsert clip folder", zap.Error(err))
	}
}

// defaultConcurrency returns the number of parallel workers for segment processing.
// If reqConcurrency <= 0, uses default of 3 (safe for most connections).
func defaultConcurrency(reqConcurrency int) int {
	if reqConcurrency > 0 {
		return reqConcurrency
	}
	return 3
}

// updateMonitoredSourceStatus sets the final status on the monitored source record.
func (s *Service) updateMonitoredSourceStatus(ctx context.Context, ms *models.MonitoredSource, resp *ExtractResponse) {
	if s.monitoredRepo == nil {
		return
	}

	if resp.Stats.Failed == resp.Stats.Requested {
		ms.Status = "failed"
	} else {
		ms.Status = "processed"
	}

	if err := s.monitoredRepo.UpsertSource(ctx, ms); err != nil {
		s.log.Error("Failed to update monitored source status", zap.Error(err))
	}
	if resp.Stats.Failed != resp.Stats.Requested {
		if err := s.monitoredRepo.IncrementProcessed(ctx, ms.ID); err != nil {
			s.log.Error("Failed to increment processed count", zap.Error(err))
		}
	}
}
