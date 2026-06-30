// Package segments provides segment-level helpers for YouTube clip extraction.
// During PR5 Phase 4 (June 2026), standalone functions were consolidated into a
// cohesive Service struct to enable dependency injection and smaller call
// signatures.
package usecase

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	types "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// Service provides segment-level helpers for youtube clip extraction.
// Zero dependencies — all state comes from method parameters.
type SegmentsService struct{}

// NewService is the canonical constructor.
func NewSegmentsService() *SegmentsService {
	return &SegmentsService{}
}

// ── Filename and validation helpers ────────────────────────────────────────

// FileSizeFromPath returns the file size in bytes, or 0 if the file cannot be stat'd.
func (s *SegmentsService) FileSizeFromPath(path string) int64 {
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// BuildClipFilename constructs a canonical YouTube clip filename from video ID,
// timestamps, a human-readable name, AND the policy version.
//
// Commit 2/6 (PR-C-YouTube-Cutover, June 2026, Correttezza #4): the
// policyVersion is stamped into the filename so two policy versions
// of the same (videoID, start, end) tuple produce different files
// (different local paths + different Drive names). Without the stamp,
// re-extraction under a bumped policy version would silently overwrite
// the previous clip file in Drive. Format:
//
//	yt_<videoID>_<startSec>_<endSec>_<policyVersion>_<slug>.mp4
//
// The clipID (yt_<videoID>_<start>_<end>_<policyVer>) is the canonical
// primary key; the filename adds the slug so operators can locate
// clips by name in Drive.
func (s *SegmentsService) BuildClipFilename(videoID string, startSec, endSec int, name, policyVersion string) string {
	slug := textutil.SlugifyWithMax(name, 40)
	if slug == "" {
		slug = "clip"
	}
	if len(slug) > 0 && slug[0] >= '0' && slug[0] <= '9' {
		slug = "c_" + slug
	}
	if policyVersion == "" {
		policyVersion = ProcessSegmentPolicyVersion
	}
	return fmt.Sprintf("yt_%s_%d_%d_%s_%s.mp4", videoID, startSec, endSec, policyVersion, slug)
}

// SanitizeTimestamp validates a timestamp string format (SS, MM:SS, or HH:MM:SS).
func (s *SegmentsService) SanitizeTimestamp(ts string) error {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return fmt.Errorf("timestamp is required")
	}
	parts := strings.Split(ts, ":")
	if len(parts) > 3 {
		return fmt.Errorf("invalid timestamp format: %s", ts)
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid timestamp: %s", ts)
			}
		}
	}
	return nil
}

// ── Metadata builder ───────────────────────────────────────────────────────

// BuildClipMetadataInput groups all parameters needed to construct a
// lifecycle.FinalizeInput for a processed YouTube clip. Replaces the
// previous 17-parameter function signature (PR5 Phase 4).
type BuildClipMetadataInput struct {
	ClipID, Name, LocalPath, VideoID, Start, End string
	StartSec, EndSec, Duration                   int
	FolderSlug                                   string
	ShouldNormalize, KeepAudio                   bool
	DriveFolderID, FolderPath, FileHash, Group   string
	YouTubeMeta                                  *ports.DownloaderMetadata
	Segment                                      *types.Segment
}

// BuildClipMetadata creates the lifecycle.FinalizeInput for a processed clip.
func (s *SegmentsService) BuildClipMetadata(in BuildClipMetadataInput) *lifecycle.FinalizeInput {
	metadataMap := map[string]any{
		"video_id":         in.VideoID,
		"start":            in.Start,
		"end":              in.End,
		"start_seconds":    in.StartSec,
		"end_seconds":      in.EndSec,
		"duration_seconds": in.Duration,
		"folder_slug":      in.FolderSlug,
		"normalized":       in.ShouldNormalize,
		"keep_audio":       in.KeepAudio,
	}

	// Include custom metadata from request if provided
	if in.Segment != nil {
		seg := in.Segment
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

	// Include YouTube video metadata if available
	if in.YouTubeMeta != nil {
		metadataMap["youtube_title"] = in.YouTubeMeta.Title
		metadataMap["youtube_description"] = in.YouTubeMeta.Description
		metadataMap["youtube_language"] = in.YouTubeMeta.Language
		metadataMap["youtube_uploader"] = in.YouTubeMeta.Uploader
		metadataMap["youtube_upload_date"] = in.YouTubeMeta.UploadDate
		metadataMap["youtube_view_count"] = in.YouTubeMeta.ViewCount
		metadataMap["youtube_duration"] = in.YouTubeMeta.Duration
		metadataMap["youtube_video_id"] = in.YouTubeMeta.ID
		metadataMap["youtube_url"] = fmt.Sprintf("https://www.youtube.com/watch?v=%s", in.YouTubeMeta.ID)
		if len(in.YouTubeMeta.Tags) > 0 {
			metadataMap["youtube_tags"] = in.YouTubeMeta.Tags
		}
		if len(in.YouTubeMeta.Chapters) > 0 {
			metadataMap["youtube_chapters"] = in.YouTubeMeta.Chapters
		}
	}
	metadataBytes, _ := json.Marshal(metadataMap)

	return &lifecycle.FinalizeInput{
		ID:           in.ClipID,
		Name:         in.Name,
		Filename:     filepath.Base(in.LocalPath),
		Kind:         lifecycle.AssetKindVideo,
		Source:       "youtube",
		Group:        in.Group,
		Subfolder:    "",
		LocalPath:    in.LocalPath,
		FolderID:     in.DriveFolderID,
		FolderPath:   in.FolderPath,
		DriveLink:    "",
		DriveFileID:  "",
		DownloadLink: "",
		FileHash:     in.FileHash,
		Metadata:     string(metadataBytes),
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: in.DriveFolderID != "",
		VerifyDB:     true,
	}
}
