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

	"go.uber.org/zap"

	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// MetadataDeps holds dependencies for the metadata enrichment service (max 8 fields).
// PR5 Phase 1 target: ≤8 fields — currently 6.
type MetadataDeps struct {
	Clips       ports.ClipStorePort
	MetaFetcher ports.VideoMetadataFetcherPort
	Ollama      ports.OllamaClientPort
	AssetRepo   asset.Repository
	Cfg         types.RuntimeConfig
	Log         *zap.Logger
}

// Service performs YouTube clip metadata enrichment.
type Service struct {
	clips       ports.ClipStorePort
	metaFetcher ports.VideoMetadataFetcherPort
	ollama      ports.OllamaClientPort
	assetRepo   asset.Repository
	cfg         types.RuntimeConfig
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
