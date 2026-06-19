package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"go.uber.org/zap"
)

// writeClipMetadataFile writes and uploads a per-clip metadata file alongside the clip,
// then uploads the clip MP4 to the same Drive folder.
// All clips share a single Drive folder (category or explicit) — no per-clip subfolders.
//
// Fix B: The description field contains the CLIP TRANSCRIPT (from .txt file), not
// the full YouTube description — making it useful for semantic search.
// Fix C: Each clip gets a UNIQUE metadata filename (metadata_<clip_id>.json) so
// that multiple clips in the same Drive folder don't overwrite each other.
func (s *Service) writeClipMetadataFile(ctx context.Context, clip *assets.Asset, ym *downloader.YouTubeMetadata) {
	if clip == nil || clip.LocalPath() == "" {
		return
	}

	// Parse clip ID to extract start/end timestamps (format: yt_videoID_start_end)
	startSec, endSec := 0, 0
	parts := strings.Split(clip.ID, "_")
	if len(parts) >= 4 && parts[0] == "yt" {
		if s, err := strconv.Atoi(parts[len(parts)-2]); err == nil {
			startSec = s
		}
		if e, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			endSec = e
		}
	}

	// Use clip duration if available
	durationSec := endSec - startSec
	if durationSec <= 0 {
		durationSec = int(clip.Duration.Seconds())
	}

	youtubeURL := clip.GetMetadataString("youtube_url")
	if youtubeURL == "" && ym != nil && ym.ID != "" {
		youtubeURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", ym.ID)
	}

	// Gather metadata from the clip DB first. The DB already stores the enriched
	// clip-specific tags after semantic processing; we keep the YouTube tags only
	// as fallback.
	tags := normalizeClipTagList(clip.Tags)
	if len(tags) == 0 {
		tags = ymTags(ym, clip)
	}
	categories := ymCategories(ym, clip)
	viewCount := ymViewCount(ym, clip)
	uploadDate := ymUploadDate(ym, clip)
	thumbnailURL := ymThumbnailURL(ym, clip)

	// Read clip transcript from the .txt file alongside the clip (clip-specific spoken content)
	transcriptPath := strings.TrimSuffix(clip.LocalPath(), filepath.Ext(clip.LocalPath())) + ".txt"
	transcript := ""
	if transcriptBytes, err := os.ReadFile(transcriptPath); err == nil && len(transcriptBytes) > 0 {
		transcript = strings.TrimSpace(string(transcriptBytes))
	}
	cleanTranscriptText := cleanClipTranscript(transcript)

	// Description = compact YouTube description (general video info without
	// sponsor/link boilerplate).
	// Transcript = clip-specific spoken content (what's said in this clip window)
	description := compactYouTubeDescription(ymDescription(ym, clip))
	rawTitle := clip.Name
	cleanTitle := clip.GetMetadataString("clean_title")
	if cleanTitle == "" {
		cleanTitle = clip.Name
	}
	shortTitle := clip.GetMetadataString("short_title")
	clipSummary := clip.GetMetadataString("clip_summary")
	hook := clip.GetMetadataString("hook")
	topics := metadataStringSlice(clip.Metadata, "topics")
	speakers := metadataStringSlice(clip.Metadata, "speakers")
	mentionedPeople := metadataStringSlice(clip.Metadata, "mentioned_people")
	people := metadataStringSlice(clip.Metadata, "people")
	sourceTags := metadataStringSlice(clip.Metadata, "source_tags")
	clipTags := metadataStringSlice(clip.Metadata, "clip_tags")
	searchKeywords := metadataStringSlice(clip.Metadata, "search_keywords")
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
	fallbackTopics, fallbackSpeakers, fallbackMentionedPeople, fallbackSourceTags, fallbackClipTags, fallbackSearchKeywords, _, fallbackHook := deriveFallbackSemanticFields(videoTitle, storedCleanTranscript, description, cleanTitle)
	if len(topics) == 0 {
		topics = fallbackTopics
	}
	if len(speakers) == 0 {
		speakers = fallbackSpeakers
	}
	if len(mentionedPeople) == 0 {
		mentionedPeople = fallbackMentionedPeople
	}
	people = mergeTagLists(speakers, mentionedPeople, people)
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
		embeddingText = buildEmbeddingText(cleanTitle, clipSummary, hook, topics, speakers, mentionedPeople, sourceTags, clipTags, searchKeywords, storedCleanTranscript)
	}
	qualityScore := metadataFloat64(clip.Metadata, "quality_score")
	searchVisibility := clip.GetMetadataString("search_visibility")
	if searchVisibility == "" {
		searchVisibility = deriveSearchVisibility(qualityScore, nil, nil)
	}
	if qualityScore >= 0.80 {
		searchVisibility = "high"
	} else if searchVisibility == "" {
		searchVisibility = deriveSearchVisibility(qualityScore, nil, nil)
	}

	meta := ClipMetadataFile{
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
		IsDuplicate:       metadataBool(clip.Metadata, "is_duplicate"),
		IsBestVersion:     metadataBool(clip.Metadata, "is_best_version"),
		DuplicateReason:   clip.GetMetadataString("duplicate_reason"),
		DuplicateScore:    metadataFloat64(clip.Metadata, "duplicate_score"),
		TopicClusterID:    clip.GetMetadataString("topic_cluster_id"),
		TopicClusterLabel: clip.GetMetadataString("topic_cluster_label"),
		TopicClusterSize:  metadataInt(clip.Metadata, "topic_cluster_size"),
		TopicClusterRank:  metadataInt(clip.Metadata, "topic_cluster_rank"),
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
		s.log.Warn("failed to marshal clip metadata", zap.String("clip_id", clip.ID), zap.Error(err))
		return
	}

	// Fix C: Unique metadata filename per clip
	metaFilename := "metadata_" + clip.ID + ".json"
	metaPath := filepath.Join(filepath.Dir(clip.LocalPath()), metaFilename)
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		s.log.Warn("failed to write clip metadata file",
			zap.String("clip_id", clip.ID),
			zap.String("path", metaPath),
			zap.Error(err))
		return
	}

	s.log.Debug("clip metadata file written", zap.String("clip_id", clip.ID), zap.String("path", metaPath))

	if transcript != "" {
		s.log.Debug("clip transcript available",
			zap.String("clip_id", clip.ID),
			zap.Int("transcript_chars", len(transcript)))
	}
}
