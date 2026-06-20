package youtube

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// buildFallbackSearchText builds a minimal search_text from existing clip metadata.
// This ensures search_text is NEVER empty for YouTube clips, even when yt-dlp is unavailable.
//
// Order: Title > Segment > Description keywords > Tags > Uploader > Language.
// Transcript is intentionally excluded to prevent false positives from
// literal phrase matches. It is indexed separately in the Qdrant "transcript"
// named vector for hybrid search.
func (s *Service) buildFallbackSearchText(clip *asset.Asset) {
	var parts []string

	// 1. YouTube title
	ytTitle := clip.GetMetadataString("youtube_title")
	if ytTitle != "" {
		parts = append(parts, "Title: "+ytTitle)
	}

	// 2. Segment name (clip-specific, descriptive)
	if clip.Name != "" {
		parts = append(parts, "Segment: "+clip.Name)
	}

	// 3. Description keywords only. Raw description stays in metadata, but the
	// embedding should not ingest the full sponsor-heavy description block.
	// Transcript is intentionally excluded to prevent false positives from
	// literal phrase matches. Transcript is indexed separately in Qdrant as
	// the "transcript" named vector for hybrid search.
	ytDesc := clip.GetMetadataString("youtube_description")
	if ytDesc != "" {
		cleanedDesc := cleanYouTubeDescription(ytDesc)
		if cleanedDesc != "" {
			if phrases := extractKeyPhrases(cleanedDesc, 5); len(phrases) > 0 {
				parts = append(parts, "Description keywords: "+strings.Join(phrases, ", "))
			}
		}
	}

	// 5. Tags
	ytTags := clip.GetMetadataString("youtube_tags")
	if ytTags != "" && ytTags != "[]" {
		parts = append(parts, "Tags: "+ytTags)
	}
	if len(clip.Tags) > 0 {
		parts = append(parts, "Tags: "+strings.Join(clip.Tags, ", "))
	}

	// 6. Uploader / channel
	ytUploader := clip.GetMetadataString("youtube_uploader")
	if ytUploader != "" {
		parts = append(parts, "Uploader: "+ytUploader)
	}

	// 7. Language
	ytLang := clip.GetMetadataString("youtube_language")
	if ytLang != "" {
		parts = append(parts, "Language: "+ytLang)
	}

	if len(parts) > 0 {
		clip.SearchText = strings.Join(parts, "\n")
	}
}
