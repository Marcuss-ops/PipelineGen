// Package metadata provides YouTube clip metadata enrichment — the core
// logic for enriching YouTube clips with semantic metadata (title, description,
// tags, language, categories, chapters, quality scoring) and persisting it.
// Extracted from the root youtube package during PR5 Phase 1 (June 2026).
//
// Design: MetadataDeps accepts max 8 fields. The service owns enrichment,
// metadata file writing, fallback search text, and Ollama-based metadata
// generation.
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// MetadataDeps holds dependencies for the metadata enrichment service (max 8 fields).
// PR5 Phase 1 target: ≤8 fields — currently 6.
type MetadataDeps struct {
	Clips       ports.ClipStorePort
	MetaFetcher ports.VideoMetadataFetcherPort
	Ollama      ports.OllamaClientPort
	AssetRepo   asset.Repository
	Cfg         *config.Config
	Log         *zap.Logger
}

// Service performs YouTube clip metadata enrichment.
type Service struct {
	clips       ports.ClipStorePort
	metaFetcher ports.VideoMetadataFetcherPort
	ollama      ports.OllamaClientPort
	assetRepo   asset.Repository
	cfg         *config.Config
	log         *zap.Logger
}

// NewService is the canonical constructor.
func NewService(deps MetadataDeps) *Service {
	return &Service{
		clips:       deps.Clips,
		metaFetcher: deps.MetaFetcher,
		ollama:      deps.Ollama,
		assetRepo:   deps.AssetRepo,
		cfg:         deps.Cfg,
		log:         deps.Log,
	}
}

// ── Public API ────────────────────────────────────────────────────────────

// EnrichClip updates a clip's metadata with YouTube video information
// (title, description, tags, language, categories, chapters) to enable rich
// semantic search across multiple languages and conceptual queries.
//
// RESILIENCE: If meta is nil (e.g., yt-dlp failed during extraction), this
// function falls back to fetching YouTube metadata directly via the
// metaFetcher port.
func (s *Service) EnrichClip(ctx context.Context, clipID string, meta *ports.DownloaderMetadata, force bool) {
	if s.clips == nil {
		return
	}

	existing, err := s.clips.GetClip(ctx, clipID)
	if err != nil || existing == nil {
		s.log.Warn("cannot enrich YouTube clip: not found in DB", zap.String("clip_id", clipID))
		return
	}

	if !force && existing.GetMetadataString("youtube_title") != "" && existing.SearchText != "" {
		return
	}

	var ym *ports.DownloaderMetadata
	if meta != nil {
		ym = meta
		s.log.Debug("enriching with pre-fetched metadata", zap.String("clip_id", clipID))
	} else if s.metaFetcher != nil {
		videoURL := buildVideoURL(clipID, existing)
		if videoURL != "" {
			s.log.Info("fetching YouTube metadata directly for enrichment",
				zap.String("clip_id", clipID), zap.String("url", videoURL))
			fetchedMeta, err := s.metaFetcher.GetVideoMetadata(ctx, videoURL)
			if err == nil && fetchedMeta != nil {
				ym = fetchedMeta
			} else {
				s.log.Warn("failed to fetch YouTube metadata for enrichment",
					zap.String("clip_id", clipID), zap.Error(err))
			}
		}
	}

	if ym == nil {
		s.log.Debug("no YouTube metadata available, building fallback search_text",
			zap.String("clip_id", clipID))
		s.BuildFallbackSearchText(existing)
		ytLang := existing.GetMetadataString("youtube_language")
		if ytLang != "" {
			if existing.Metadata == nil {
				existing.Metadata = make(map[string]any)
			}
			existing.Metadata["language"] = ytLang
		}
		if err := s.assetRepo.Upsert(ctx, existing); err != nil {
			s.log.Warn("failed to save fallback search_text", zap.String("clip_id", clipID), zap.Error(err))
		}
		return
	}

	cleanedDescription := tagutil.CleanYouTubeDescription(ym.Description)

	// Read transcript
	var clipTranscript string
	transcriptPath := strings.TrimSuffix(existing.LocalPath(), filepath.Ext(existing.LocalPath())) + ".txt"
	if transcriptBytes, err := os.ReadFile(transcriptPath); err == nil && len(transcriptBytes) > 0 {
		clipTranscript = strings.TrimSpace(string(transcriptBytes))
		if len(clipTranscript) > 5000 {
			clipTranscript = clipTranscript[:5000]
		}
	}
	cleanedTranscript := tagutil.CleanClipTranscript(clipTranscript)

	// Try Ollama-based enrichment
	hasUserSummary := existing.GetMetadataString("clip_summary") != ""
	hasUserTopics := len(metadataStringSlice(existing.Metadata, "topics")) > 0

	var clipMetadata *types.ClipRichMetadata
	if hasUserSummary && hasUserTopics {
		s.log.Info("using user-provided custom metadata, skipping Ollama enrichment", zap.String("clip_id", clipID))
		clipMetadata = &types.ClipRichMetadata{
			ClipSummary:      existing.GetMetadataString("clip_summary"),
			Topics:           metadataStringSlice(existing.Metadata, "topics"),
			Speakers:         metadataStringSlice(existing.Metadata, "speakers"),
			MentionedPeople:  metadataStringSlice(existing.Metadata, "mentioned_people"),
			Hook:             existing.GetMetadataString("hook"),
			QualityScore:     metadataFloat64(existing.Metadata, "quality_score"),
			CleanTitle:       existing.GetMetadataString("clean_title"),
			ShortTitle:       existing.GetMetadataString("short_title"),
			SearchVisibility: existing.GetMetadataString("search_visibility"),
			CleanTranscript:  cleanedTranscript,
		}
		if clipMetadata.CleanTitle == "" {
			clipMetadata.CleanTitle = existing.Name
		}
	} else {
		clipMetadata = s.GenerateClipMetadata(ctx, ym.Title, cleanedTranscript, cleanedDescription)
	}

	// Fill gaps with fallback semantic fields
	if clipMetadata != nil {
		fallbackTopics, fallbackSpeakers, fallbackMentionedPeople, fallbackSourceTags, fallbackClipTags, fallbackSearchKeywords, _, fallbackHook :=
			tagutil.DeriveFallbackSemanticFields(ym.Title, cleanedTranscript, cleanedDescription, clipMetadata.CleanTitle)
		if len(clipMetadata.Topics) == 0 {
			clipMetadata.Topics = fallbackTopics
		}
		if len(clipMetadata.Speakers) == 0 {
			clipMetadata.Speakers = fallbackSpeakers
		}
		if len(clipMetadata.MentionedPeople) == 0 {
			clipMetadata.MentionedPeople = fallbackMentionedPeople
		}
		if len(clipMetadata.People) == 0 && len(clipMetadata.MentionedPeople) > 0 {
			clipMetadata.People = append([]string(nil), clipMetadata.MentionedPeople...)
		}
		if len(clipMetadata.SourceTags) == 0 {
			clipMetadata.SourceTags = fallbackSourceTags
		}
		if len(clipMetadata.ClipTags) == 0 {
			clipMetadata.ClipTags = fallbackClipTags
		}
		if len(clipMetadata.SearchKeywords) == 0 {
			clipMetadata.SearchKeywords = fallbackSearchKeywords
		}
		if clipMetadata.Hook == "" {
			clipMetadata.Hook = fallbackHook
		}
		if clipMetadata.CleanTranscript == "" {
			clipMetadata.CleanTranscript = cleanedTranscript
		}
		if clipMetadata.EmbeddingText == "" {
			clipMetadata.EmbeddingText = tagutil.BuildEmbeddingText(
				clipMetadata.CleanTitle, clipMetadata.ClipSummary, clipMetadata.Hook,
				clipMetadata.Topics, clipMetadata.Speakers, clipMetadata.MentionedPeople,
				clipMetadata.SourceTags, clipMetadata.ClipTags, clipMetadata.SearchKeywords,
				cleanedTranscript,
			)
		}
	}

	embeddingText := tagutil.BuildEmbeddingText(ym.Title, "", "", nil, nil, nil, nil, nil, nil, cleanedTranscript)
	if clipMetadata != nil {
		if clipMetadata.EmbeddingText != "" {
			embeddingText = clipMetadata.EmbeddingText
		} else {
			embeddingText = tagutil.BuildEmbeddingText(
				clipMetadata.CleanTitle, clipMetadata.ClipSummary, clipMetadata.Hook,
				clipMetadata.Topics, clipMetadata.Speakers, clipMetadata.MentionedPeople,
				clipMetadata.SourceTags, clipMetadata.ClipTags, clipMetadata.SearchKeywords,
				cleanedTranscript,
			)
		}
	}

	existing.SearchText = embeddingText
	existing.Tags = tagutil.MergeYouTubeClipTags(existing.Tags, ym.Tags, clipMetadata)

	if clipMetadata != nil {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["clip_summary"] = clipMetadata.ClipSummary
		existing.Metadata["topics"] = clipMetadata.Topics
		existing.Metadata["speakers"] = clipMetadata.Speakers
		existing.Metadata["mentioned_people"] = clipMetadata.MentionedPeople
		existing.Metadata["people"] = tagutil.MergeTagLists(clipMetadata.Speakers, clipMetadata.MentionedPeople, clipMetadata.People)
		existing.Metadata["source_tags"] = clipMetadata.SourceTags
		existing.Metadata["clip_tags"] = clipMetadata.ClipTags
		existing.Metadata["search_keywords"] = clipMetadata.SearchKeywords
		existing.Metadata["hook"] = clipMetadata.Hook
		existing.Metadata["clean_title"] = clipMetadata.CleanTitle
		existing.Metadata["short_title"] = clipMetadata.ShortTitle
		existing.Metadata["embedding_text"] = clipMetadata.EmbeddingText
		existing.Metadata["semantic_tags"] = clipMetadata.Tags
		existing.Metadata["quality_score"] = clipMetadata.QualityScore
	}
	if cleanedTranscript != "" {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["clean_transcript"] = cleanedTranscript
	}
	if ym.Language != "" {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["language"] = ym.Language
	}

	existing.SetMetadataString("youtube_title", ym.Title)
	existing.SetMetadataString("youtube_description", ym.Description)
	existing.SetMetadataString("youtube_language", ym.Language)
	existing.SetMetadataString("youtube_uploader", ym.Uploader)
	existing.SetMetadataString("youtube_upload_date", ym.UploadDate)
	existing.SetMetadataString("youtube_view_count", fmt.Sprintf("%d", ym.ViewCount))
	existing.SetMetadataString("youtube_duration", fmt.Sprintf("%.1f", ym.Duration))
	existing.SetMetadataString("youtube_video_id", ym.ID)
	existing.SetMetadataString("youtube_url", fmt.Sprintf("https://www.youtube.com/watch?v=%s", ym.ID))
	if len(ym.Categories) > 0 {
		catsJSON, _ := json.Marshal(ym.Categories)
		existing.SetMetadataString("youtube_categories", string(catsJSON))
	}
	if len(ym.Tags) > 0 {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["youtube_tags"] = ym.Tags
	}

	if clipTranscript != "" && isSponsorSegment(clipTranscript) {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["is_sponsor_segment"] = true
		existing.Metadata["sponsor_confidence"] = "high"
	} else if existing.Metadata != nil {
		delete(existing.Metadata, "is_sponsor_segment")
		delete(existing.Metadata, "sponsor_confidence")
	}

	qualityScore := calculateQualityScore(cleanedTranscript, ym.Title, ym.Description, ym.Tags, ym.Duration, clipMetadata)
	if existing.Metadata == nil {
		existing.Metadata = make(map[string]any)
	}
	existing.Metadata["quality_score"] = qualityScore
	existing.Metadata["quality_tier"] = getQualityTier(qualityScore)
	existing.Metadata["search_visibility"] = tagutil.DeriveSearchVisibility(qualityScore)

	if len(ym.Chapters) > 0 {
		chaptersJSON, _ := json.Marshal(ym.Chapters)
		existing.SetMetadataString("youtube_chapters", string(chaptersJSON))
	}
	if ym.ThumbnailURL != "" {
		existing.SetMetadataString("youtube_thumbnail", ym.ThumbnailURL)
	}

	if err := s.assetRepo.Upsert(ctx, existing); err != nil {
		s.log.Warn("failed to enrich YouTube clip with metadata", zap.String("clip_id", clipID), zap.Error(err))
		return
	}

	s.WriteClipMetadataFile(ctx, existing, ym)

	s.log.Info("YouTube clip enriched with metadata",
		zap.String("clip_id", clipID),
		zap.String("title", ym.Title),
		zap.String("language", ym.Language),
		zap.Int("tags", len(ym.Tags)),
		zap.Int("categories", len(ym.Categories)),
		zap.Int("semantic_text_len", len(embeddingText)),
	)
}

// WriteClipMetadataFile writes and uploads a per-clip metadata file alongside the clip.
func (s *Service) WriteClipMetadataFile(ctx context.Context, clip *asset.Asset, ym *ports.DownloaderMetadata) {
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
		tags = ymTags(ym, clip)
	}
	categories := ymCategories(ym, clip)
	viewCount := ymViewCount(ym, clip)
	uploadDate := ymUploadDate(ym, clip)
	thumbnailURL := ymThumbnailURL(ym, clip)

	transcriptPath := strings.TrimSuffix(clip.LocalPath(), filepath.Ext(clip.LocalPath())) + ".txt"
	transcript := ""
	if transcriptBytes, err := os.ReadFile(transcriptPath); err == nil && len(transcriptBytes) > 0 {
		transcript = strings.TrimSpace(string(transcriptBytes))
	}
	cleanTranscriptText := tagutil.CleanClipTranscript(transcript)

	description := tagutil.CompactYouTubeDescription(ymDescription(ym, clip))
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
	qualityScore := metadataFloat64(clip.Metadata, "quality_score")
	searchVisibility := clip.GetMetadataString("search_visibility")
	if searchVisibility == "" {
		searchVisibility = tagutil.DeriveSearchVisibility(qualityScore)
	}
	if qualityScore >= 0.80 {
		searchVisibility = "high"
	}

	meta := types.ClipMetadataFile{
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

	metaFilename := "metadata_" + clip.ID + ".json"
	metaPath := filepath.Join(filepath.Dir(clip.LocalPath()), metaFilename)
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		s.log.Warn("failed to write clip metadata file", zap.String("clip_id", clip.ID), zap.String("path", metaPath), zap.Error(err))
		return
	}
	s.log.Debug("clip metadata file written", zap.String("clip_id", clip.ID), zap.String("path", metaPath))
}

// BuildFallbackSearchText builds a minimal search_text from existing clip metadata.
// This ensures search_text is NEVER empty for YouTube clips, even when yt-dlp is unavailable.
func (s *Service) BuildFallbackSearchText(clip *asset.Asset) {
	var parts []string
	if ytTitle := clip.GetMetadataString("youtube_title"); ytTitle != "" {
		parts = append(parts, "Title: "+ytTitle)
	}
	if clip.Name != "" {
		parts = append(parts, "Segment: "+clip.Name)
	}
	if ytDesc := clip.GetMetadataString("youtube_description"); ytDesc != "" {
		cleanedDesc := tagutil.CleanYouTubeDescription(ytDesc)
		if cleanedDesc != "" {
			if phrases := tagutil.ExtractKeyPhrases(cleanedDesc, 5); len(phrases) > 0 {
				parts = append(parts, "Description keywords: "+strings.Join(phrases, ", "))
			}
		}
	}
	if ytTags := clip.GetMetadataString("youtube_tags"); ytTags != "" && ytTags != "[]" {
		parts = append(parts, "Tags: "+ytTags)
	}
	if len(clip.Tags) > 0 {
		parts = append(parts, "Tags: "+strings.Join(clip.Tags, ", "))
	}
	if ytUploader := clip.GetMetadataString("youtube_uploader"); ytUploader != "" {
		parts = append(parts, "Uploader: "+ytUploader)
	}
	if ytLang := clip.GetMetadataString("youtube_language"); ytLang != "" {
		parts = append(parts, "Language: "+ytLang)
	}
	if len(parts) > 0 {
		clip.SearchText = strings.Join(parts, "\n")
	}
}

func (s *Service) GenerateClipMetadata(ctx context.Context, title, transcript, description string) *types.ClipRichMetadata {
	if s.ollama == nil {
		return nil
	}
	model := s.metadataModel()

	if len(transcript) > 3000 {
		transcript = transcript[:3000]
	}

	prompt := fmt.Sprintf(`You are an assistant that generates rich metadata for a YouTube clip.
Analyze only the clip transcript below. Do not invent events from the description.
Use the title only as lightweight context for names/entities.

Title: %s
Transcript: %s

Return only JSON with these fields:
{
  "clip_summary": "2-3 sentence summary of the actual clip",
  "topics": ["concept 1", "concept 2"],
  "speakers": ["primary speaker", "host"],
  "mentioned_people": ["person mentioned", "another person"],
  "source_tags": ["show/channel tags tied to source"],
  "clip_tags": ["clip-specific concepts"],
  "search_keywords": ["short keyword phrases from the clip"],
  "hook": "the strongest spoken line from the clip",
  "clean_title": "specific clip title, not the whole video",
  "short_title": "short searchable title",
  "quality_score": 0.0
}

Rules:
- clip_summary must be faithful to the transcript only
- topics must be concepts or themes, not filler words
- speakers are the people actually speaking in the clip when inferable; clearly distinguish the main host/presenter from any guests or interviewees
- mentioned_people are people named in the clip, distinct from speakers
- source_tags should describe the show/channel/source, not the clip moment
- clip_tags should describe the specific moment or topic of the clip
- search_keywords should be short phrases actually useful for search
- hook should be the strongest line actually spoken in the clip
- clean_title should describe the clip-specific moment, not the whole video
- short_title should be concise and searchable
- quality_score must reflect narrative value, clarity, hook strength, completeness, and usefulness for search
- use a score from 0.0 to 1.0; strong clips should be 0.7+ and weak or incomplete clips should be below 0.5
- if the clip is short, incomplete, or low-signal, reduce specificity and quality
- Return ONLY the JSON object, no explanation`, title, transcript)

	s.log.Info("calling Ollama for clip metadata generation",
		zap.String("model", model), zap.Int("transcript_chars", len(transcript)))

	response, err := s.ollama.SimpleGenerate(ctx, model, prompt, 60*time.Second, nil)
	if err != nil {
		s.log.Warn("Ollama call failed for clip metadata", zap.Error(err))
		return nil
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return nil
	}

	start := strings.Index(response, "{")
	end := strings.LastIndex(response, "}")
	if start == -1 || end == -1 || end <= start {
		s.log.Warn("invalid JSON in ollama response for clip metadata")
		return nil
	}
	jsonStr := response[start : end+1]

	var result types.ClipRichMetadata
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		s.log.Warn("failed to parse ollama JSON response for clip metadata", zap.Error(err))
		return tagutil.FallbackClipRichMetadata(title, transcript, description)
	}

	return tagutil.NormalizeClipRichMetadata(&result, title, transcript, description)
}

func (s *Service) metadataModel() string {
	if s.cfg == nil {
		return "gemma4:e2b"
	}
	model := strings.TrimSpace(s.cfg.External.OllamaMetadataModel)
	if model == "" {
		model = strings.TrimSpace(s.cfg.External.OllamaModel)
	}
	if model == "" {
		model = "gemma4:e2b"
	}
	return model
}
