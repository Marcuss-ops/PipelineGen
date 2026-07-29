// Package metadata — enrichment helpers.
//
// enrichment.go owns WriteClipMetadataFile and its canonical
// YouTube metadata field accessors (ymDescriptionCanonical,
// ymTagsCanonical, ymCategoriesCanonical, ymViewCountCanonical,
// ymUploadDateCanonical, ymThumbnailURLCanonical).
// Extracted from service.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── WriteClipMetadataFile (ported from usecase/metadata_service_write.go, CLIPS-META-A2) ──

// WriteClipMetadataFile writes the per-clip metadata JSON file alongside the clip MP4.
// CLIPS-META-2026-07-04 (Azione 2): moved from usecase.MetadataService to the canonical
// metadata package as a standalone function. The legacy usecase.EnrichClip now calls
// this canonical function.
func WriteClipMetadataFile(log *zap.Logger, clip *asset.Asset, ym *youtubeports.DownloaderMetadata) {
	if clip == nil || clip.LocalPath() == "" {
		return
	}

	startSec, endSec := parseClipTimestamps(clip.ID)

	durationSec := endSec - startSec
	if durationSec <= 0 {
		durationSec = int(clip.Duration.Seconds())
	}

	youtubeURL := clip.GetMetadataString("youtube_url")
	if youtubeURL == "" && ym != nil && ym.ID != "" {
		youtubeURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", ym.ID)
	}

	tags := tagutil.NormalizeClipTagList(clip.Tags)
	if len(tags) == 0 {
		tags = ymTagsCanonical(ym, clip)
	}
	categories := ymCategoriesCanonical(ym, clip)
	viewCount := ymViewCountCanonical(ym, clip)
	uploadDate := ymUploadDateCanonical(ym, clip)
	thumbnailURL := ymThumbnailURLCanonical(ym, clip)

	transcriptPath := strings.TrimSuffix(clip.LocalPath(), filepath.Ext(clip.LocalPath())) + ".txt"
	transcript := ""
	if transcriptBytes, err := os.ReadFile(transcriptPath); err == nil && len(transcriptBytes) > 0 {
		transcript = strings.TrimSpace(string(transcriptBytes))
	}
	cleanTranscriptText := tagutil.CleanClipTranscript(transcript)

	description := tagutil.CompactYouTubeDescription(ymDescriptionCanonical(ym, clip))
	rawTitle := clip.Name
	cleanTitle := clip.GetMetadataString("clean_title")
	if cleanTitle == "" {
		cleanTitle = clip.Name
	}
	shortTitle := clip.GetMetadataString("short_title")
	clipSummary := clip.GetMetadataString("clip_summary")
	hook := clip.GetMetadataString("hook")
	topics := tagutil.NormalizeClipTagList(asset.MetadataStringSlice(clip.Metadata, "topics"))
	speakers := tagutil.NormalizeClipTagList(asset.MetadataStringSlice(clip.Metadata, "speakers"))
	mentionedPeople := tagutil.NormalizeClipTagList(asset.MetadataStringSlice(clip.Metadata, "mentioned_people"))
	people := tagutil.NormalizeClipTagList(asset.MetadataStringSlice(clip.Metadata, "people"))
	sourceTags := tagutil.NormalizeClipTagList(asset.MetadataStringSlice(clip.Metadata, "source_tags"))
	clipTags := tagutil.NormalizeClipTagList(asset.MetadataStringSlice(clip.Metadata, "clip_tags"))
	searchKeywords := tagutil.NormalizeClipTagList(asset.MetadataStringSlice(clip.Metadata, "search_keywords"))
	embeddingText := clip.GetMetadataString("embedding_text")
	rawTranscript := clip.GetMetadataString("raw_transcript")
	if rawTranscript == "" {
		rawTranscript = transcript
	}
	storedCleanTranscript := clip.GetMetadataString("clean_transcript")
	if storedCleanTranscript == "" {
		storedCleanTranscript = cleanTranscriptText
	}
	videoTitle := clip.GetMetadataString("youtube_title")
	if videoTitle == "" && ym != nil && ym.Title != "" {
		videoTitle = ym.Title
	}

	fallbackTopics, fallbackSpeakers, fallbackMentionedPeople, fallbackSourceTags, fallbackClipTags, fallbackSearchKeywords, _, fallbackHook :=
		tagutil.DeriveFallbackSemanticFields(videoTitle, storedCleanTranscript, description, cleanTitle)
	if len(topics) == 0 {
		topics = fallbackTopics
	}
	if len(speakers) == 0 {
		speakers = fallbackSpeakers
	}
	if len(mentionedPeople) == 0 {
		mentionedPeople = fallbackMentionedPeople
	}
	people = tagutil.MergeTagLists(speakers, mentionedPeople, people)
	if len(sourceTags) == 0 {
		sourceTags = fallbackSourceTags
	}
	if len(clipTags) == 0 {
		clipTags = fallbackClipTags
	}
	if len(searchKeywords) == 0 {
		searchKeywords = fallbackSearchKeywords
	}
	if hook == "" {
		hook = fallbackHook
	}
	if embeddingText == "" {
		embeddingText = tagutil.BuildEmbeddingText(cleanTitle, clipSummary, hook, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, storedCleanTranscript)
	}
	qualityScore := asset.MetadataFloat(clip.Metadata, "quality_score")
	searchVisibility := clip.GetMetadataString("search_visibility")
	if searchVisibility == "" {
		searchVisibility = tagutil.DeriveSearchVisibility(qualityScore)
	}

	meta := youtubetypes.ClipMetadataFile{
		ClipID:            clip.ID,
		ClipTitle:         cleanTitle,
		RawTitle:          rawTitle,
		CleanTitle:        cleanTitle,
		ShortTitle:        shortTitle,
		EmbeddingText:     embeddingText,
		VideoTitle:        videoTitle,
		Channel:           clip.GetMetadataString("youtube_uploader"),
		Description:       description,
		RawTranscript:     rawTranscript,
		Transcript:        rawTranscript,
		CleanTranscript:   storedCleanTranscript,
		ClipSummary:       clipSummary,
		Hook:              hook,
		Topics:            topics,
		Speakers:          speakers,
		MentionedPeople:   mentionedPeople,
		People:            people,
		SourceTags:        sourceTags,
		ClipTags:          clipTags,
		SearchKeywords:    searchKeywords,
		DuplicateGroupID:  clip.GetMetadataString("duplicate_group_id"),
		DuplicateOf:       clip.GetMetadataString("duplicate_of"),
		IsDuplicate:       asset.MetadataBool(clip.Metadata, "is_duplicate"),
		IsBestVersion:     asset.MetadataBool(clip.Metadata, "is_best_version"),
		DuplicateReason:   clip.GetMetadataString("duplicate_reason"),
		DuplicateScore:    asset.MetadataFloat(clip.Metadata, "duplicate_score"),
		TopicClusterID:    clip.GetMetadataString("topic_cluster_id"),
		TopicClusterLabel: clip.GetMetadataString("topic_cluster_label"),
		TopicClusterSize:  asset.MetadataInt(clip.Metadata, "topic_cluster_size"),
		TopicClusterRank:  asset.MetadataInt(clip.Metadata, "topic_cluster_rank"),
		Language:          clip.GetMetadataString("youtube_language"),
		DurationSec:       durationSec,
		StartSec:          startSec,
		EndSec:            endSec,
		Tags:              tags,
		Categories:        categories,
		QualityScore:      qualityScore,
		SearchVisibility:  searchVisibility,
		YouTubeURL:        youtubeURL,
		ThumbnailURL:      thumbnailURL,
		UploadDate:        uploadDate,
		ViewCount:         viewCount,
		LastEnriched:      timeutil.FormatRFC3339(time.Now()),
	}

	if meta.VideoTitle == "" && ym != nil {
		meta.VideoTitle = ym.Title
	}
	if meta.Channel == "" && ym != nil {
		meta.Channel = ym.Uploader
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		if log != nil {
			log.Warn("failed to marshal clip metadata", zap.String("clip_id", clip.ID), zap.Error(err))
		}
		return
	}

	metaFilename := "metadata_" + clip.ID + ".json"
	metaPath := filepath.Join(filepath.Dir(clip.LocalPath()), metaFilename)
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		if log != nil {
			log.Warn("failed to write clip metadata file", zap.String("clip_id", clip.ID), zap.String("path", metaPath), zap.Error(err))
		}
		return
	}
	if log != nil {
		log.Debug("clip metadata file written", zap.String("clip_id", clip.ID), zap.String("path", metaPath))
	}
}

// ── Inline helpers for WriteClipMetadataFile ──────────────────────────

func ymDescriptionCanonical(ym *youtubeports.DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.Description != "" {
		return tagutil.CompactYouTubeDescription(ym.Description)
	}
	desc := clip.GetMetadataString("youtube_description")
	if desc != "" {
		return tagutil.CompactYouTubeDescription(desc)
	}
	return ""
}

func ymTagsCanonical(ym *youtubeports.DownloaderMetadata, clip *asset.Asset) []string {
	if ym != nil && len(ym.Tags) > 0 {
		return tagutil.NormalizeClipTagList(ym.Tags)
	}
	tagsJSON := clip.GetMetadataString("youtube_tags")
	if tagsJSON != "" && tagsJSON != "[]" {
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err == nil {
			return tagutil.NormalizeClipTagList(tags)
		}
	}
	if len(clip.Tags) > 0 {
		return tagutil.NormalizeClipTagList(clip.Tags)
	}
	return nil
}

func ymCategoriesCanonical(ym *youtubeports.DownloaderMetadata, clip *asset.Asset) []string {
	if ym != nil && len(ym.Categories) > 0 {
		return ym.Categories
	}
	catsJSON := clip.GetMetadataString("youtube_categories")
	if catsJSON != "" && catsJSON != "[]" {
		var cats []string
		json.Unmarshal([]byte(catsJSON), &cats)
		return cats
	}
	return nil
}

func ymViewCountCanonical(ym *youtubeports.DownloaderMetadata, clip *asset.Asset) int64 {
	if ym != nil {
		return ym.ViewCount
	}
	countStr := clip.GetMetadataString("youtube_view_count")
	if countStr != "" {
		if n, err := strconv.ParseInt(countStr, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func ymUploadDateCanonical(ym *youtubeports.DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.UploadDate != "" {
		return ym.UploadDate
	}
	return clip.GetMetadataString("youtube_upload_date")
}

func ymThumbnailURLCanonical(ym *youtubeports.DownloaderMetadata, clip *asset.Asset) string {
	if ym != nil && ym.ThumbnailURL != "" {
		return ym.ThumbnailURL
	}
	return clip.GetMetadataString("youtube_thumbnail")
}
