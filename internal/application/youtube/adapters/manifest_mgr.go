package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	ptrutil "github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"

	"go.uber.org/zap"
)

// ── Drive destination ───────────────────────────────────────────────────

// resolveDriveDestination resolves the Google Drive folder for the extraction output.
func (s *Service) resolveDriveDestination(ctx context.Context, req *youtubetypes.ExtractRequest, videoID string) (string, string) {
	if s.assetDestResolver == nil || req.Destination == nil {
		return "", ""
	}

	destReq := &asset.ResolveRequest{
		Source:          "youtube",
		Group:           req.Destination.Group,
		FolderID:        req.Destination.FolderID,
		FolderPath:      req.Destination.FolderPath,
		SubfolderName:   strings.TrimPrefix(req.Destination.SubfolderName, "yt_"),
		CreateSubfolder: req.Destination.CreateSubfolder,
	}

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

// ── Clip folder ──────────────────────────────────────────────────────────

// loadClipFolder loads an existing clip folder from DB or creates a new one.
func (s *Service) loadClipFolder(ctx context.Context, videoID, outDir, driveFolderID, resolvedPath string,
	resp *youtubetypes.ExtractResponse, req *youtubetypes.ExtractRequest) *asset.ClipFolder {
	if s.clips == nil {
		return nil
	}

	folderID := fmt.Sprintf("clipfolder_youtube_%s", videoID)
	existingFolder, err := s.clips.GetFolder(ctx, folderID)
	if err == nil && existingFolder != nil {
		s.log.Info("loaded existing clip folder", zap.String("folder_id", folderID))

		if driveFolderID != "" {
			existingFolder.FolderID = driveFolderID
			existingFolder.FolderPath = resolvedPath
			existingFolder.Group = getGroupFromDest(req.Destination)
		}
		if existingFolder.LocalFolderPath != outDir {
			existingFolder.LocalFolderPath = outDir
			existingFolder.ManifestTXTPath = filepath.Join(outDir, "clip_manifest.txt")
			existingFolder.ManifestJSONPath = filepath.Join(outDir, "clip_manifest.json")
		}
		return existingFolder
	}

	clipFolder := &asset.ClipFolder{
		ID:               folderID,
		Source:           "youtube",
		SourceURL:        resp.SourceURL,
		VideoID:          resp.VideoID,
		FolderID:         driveFolderID,
		FolderPath:       resolvedPath,
		LocalFolderPath:  outDir,
		Group:            getGroupFromDest(req.Destination),
		ManifestTXTPath:  filepath.Join(outDir, "clip_manifest.txt"),
		ManifestJSONPath: filepath.Join(outDir, "clip_manifest.json"),
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	s.log.Info("created new clip folder", zap.String("folder_id", folderID))
	return clipFolder
}

// ── Manifest ─────────────────────────────────────────────────────────────

// loadManifest loads an existing clip manifest from disk or creates a new one.
func (s *Service) loadManifest(clipFolder *asset.ClipFolder, folderSlug, outDir, driveFolderID, resolvedPath, sourceURL, youtubeVideoID string, mergeExisting bool) *asset.ClipManifest {
	folderID := fmt.Sprintf("clipfolder_youtube_%s", folderSlug)
	manifest := &asset.ClipManifest{
		ID:              folderID,
		FolderID:        driveFolderID,
		FolderPath:      resolvedPath,
		Source:          "youtube",
		SourceURL:       sourceURL,
		VideoID:         youtubeVideoID,
		FolderSlug:      folderSlug,
		LocalFolderPath: outDir,
		Clips:           []asset.ClipManifestItem{},
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

// updateManifest updates the clip manifest with the processed segment.
func (s *Service) updateManifest(manifest *asset.ClipManifest, seg youtubetypes.Segment, clipID string, item youtubetypes.ExtractItem,
	startSec, endSec, duration int, localPath, fileHash string) {
	if manifest == nil {
		return
	}

	filename := item.Filename
	if filename == "" && localPath != "" {
		filename = filepath.Base(localPath)
	}
	if filename == "." {
		filename = ""
	}

	newMItem := asset.ClipManifestItem{
		ID:              clipID,
		Name:            item.Name,
		Start:           item.Start,
		End:             item.End,
		StartSeconds:    startSec,
		EndSeconds:      endSec,
		DurationSeconds: duration,
		Filename:        filename,
		LocalPath:       item.LocalPath,
		DriveLink:       item.DriveLink,
		FileHash:        fileHash,
		Status:          item.Status,
		Tags:            append([]string(nil), seg.Tags...),
	}

	// Read per-clip metadata file to enrich the combined manifest
	perClipMetaPath := filepath.Join(filepath.Dir(localPath), "metadata_"+clipID+".json")
	if metaBytes, err := os.ReadFile(perClipMetaPath); err == nil {
		var clipMeta youtubetypes.ClipMetadataFile
		if err := json.Unmarshal(metaBytes, &clipMeta); err == nil {
			newMItem.RawName = clipMeta.RawTitle
			newMItem.CleanTitle = clipMeta.CleanTitle
			newMItem.ShortTitle = clipMeta.ShortTitle
			newMItem.EmbeddingText = clipMeta.EmbeddingText
			newMItem.VideoTitle = clipMeta.VideoTitle
			newMItem.Channel = clipMeta.Channel
			newMItem.Description = clipMeta.Description
			newMItem.RawTranscript = clipMeta.RawTranscript
			newMItem.Transcript = clipMeta.Transcript
			newMItem.CleanTranscript = clipMeta.CleanTranscript
			newMItem.ClipSummary = clipMeta.ClipSummary
			newMItem.Hook = clipMeta.Hook
			newMItem.Topics = append([]string(nil), clipMeta.Topics...)
			newMItem.Speakers = append([]string(nil), clipMeta.Speakers...)
			newMItem.People = append([]string(nil), clipMeta.People...)
			newMItem.MentionedPeople = append([]string(nil), clipMeta.MentionedPeople...)
			newMItem.SourceTags = append([]string(nil), clipMeta.SourceTags...)
			newMItem.ClipTags = append([]string(nil), clipMeta.ClipTags...)
			newMItem.SearchKeywords = append([]string(nil), clipMeta.SearchKeywords...)
			newMItem.QualityScore = clipMeta.QualityScore
			newMItem.SearchVisibility = clipMeta.SearchVisibility
			newMItem.DuplicateGroupID = clipMeta.DuplicateGroupID
			newMItem.DuplicateOf = clipMeta.DuplicateOf
			newMItem.IsDuplicate = clipMeta.IsDuplicate
			newMItem.IsBestVersion = clipMeta.IsBestVersion
			newMItem.DuplicateReason = clipMeta.DuplicateReason
			newMItem.DuplicateScore = clipMeta.DuplicateScore
			newMItem.TopicClusterID = clipMeta.TopicClusterID
			newMItem.TopicClusterLabel = clipMeta.TopicClusterLabel
			newMItem.TopicClusterSize = clipMeta.TopicClusterSize
			newMItem.TopicClusterRank = clipMeta.TopicClusterRank
			newMItem.YouTubeURL = clipMeta.YouTubeURL
			if len(clipMeta.Tags) > 0 {
				tagSet := make(map[string]struct{}, len(newMItem.Tags)+len(clipMeta.Tags))
				merged := make([]string, 0, len(newMItem.Tags)+len(clipMeta.Tags))
				for _, tag := range newMItem.Tags {
					normalized := strings.ToLower(strings.TrimSpace(tag))
					if normalized == "" {
						continue
					}
					if _, ok := tagSet[normalized]; ok {
						continue
					}
					tagSet[normalized] = struct{}{}
					merged = append(merged, tag)
				}
				for _, tag := range clipMeta.Tags {
					normalized := strings.ToLower(strings.TrimSpace(tag))
					if normalized == "" {
						continue
					}
					if _, ok := tagSet[normalized]; ok {
						continue
					}
					tagSet[normalized] = struct{}{}
					merged = append(merged, tag)
				}
				newMItem.Tags = merged
			}
		}
	}

	// Replace existing or append new
	for j, mItem := range manifest.Clips {
		if mItem.ID == clipID {
			manifest.Clips[j] = newMItem
			return
		}
	}
	manifest.Clips = append(manifest.Clips, newMItem)
}

// saveManifest writes the manifest JSON and TXT files, uploads to Drive,
// and updates the clip folder in DB.
func (s *Service) saveManifest(ctx context.Context, clipFolder *asset.ClipFolder, manifest *asset.ClipManifest,
	req *youtubetypes.ExtractRequest, outDir string) {
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

	if manifest != nil {
		if err := s.folderMemory.SaveManifest(clipFolder.ManifestJSONPath, manifest); err != nil {
			s.log.Warn("failed to write manifest JSON", zap.Error(err))
		} else {
			s.log.Info("manifest JSON updated", zap.String("path", clipFolder.ManifestJSONPath))
		}
	}

	writeSummary := ptrutil.BoolDefault(req.WriteSummary, true)
	if writeSummary && clipFolder.ManifestTXTPath != "" {
		if err := s.folderMemory.UpdateManifestTXT(clipFolder, manifest); err != nil {
			s.log.Warn("failed to write manifest TXT", zap.Error(err))
		} else {
			s.log.Info("manifest TXT updated", zap.String("path", clipFolder.ManifestTXTPath))
		}
	}

	targetFolderID := clipFolder.FolderID

	// If no explicit folder ID, resolve category folder
	if targetFolderID == "" {
		clipsRoot := s.cfg.ClipsFolderID
		if clipsRoot != "" && clipFolder.LocalFolderPath != "" {
			categoryDir := filepath.Base(filepath.Dir(clipFolder.LocalFolderPath))
			clipsRootRel := filepath.Base(filepath.Dir(filepath.Dir(clipFolder.LocalFolderPath)))
			if clipsRootRel == "clips" && categoryDir != "" && categoryDir != "." && categoryDir != "clips" {
				if catID, err := s.callbacks.DriveGetOrCreateFolder(ctx, categoryDir, clipsRoot); err == nil {
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

				if targetFolderID != "" {
					if _, skipped, err := s.callbacks.DriveUploadFileIfChanged(ctx, unifiedMetaPath, targetFolderID, "metadata_unified.json", "", ""); err != nil {
						s.log.Warn("failed to upload metadata_unified.json to Drive", zap.Error(err))
					} else if skipped {
						s.log.Info("metadata_unified.json unchanged on Drive, skipped re-upload")
					} else {
						s.log.Info("metadata_unified.json uploaded to Drive successfully")
					}
				}
			}
		}
	}

	// ── Upload manifest to Drive as metadata.json (backward compat) ──
	if clipFolder.ManifestJSONPath != "" && targetFolderID != "" {
		result, skipped, err := s.callbacks.DriveUploadFileIfChanged(ctx, clipFolder.ManifestJSONPath, targetFolderID, "metadata.json", "", "")
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

	if err := s.clips.UpsertFolder(ctx, clipFolder); err != nil {
		s.log.Warn("failed to upsert clip folder", zap.Error(err))
	}
}

// ── Monitored source ─────────────────────────────────────────────────────

func (s *Service) updateMonitoredSourceStatus(ctx context.Context, ms *asset.MonitoredSource, resp *youtubetypes.ExtractResponse) {
	if s.monitors == nil {
		return
	}

	if resp.Stats.Failed == resp.Stats.Requested {
		ms.Status = "failed"
	} else {
		ms.Status = "processed"
	}

	if err := s.monitors.UpsertSource(ctx, ms); err != nil {
		s.log.Error("Failed to update monitored source status", zap.Error(err))
	}
	if resp.Stats.Failed != resp.Stats.Requested {
		if err := s.monitors.IncrementProcessed(ctx, ms.ID); err != nil {
			s.log.Error("Failed to increment processed count", zap.Error(err))
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────

func defaultConcurrency(reqConcurrency int) int {
	if reqConcurrency > 0 {
		return reqConcurrency
	}
	return 3
}

func getGroupFromDest(dest *youtubetypes.DestinationRequest) string {
	if dest == nil {
		return ""
	}
	return dest.Group
}
