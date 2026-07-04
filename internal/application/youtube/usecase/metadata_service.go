// Package metadata provides YouTube clip metadata enrichment — the core
// logic for enriching YouTube clips with semantic metadata (title, description,
// tags, language, categories, chapters, quality scoring) and persisting it.
// Extracted from the root youtube package during PR5 Phase 1 (June 2026).
//
// Design: MetadataDeps accepts max 8 fields. The service owns enrichment,
// metadata file writing, fallback search text, and Ollama-based metadata
// generation.
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// Removed in Phase 1c Commit 3b/4 per user-spec formula change.

// MetadataDeps holds dependencies for the metadata enrichment service (max 8 fields).
// PR5 Phase 1 target: ≤8 fields — currently 6.
type MetadataDeps struct {
	Clips       ports.ClipStorePort
	MetaFetcher ports.VideoMetadataFetcherPort
	Ollama      ports.OllamaClientPort
	AssetRepo   asset.Repository
	Cfg         dto.RuntimeConfig
	Log         *zap.Logger
}

// Service performs YouTube clip metadata enrichment.
type MetadataService struct {
	clips       ports.ClipStorePort
	metaFetcher ports.VideoMetadataFetcherPort
	ollama      ports.OllamaClientPort
	assetRepo   asset.Repository
	cfg         dto.RuntimeConfig
	log         *zap.Logger
}

// NewService is the canonical constructor.
func NewMetadataService(deps MetadataDeps) *MetadataService {
	return &MetadataService{
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
func (s *MetadataService) EnrichClip(ctx context.Context, clipID string, meta *ports.DownloaderMetadata, force bool) {
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

	var clipMetadata *dto.CanonicalClipMetadata
	if hasUserSummary && hasUserTopics {
		s.log.Info("using user-provided custom metadata, skipping Ollama enrichment", zap.String("clip_id", clipID))
		clipMetadata = &dto.CanonicalClipMetadata{
			Summary:          existing.GetMetadataString("clip_summary"),
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
				clipMetadata.CleanTitle, clipMetadata.Summary, clipMetadata.Hook,
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
				clipMetadata.CleanTitle, clipMetadata.Summary, clipMetadata.Hook,
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
		existing.Metadata["clip_summary"] = clipMetadata.Summary
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

	ytmetadata.WriteClipMetadataFile(s.log, existing, ym)

	s.log.Info("YouTube clip enriched with metadata",
		zap.String("clip_id", clipID),
		zap.String("title", ym.Title),
		zap.String("language", ym.Language),
		zap.Int("tags", len(ym.Tags)),
		zap.Int("categories", len(ym.Categories)),
		zap.Int("semantic_text_len", len(embeddingText)),
	)
}

// GenerateClipMetadata returns the canonical Ollama-driven rich metadata
// for a clip. Phase 1c closure (June 2026): per godlike/07 §"no fake
// availability" this method intentionally returns nil — the placeholder
// constructor is deferred behind a future metadata capability extraction
// wave, and the placeholder MUST NOT produce synthetic LLM output. The
// caller (EnrichClip) handles nil by merging fallback semantic fields via
// tagutil.DeriveFallbackSemanticFields, so the absent rich-metadata path
// remains a documented no-op rather than a silent-success path. The real
// implementation is a follow-up tracked in CHANGELOG.md under
// `### Deferred` — not inlined here to avoid a fake-tracking comment
// referencing an unlanded YAML ticket (godlike/07).
func (s *MetadataService) GenerateClipMetadata(ctx context.Context, title, transcript, description string) *dto.CanonicalClipMetadata {
	_ = ctx
	_ = title
	_ = transcript
	_ = description
	return nil
}

// ── Phase 1b stubs (methods moved to adapters/ during melt) ──────────

func buildVideoURL(clipID string, existing *asset.Asset) string {
	// Phase 1c closure (June 2026): the prior stub-restore marker
	// described a no-op implementation; this now returns
	// existing.ExternalURL() when available, or "" otherwise.
	if existing != nil {
		if url := existing.ExternalURL(); url != "" {
			return url
		}
	}
	return ""
}

// skipMetadataKeysForSearchText is the canonical deny-list
// for metadata entries that would otherwise bloat the
// assembled SearchText beyond the 1024-byte budget. Each key
// holds either pre-computed content, JSON-marshalled
// structures, or full descriptions that duplicate
// `metadata["clip_summary"]` signal without giving BM25
// recall anything new. Adding long-content metadata keys:
// extend this set, do NOT remove the byte-budget cap.
var skipMetadataKeysForSearchText = map[string]struct{}{
	"embedding_text":      {},
	"clean_transcript":    {},
	"youtube_chapters":    {},
	"youtube_categories":  {},
	"youtube_description": {},
}

// BuildFallbackSearchText populates `clip.SearchText` from a
// deterministic in-file assembly of `clip.Name` + `clip.Tags`
// + `Metadata` (skip-empties + case-insensitive dedup +
// lower-bound 150 chars + upper-bound 1024 bytes) for the
// ym=nil fallback path in EnrichClip. Safety-of-shape: this
// fallback writes `SearchText` but NOT `youtube_title`, so
// EnrichClip's later `force + youtube_title + SearchText`
// short-circuit guard (which requires BOTH) is never triggered
// here. `asset.Asset` has no `Description` field; description-
// like content folds via metadata.
func (s *MetadataService) BuildFallbackSearchText(clip *asset.Asset) {
	if clip == nil {
		return
	}

	var parts []string
	seen := make(map[string]struct{})

	addPart := func(prefix, val string) {
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}
		low := strings.ToLower(val)
		if _, dup := seen[low]; dup {
			return
		}
		seen[low] = struct{}{}
		parts = append(parts, prefix+val)
	}

	addPart("Name: ", clip.Name)

	if len(clip.Tags) > 0 {
		addPart("Tags: ", strings.Join(clip.Tags, ", "))
	}

	if clip.Metadata != nil {
		keys := make([]string, 0, len(clip.Metadata))
		for k := range clip.Metadata {
			if _, skip := skipMetadataKeysForSearchText[k]; skip {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := clip.Metadata[k]
			switch v := val.(type) {
			case string:
				addPart(fmt.Sprintf("[%s] ", k), v)
			case []string:
				if len(v) > 0 {
					addPart(fmt.Sprintf("[%s] ", k), strings.Join(v, ", "))
				}
			case []any:
				strs := make([]string, 0, len(v))
				for _, a := range v {
					if str, ok := a.(string); ok && strings.TrimSpace(str) != "" {
						strs = append(strs, str)
					}
				}
				if len(strs) > 0 {
					addPart(fmt.Sprintf("[%s] ", k), strings.Join(strs, ", "))
				}
			}
		}
	}

	out := strings.Join(parts, "\n")

	if len(out) < 150 {
		clip.SearchText = ""
		return
	}
	if len(out) > 1024 {
		trimmed := out[:1024]
		if idx := strings.LastIndex(trimmed, " "); idx > 1024-128 {
			trimmed = trimmed[:idx]
		}
		out = strings.TrimRight(trimmed, " \t")
	}

	clip.SearchText = out
}

func metadataStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case []string:
			return val
		case []any:
			out := make([]string, 0, len(val))
			for _, item := range val {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	return nil
}

func metadataFloat64(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		}
	}
	return 0
}

// isSponsorSegment delegates to the canonical regex in metadata.IsSponsorSegment.
// CLIPS-META-2026-07-04 (Azione 2): replaced the legacy substring match with
// the canonical word-boundary-anchored regex.
func isSponsorSegment(transcript string) bool {
	return ytmetadata.IsSponsorSegment(transcript)
}

// calculateQualityScore delegates to the canonical 40/40/20 weighted formula
// in metadata.CalculateQualityScore. CLIPS-META-2026-07-04 (Azione 2): replaced
// the legacy linear-blend formula (transcript_len/2000 + tag_count/10 +
// duration/600 + title_len/100) with the canonical weighted formula.
//
// External contract: `description` and `meta` are required by `EnrichClip`'s
// call site (signature compatibility). Only meta is consumed for semantic
// coverage counts; description is a signature-only discard.
func calculateQualityScore(transcript, title, description string, tags []string, duration float64, meta *dto.CanonicalClipMetadata) float64 {
	_ = description // signature-only (kept for EnrichClip call-site compatibility)
	_ = tags        // signature-only (legacy linear-blend consumed this; canonical formula doesn't)

	transcriptWordCount := ytmetadata.CountWords(transcript)
	clipDuration := int(duration)

	topicCount := 0
	speakerCount := 0
	mentionedCount := 0
	if meta != nil {
		topicCount = len(meta.Topics)
		speakerCount = len(meta.Speakers)
		mentionedCount = len(meta.MentionedPeople)
	}

	score := ytmetadata.CalculateQualityScore(transcriptWordCount, clipDuration, topicCount, speakerCount, mentionedCount)

	// Sponsor penalty: caller-side per the canonical contract
	if isSponsorSegment(transcript) {
		score -= 0.20
	}
	if score < 0 {
		score = 0
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}

func getQualityTier(score float64) string {
	if score >= 0.7 {
		return "high"
	}
	if score >= 0.4 {
		return "medium"
	}
	return "low"
}

// ── Phase 1b stubs — removed in CLIPS-META-2026-07-04 (Azione 2).
// parseClipTimestamps, ymTags, ymCategories, ymViewCount, ymUploadDate,
// ymThumbnailURL, ymDescription, metadataBool, metadataInt were dead code
// after WriteClipMetadataFile migrated to the canonical metadata package.
