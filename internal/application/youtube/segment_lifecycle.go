package youtube

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/core/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
)

// buildClipMetadata creates the lifecycle.FinalizeInput for a processed clip.
// If youtubeMeta is provided, includes YouTube video metadata (title,
// description, tags, language). Returns a pointer to the finalized metadata
// structure; nil fields are encoded as empty strings/maps in the lifecycle.
func buildClipMetadata(clipID, name, localPath, videoID, start, end string,
	startSec, endSec, duration int, folderSlug string,
	shouldNormalize, keepAudio bool,
	driveFolderID, resolvedPath, fileHash string,
	dest *DestinationRequest,
	youtubeMeta *downloader.YouTubeMetadata,
	seg *Segment) *lifecycle.FinalizeInput {

	metadataMap := map[string]any{
		"video_id":         videoID,
		"start":            start,
		"end":              end,
		"start_seconds":    startSec,
		"end_seconds":      endSec,
		"duration_seconds": duration,
		"folder_slug":      folderSlug,
		"normalized":       shouldNormalize,
		"keep_audio":       keepAudio,
	}

	// Include custom metadata from request if provided
	if seg != nil {
		if seg.Summary != "" {
			metadataMap["clip_summary"] = seg.Summary
		}
		if len(seg.Topics) > 0 {
			metadataMap["topics"] = seg.Topics
		}
		if len(seg.Speakers) > 0 {
			metadataMap["speakers"] = seg.Speakers
		}
		if len(seg.MentionedPeople) > 0 {
			metadataMap["mentioned_people"] = seg.MentionedPeople
		}
		if seg.Hook != "" {
			metadataMap["hook"] = seg.Hook
		}
		if seg.QualityScore > 0 {
			metadataMap["quality_score"] = seg.QualityScore
		}
		if seg.SearchVisibility != "" {
			metadataMap["search_visibility"] = seg.SearchVisibility
		}
		if len(seg.Tags) > 0 {
			metadataMap["segment_tags"] = seg.Tags
		}
	}

	// Include YouTube video metadata if available (from yt-dlp --dump-json)
	if youtubeMeta != nil {
		metadataMap["youtube_title"] = youtubeMeta.Title
		metadataMap["youtube_description"] = youtubeMeta.Description
		metadataMap["youtube_language"] = youtubeMeta.Language
		metadataMap["youtube_uploader"] = youtubeMeta.Uploader
		metadataMap["youtube_upload_date"] = youtubeMeta.UploadDate
		metadataMap["youtube_view_count"] = youtubeMeta.ViewCount
		metadataMap["youtube_duration"] = youtubeMeta.Duration
		metadataMap["youtube_video_id"] = youtubeMeta.ID
		metadataMap["youtube_url"] = fmt.Sprintf("https://www.youtube.com/watch?v=%s", youtubeMeta.ID)
		if len(youtubeMeta.Tags) > 0 {
			metadataMap["youtube_tags"] = youtubeMeta.Tags
		}
		if len(youtubeMeta.Chapters) > 0 {
			metadataMap["youtube_chapters"] = youtubeMeta.Chapters
		}
	}
	metadataBytes, _ := json.Marshal(metadataMap)

	folderPath := resolvedPath
	if folderPath == "" && dest != nil {
		folderPath = dest.FolderPath
	}

	return &lifecycle.FinalizeInput{
		ID:           clipID,
		Name:         name,
		Filename:     filepath.Base(localPath),
		Kind:         lifecycle.AssetKindVideo,
		Source:       "youtube",
		Group:        getGroupFromDestination(dest),
		Subfolder:    "",
		LocalPath:    localPath,
		FolderID:     driveFolderID,
		FolderPath:   folderPath,
		DriveLink:    "",
		DriveFileID:  "",
		DownloadLink: "",
		FileHash:     fileHash,
		Metadata:     string(metadataBytes),
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: driveFolderID != "",
		VerifyDB:     true,
	}
}

// fileSizeFromPath returns the file size in bytes, or 0 if the file cannot be stat'd.
func fileSizeFromPath(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
