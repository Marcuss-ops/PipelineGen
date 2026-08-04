// Package usecase — metadata_enrich.go: YouTube clip metadata enrichment,
// ported from the deprecated MetadataService onto the canonical *Service.
//
// P0.3 (CLIPS-META-2026-07-04, Azione 3): the MetadataService struct,
// MetadataDeps, and NewMetadataService constructor have been retired.
// enrichClip, buildFallbackSearchText, and all helper functions now
// live as private methods on *Service so callers in callbacks.go and
// job_registration.go can call s.enrichClip(...) directly.
//
// Canonical helper delegations (isSponsorSegment, calculateQualityScore,
// WriteClipMetadataFile) route through youtube/metadata/service.go
// per P0.1 + P0.2 closures.
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

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	ports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TranscriptReader reads the on-disk transcript file for a clip.
// Pattern 0 port (P1.3): extracts os.ReadFile behind a typed interface
// so tests can inject an in-memory reader without touching the filesystem.
type TranscriptReader interface {
	ReadTranscript(clip *asset.Asset) (string, error)
}

// OSTranscriptReader is the concrete filesystem-backed TranscriptReader.
// Uses os.ReadFile on the canonical transcript path derived from the
// clip's LocalPath (same file, .txt extension).
type OSTranscriptReader struct{}

// Compile-time assertion: OSTranscriptReader satisfies TranscriptReader.
var _ TranscriptReader = (*OSTranscriptReader)(nil)

// ReadTranscript reads the transcript file at <clip.LocalPath without ext>.txt.
// Returns the trimmed content (max 5000 chars). A missing file returns ("", nil) —
// the caller treats an empty transcript as a legitimate "no transcript" state.
func (OSTranscriptReader) ReadTranscript(clip *asset.Asset) (string, error) {
	if clip == nil || clip.LocalPath() == "" {
		return "", nil
	}
	transcriptPath := strings.TrimSuffix(clip.LocalPath(), filepath.Ext(clip.LocalPath())) + ".txt"
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return "", nil // missing transcript is not an error
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 5000 {
		text = text[:5000]
	}
	return text, nil
}

// skipMetadataKeysForSearchText is the canonical deny-list
// for metadata entries that would otherwise bloat the
// assembled SearchText beyond the 1024-byte budget.
var skipMetadataKeysForSearchText = map[string]struct{}{
	"embedding_text":      {},
	"clean_transcript":    {},
	"youtube_chapters":    {},
	"youtube_categories":  {},
	"youtube_description": {},
}

// enrichClip updates a clip's metadata with YouTube video information
// (title, description, tags, language, categories, chapters) to enable rich
// semantic search across multiple languages and conceptual queries.
//
// RESILIENCE: If meta is nil (e.g., yt-dlp failed during extraction), this
// function falls back to fetching YouTube metadata directly via the
// metaFetcher port.
func (s *Service) enrichClip(ctx context.Context, clipID string, meta *ports.DownloaderMetadata, force bool) {
	if s.clips == nil {
		return
	}

	existing, err := s.clips.GetClip(ctx, clipID)
	if err != nil || existing == nil {
		s.log.Warn("cannot enrich YouTube clip: not found in DB", zap.String("clip_id", clipID))
		return
	}

	if !force && existing.YouTubeTitle() != "" && existing.SearchText != "" {
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
		s.buildFallbackSearchText(existing)
		ytLang := existing.YouTubeLanguage()
		if ytLang != "" {
			existing.SetLanguage(ytLang)
		}
		if err := s.assetRepo.Upsert(ctx, existing); err != nil {
			s.log.Warn("failed to save fallback search_text", zap.String("clip_id", clipID), zap.Error(err))
		}
		return
	}

	cleanedDescription := tagutil.CleanYouTubeDescription(ym.Description)

	// Read transcript via Pattern 0 port (P1.3).
	var clipTranscript string
	if s.transcriptReader != nil {
		if text, err := s.transcriptReader.ReadTranscript(existing); err == nil && text != "" {
			clipTranscript = text
		}
	}
	cleanedTranscript := tagutil.CleanClipTranscript(clipTranscript)

	// Build clip metadata from user-provided fields or Ollama.
	// P0.3: the legacy GenerateClipMetadata stub always returned nil,
	// so we skip the Ollama call and fall through to fallback fields.
	clipMetadata := s.resolveExistingMetadata(existing, cleanedTranscript)

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
		existing.SetClipSummary(clipMetadata.Summary)
		existing.SetTopics(clipMetadata.Topics)
		existing.SetSpeakers(clipMetadata.Speakers)
		existing.SetMentionedPeople(clipMetadata.MentionedPeople)
		existing.SetPeople(tagutil.MergeTagLists(clipMetadata.Speakers, clipMetadata.MentionedPeople, clipMetadata.People))
		existing.SetSourceTags(clipMetadata.SourceTags)
		existing.SetClipTags(clipMetadata.ClipTags)
		existing.SetSearchKeywords(clipMetadata.SearchKeywords)
		existing.SetHook(clipMetadata.Hook)
		existing.SetCleanTitle(clipMetadata.CleanTitle)
		existing.SetShortTitle(clipMetadata.ShortTitle)
		existing.SetEmbeddingText(clipMetadata.EmbeddingText)
		existing.SetSemanticTags(clipMetadata.Tags)
		existing.SetQualityScore(clipMetadata.QualityScore)
	}
	if cleanedTranscript != "" {
		existing.SetCleanTranscript(cleanedTranscript)
	}
	if ym.Language != "" {
		existing.SetLanguage(ym.Language)
	}

	existing.SetYouTubeTitle(ym.Title)
	existing.SetYouTubeDescription(ym.Description)
	existing.SetYouTubeLanguage(ym.Language)
	existing.SetYouTubeUploader(ym.Uploader)
	existing.SetYouTubeUploadDate(ym.UploadDate)
	existing.SetYouTubeViewCount(fmt.Sprintf("%d", ym.ViewCount))
	existing.SetYouTubeDuration(fmt.Sprintf("%.1f", ym.Duration))
	existing.SetYouTubeVideoID(ym.ID)
	existing.SetYouTubeURL(fmt.Sprintf("https://www.youtube.com/watch?v=%s", ym.ID))
	if len(ym.Categories) > 0 {
		catsJSON, _ := json.Marshal(ym.Categories)
		existing.SetYouTubeCategories(string(catsJSON))
	}
	if len(ym.Tags) > 0 {
		existing.SetYouTubeTags(ym.Tags)
	}

	// Sponsor detection via canonical regex (P0.1 closure).
	if clipTranscript != "" && ytmetadata.IsSponsorSegment(clipTranscript) {
		existing.SetIsSponsorSegment(true)
		existing.SetSponsorConfidence("high")
	} else if existing.Metadata != nil {
		delete(existing.Metadata, "is_sponsor_segment")
		delete(existing.Metadata, "sponsor_confidence")
	}

	// Quality score via canonical 40/40/20 formula (P0.2 closure).
	qualityScore := clipQualityScore(cleanedTranscript, ym.Duration, clipMetadata)
	existing.SetQualityScore(qualityScore)
	existing.SetQualityTier(getQualityTier(qualityScore))
	existing.SetSearchVisibility(tagutil.DeriveSearchVisibility(qualityScore))

	if len(ym.Chapters) > 0 {
		chaptersJSON, _ := json.Marshal(ym.Chapters)
		existing.SetYouTubeChapters(string(chaptersJSON))
	}
	if ym.ThumbnailURL != "" {
		existing.SetYouTubeThumbnail(ym.ThumbnailURL)
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

// resolveExistingMetadata builds a CanonicalClipMetadata from user-provided
// metadata already stored on the asset. P0.3: the legacy GenerateClipMetadata
// stub always returned nil, so we skip the Ollama path and build from
// existing metadata only.
func (s *Service) resolveExistingMetadata(existing *asset.Asset, cleanedTranscript string) *tagutil.CanonicalClipMetadata {
	hasUserSummary := existing.ClipSummary() != ""
	hasUserTopics := len(existing.Topics()) > 0

	if hasUserSummary && hasUserTopics {
		s.log.Info("using user-provided custom metadata, skipping Ollama enrichment",
			zap.String("clip_id", existing.ID))
		cm := &tagutil.CanonicalClipMetadata{
			Summary:          existing.ClipSummary(),
			Topics:           existing.Topics(),
			Speakers:         existing.Speakers(),
			MentionedPeople:  existing.MentionedPeople(),
			Hook:             existing.Hook(),
			QualityScore:     existing.QualityScore(),
			CleanTitle:       existing.CleanTitle(),
			ShortTitle:       existing.ShortTitle(),
			SearchVisibility: existing.SearchVisibility(),
			CleanTranscript:  cleanedTranscript,
		}
		if cm.CleanTitle == "" {
			cm.CleanTitle = existing.Name
		}
		return cm
	}
	return nil
}

// buildFallbackSearchText populates clip.SearchText from a deterministic
// in-file assembly of clip.Name + clip.Tags + Metadata.
func (s *Service) buildFallbackSearchText(clip *asset.Asset) {
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

// clipQualityScore computes the canonical quality score for a clip
// using the 40/40/20 weighted formula from metadata.CalculateQualityScore
// plus a -0.20 sponsor penalty per the canonical contract (P0.2 closure).
func clipQualityScore(transcript string, durationSec float64, meta *tagutil.CanonicalClipMetadata) float64 {
	transcriptWordCount := ytmetadata.CountWords(transcript)
	clipDuration := int(durationSec)

	topicCount := 0
	speakerCount := 0
	mentionedCount := 0
	if meta != nil {
		topicCount = len(meta.Topics)
		speakerCount = len(meta.Speakers)
		mentionedCount = len(meta.MentionedPeople)
	}

	score := ytmetadata.CalculateQualityScore(transcriptWordCount, clipDuration, topicCount, speakerCount, mentionedCount)

	if ytmetadata.IsSponsorSegment(transcript) {
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

func buildVideoURL(clipID string, existing *asset.Asset) string {
	if existing != nil {
		if url := existing.ExternalURL(); url != "" {
			return url
		}
	}
	return ""
}
