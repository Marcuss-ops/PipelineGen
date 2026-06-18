package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/media/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/downloader"

	"go.uber.org/zap"
)

// enrichYouTubeClipWithMetadata updates a clip's metadata with YouTube video information
// (title, description, tags, language, categories, chapters) to enable rich semantic
// search across multiple languages and conceptual queries.
//
// The search_text field is built from structured semantic metadata only:
// Title > Summary > Hook > Topics > Speakers > Tags > Keywords.
// Transcript is intentionally NOT included in search_text to prevent false
// positives from literal phrase matches between generated scripts and clip
// transcript content. Transcript is indexed separately in the Qdrant
// "transcript" named vector for hybrid search.
//
// RESILIENCE: If the provided meta parameter is nil (e.g., yt-dlp failed during extraction
// or the clip was from a file cache hit), this function will fall back to fetching
// YouTube metadata directly from the video URL. This ensures clips always get enriched
// even when yt-dlp is temporarily unavailable during the download phase.
func (s *Service) enrichYouTubeClipWithMetadata(ctx context.Context, clipID string, meta *videomuscles.YouTubeCutResult, force bool) {
	if s.clipsRepo == nil {
		return
	}

	// Get the existing clip from DB first
	existing, err := s.clipsRepo.GetClip(ctx, clipID)
	if err != nil || existing == nil {
		s.log.Warn("cannot enrich YouTube clip: not found in DB", zap.String("clip_id", clipID))
		return
	}

	// If already enriched AND has search_text, skip (preserves existing enrichment)
	// Re-enrich if search_text is empty, even if youtube_title exists.
	// This handles the case where yt-dlp failed during initial extraction and
	// only some metadata was saved before the enrichment resilience fix.
	//
	// When force=true, bypasses this check to rebuild search_text (e.g. after
	// changing the field order to put Transcript before Description).
	if !force && existing.GetMetadataString("youtube_title") != "" && existing.SearchText != "" {
		return
	}

	var ym *downloader.YouTubeMetadata

	// Try to get metadata from the provided result first (fast path)
	if meta != nil && meta.Metadata != nil {
		ym = meta.Metadata
		s.log.Debug("enriching with pre-fetched metadata", zap.String("clip_id", clipID))
	} else {
		// Fallback: fetch YouTube metadata directly via yt-dlp
		// This handles the case where yt-dlp was rate-limited or failed during extraction
		videoURL := existing.GetMetadataString("youtube_url")
		if videoURL == "" {
			videoID := extractYouTubeVideoID(clipID, existing)
			if videoID != "" {
				videoURL = fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
			}
		}
		if videoURL != "" {
			s.log.Info("fetching YouTube metadata directly for enrichment",
				zap.String("clip_id", clipID),
				zap.String("url", videoURL))
			ytDlp := downloader.NewYTDLP(s.cfg)
			fetchedMeta, err := ytDlp.GetVideoMetadata(ctx, videoURL)
			if err == nil && fetchedMeta != nil {
				ym = fetchedMeta
				s.log.Debug("fetched YouTube metadata for enrichment",
					zap.String("clip_id", clipID),
					zap.String("title", ym.Title))
			} else {
				s.log.Warn("failed to fetch YouTube metadata for enrichment",
					zap.String("clip_id", clipID),
					zap.Error(err))
			}
		}
	}

	// If no YouTube metadata available, build fallback search_text from existing clip metadata
	// This ensures search_text is NEVER empty for YouTube clips, even when yt-dlp fails.
	if ym == nil {
		s.log.Debug("no YouTube metadata available, building fallback search_text",
			zap.String("clip_id", clipID))
		s.buildFallbackSearchText(existing)
		// Propagate youtube_language to language field if present
		ytLang := existing.GetMetadataString("youtube_language")
		if ytLang != "" {
			if existing.Metadata == nil {
				existing.Metadata = make(map[string]any)
			}
			existing.Metadata["language"] = ytLang
		}
		// Save fallback to DB. dispatchOrIndex routes through the canonical
		// outbox_events dispatcher when wired (atomic UpsertClip +
		// IndexClip and outbox enqueue), falling back to the legacy
		// UpsertClip when a nil dispatcher leaves the ingestion crash-exposed
		// between write and re-index.
		if err := s.dispatchOrIndex(ctx, existing, existing.FileHash()); err != nil {
			s.log.Warn("failed to save fallback search_text",
				zap.String("clip_id", clipID),
				zap.Error(err))
		}
		return
	}

	// Read the clip transcript so we can clean it and feed it into the embedding.
	var clipTranscript string
	transcriptPath := strings.TrimSuffix(existing.LocalPath(), filepath.Ext(existing.LocalPath())) + ".txt"
	if transcriptBytes, err := os.ReadFile(transcriptPath); err == nil && len(transcriptBytes) > 0 {
		clipTranscript = strings.TrimSpace(string(transcriptBytes))
		if clipTranscript != "" {
			// Truncate for sanity (embedding models typically handle ~512 tokens)
			if len(clipTranscript) > 5000 {
				clipTranscript = clipTranscript[:5000]
			}
			s.log.Debug("added clip transcript to search_text",
				zap.String("clip_id", clipID),
				zap.Int("transcript_chars", len(clipTranscript)))
		}
	}

	var clipMetadata *clipRichMetadata

	// Check if we already have custom metadata provided by the user in the request
	// (which is already persisted in existing.Metadata via lifecycle).
	hasUserSummary := existing.GetMetadataString("clip_summary") != ""
	hasUserTopics := len(metadataStringSlice(existing.Metadata, "topics")) > 0

	cleanedDescription := cleanYouTubeDescription(ym.Description)
	cleanedTranscript := cleanClipTranscript(clipTranscript)

	if hasUserSummary && hasUserTopics {
		s.log.Info("using user-provided custom metadata, skipping Ollama enrichment", zap.String("clip_id", clipID))
		clipMetadata = &clipRichMetadata{
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
		// Generate rich metadata (summary, topics, people, hook, clean_title, short_title)
		clipMetadata = s.generateClipMetadata(ctx, ym.Title, cleanedTranscript, cleanedDescription)
	}
	if clipMetadata != nil {
		fallbackTopics, fallbackSpeakers, fallbackMentionedPeople, fallbackSourceTags, fallbackClipTags, fallbackSearchKeywords, _, fallbackHook := deriveFallbackSemanticFields(ym.Title, cleanedTranscript, cleanedDescription, clipMetadata.CleanTitle)
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
			clipMetadata.EmbeddingText = buildEmbeddingText(
				clipMetadata.CleanTitle,
				clipMetadata.ClipSummary,
				clipMetadata.Hook,
				clipMetadata.Topics,
				clipMetadata.Speakers,
				clipMetadata.MentionedPeople,
				clipMetadata.SourceTags,
				clipMetadata.ClipTags,
				clipMetadata.SearchKeywords,
				cleanedTranscript,
			)
		}
	}
	embeddingText := buildEmbeddingText(ym.Title, "", "", nil, nil, nil, nil, nil, nil, cleanedTranscript)
	if clipMetadata != nil {
		if clipMetadata.EmbeddingText != "" {
			embeddingText = clipMetadata.EmbeddingText
		} else {
			embeddingText = buildEmbeddingText(
				clipMetadata.CleanTitle,
				clipMetadata.ClipSummary,
				clipMetadata.Hook,
				clipMetadata.Topics,
				clipMetadata.Speakers,
				clipMetadata.MentionedPeople,
				clipMetadata.SourceTags,
				clipMetadata.ClipTags,
				clipMetadata.SearchKeywords,
				cleanedTranscript,
			)
		}
		s.log.Debug("generated clip metadata",
			zap.String("clip_id", clipID),
			zap.String("clean_title", clipMetadata.CleanTitle),
			zap.Int("topics", len(clipMetadata.Topics)),
			zap.Int("speakers", len(clipMetadata.Speakers)),
			zap.Int("mentioned_people", len(clipMetadata.MentionedPeople)),
			zap.Int("source_tags", len(clipMetadata.SourceTags)),
			zap.Int("clip_tags", len(clipMetadata.ClipTags)),
			zap.Int("search_keywords", len(clipMetadata.SearchKeywords)))
	}

	// Set enriched fields
	existing.SearchText = embeddingText
	// NOTE: existing.Name is intentionally NOT overwritten with ym.Title.
	// The clip/segment name (e.g., "padre spelling") is more descriptive for
	// the specific clip than the generic video title (e.g., "30 Minutes of Kevin Hart.").
	// youtube_title is stored in metadata for full-text search context.

	// Merge existing segment tags with clip-specific tags, then filter out generic boilerplate.
	existing.Tags = mergeYouTubeClipTags(existing.Tags, ym.Tags, clipMetadata)

	// Store rich metadata in metadata_json
	if clipMetadata != nil {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["clip_summary"] = clipMetadata.ClipSummary
		existing.Metadata["topics"] = clipMetadata.Topics
		existing.Metadata["speakers"] = clipMetadata.Speakers
		existing.Metadata["mentioned_people"] = clipMetadata.MentionedPeople
		existing.Metadata["people"] = mergeTagLists(clipMetadata.Speakers, clipMetadata.MentionedPeople, clipMetadata.People)
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

	// Store language as both a dedicated field (for Qdrant filtering) and in metadata
	if ym.Language != "" {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["language"] = ym.Language
	}

	// Store full YouTube metadata in metadata_json for persistence and future re-indexing
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
		// Store as []string directly so json.Marshal produces a JSON array,
		// not a string-encoded-JSON-array (which breaks Qdrant filters).
		// buildClipMetadata already does this correctly: metadataMap["youtube_tags"] = youtubeMeta.Tags
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["youtube_tags"] = ym.Tags
	}

	// Detect sponsor segments and flag in metadata
	// Check transcript and description for sponsor content
	if clipTranscript != "" && isSponsorSegment(clipTranscript) {
		if existing.Metadata == nil {
			existing.Metadata = make(map[string]any)
		}
		existing.Metadata["is_sponsor_segment"] = true
		existing.Metadata["sponsor_confidence"] = "high"
		s.log.Debug("detected sponsor segment in transcript",
			zap.String("clip_id", clipID))
	} else if existing.Metadata != nil {
		delete(existing.Metadata, "is_sponsor_segment")
		delete(existing.Metadata, "sponsor_confidence")
	}

	// Calculate quality score based on content richness
	qualityScore := calculateQualityScore(cleanedTranscript, ym.Title, ym.Description, ym.Tags, ym.Duration, clipMetadata)
	if existing.Metadata == nil {
		existing.Metadata = make(map[string]any)
	}
	existing.Metadata["quality_score"] = qualityScore
	existing.Metadata["quality_tier"] = getQualityTier(qualityScore)
	existing.Metadata["search_visibility"] = deriveSearchVisibility(qualityScore, existing.Metadata, existing.Tags)
	s.log.Debug("calculated quality score for clip",
		zap.String("clip_id", clipID),
		zap.Float64("score", qualityScore))

	if len(ym.Chapters) > 0 {
		chaptersJSON, _ := json.Marshal(ym.Chapters)
		existing.SetMetadataString("youtube_chapters", string(chaptersJSON))
	}
	if ym.ThumbnailURL != "" {
		existing.SetMetadataString("youtube_thumbnail", ym.ThumbnailURL)
	}

	// Save to DB. dispatchOrIndex writes the clip atomically together with
	// the outbox_events enqueue (and downstream Qdrant upsert) when
	// the dispatcher is wired, so the post-enrichment state is consistently
	// searchable. Falls back to legacy UpsertClip on nil dispatcher.
	if err := s.dispatchOrIndex(ctx, existing, existing.FileHash()); err != nil {
		s.log.Warn("failed to enrich YouTube clip with metadata",
			zap.String("clip_id", clipID),
			zap.Error(err))
		return
	}

	// Write/update metadata.json alongside the clip file and upload to Drive
	s.writeClipMetadataFile(ctx, existing, ym)

	s.log.Info("YouTube clip enriched with metadata",
		zap.String("clip_id", clipID),
		zap.String("title", ym.Title),
		zap.String("language", ym.Language),
		zap.Int("tags", len(ym.Tags)),
		zap.Int("categories", len(ym.Categories)),
		zap.Int("semantic_text_len", len(embeddingText)),
	)
}
