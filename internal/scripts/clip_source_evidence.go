package scripts

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// cleanTranscriptJSON extracts a "description" field from a JSON blob if the
// transcript text happens to be a raw metadata blob instead of plain text.
// Returns (cleanedText, description).
func cleanTranscriptJSON(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return raw, ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return raw, ""
	}
	// Try description first, then clip_summary
	for _, key := range []string{"description", "clip_summary"} {
		if v, ok := obj[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s, s
			}
		}
	}
	return raw, ""
}

// buildEvidence converts a MediaAsset to ClipEvidence, applying validation rules.
func (b *ClipSourceBuilder) buildEvidence(asset *asset.MediaAsset, opts *ClipGenerationOptions) ClipEvidence {
	ev := ClipEvidence{
		ClipID:       asset.ID,
		Title:        asset.Name,
		YouTubeTitle: asset.GetMetadataString("youtube_title"),
		Summary:      asset.GetMetadataString("clip_summary"),
		Description:  asset.GetMetadataString("description"),
		DriveLink:    asset.DriveLink(),
		Hook:         asset.GetMetadataString("hook"),
		Language:     asset.GetMetadataString("language"),
		DurationSec:  int(asset.DurationMs / 1000),
	}

	// DriveLink fallback: check metadata if struct field is empty
	if ev.DriveLink == "" {
		ev.DriveLink = asset.GetMetadataString("drive_link")
	}

	// Description fallback: use summary if description is empty
	if ev.Description == "" {
		ev.Description = ev.Summary
	}

	// Topics
	if v := asset.GetMetadataString("topics"); v != "" {
		json.Unmarshal([]byte(v), &ev.Topics)
	}

	// Speakers + people
	if v := asset.GetMetadataString("speakers"); v != "" {
		json.Unmarshal([]byte(v), &ev.Speakers)
	}
	if v := asset.GetMetadataString("mentioned_people"); v != "" {
		json.Unmarshal([]byte(v), &ev.MentionedPeople)
	}

	// Quality score
	if v := asset.GetMetadataString("quality_score"); v != "" {
		fmt.Sscanf(v, "%f", &ev.QualityScore)
	}

	// Transcript (for evidence chunks, we store a simple version)
	transcript := asset.GetMetadataString("clean_transcript")
	if transcript == "" {
		transcript = asset.GetMetadataString("transcript")
	}
	if transcript == "" {
		transcript = asset.GetMetadataString("raw_transcript")
	}

	// Clean up: if transcript is raw JSON, extract description from it.
	// Also use the extracted description as a fallback if still empty.
	var extractedDesc string
	transcript, extractedDesc = cleanTranscriptJSON(transcript)
	if ev.Description == "" && extractedDesc != "" {
		ev.Description = extractedDesc
	}

	ev.TranscriptWords = textutil.CountWords(transcript)

	// Validation: transcript required unless AllowNoTranscript is set
	if transcript == "" && !opts.AllowNoTranscript {
		ev.Excluded = true
		ev.ExcludeReason = "no_transcript"
		return ev
	}
	if opts.MinTranscriptWords > 0 && ev.TranscriptWords < opts.MinTranscriptWords {
		ev.Excluded = true
		ev.ExcludeReason = fmt.Sprintf("transcript_too_short:%d<%d", ev.TranscriptWords, opts.MinTranscriptWords)
		return ev
	}
	if opts.MinQualityScore > 0 && ev.QualityScore < opts.MinQualityScore {
		ev.Excluded = true
		ev.ExcludeReason = fmt.Sprintf("quality_too_low:%.2f<%.2f", ev.QualityScore, opts.MinQualityScore)
		return ev
	}

	// Build evidence chunks (split by paragraphs, assign fake timestamps)
	paragraphs := strings.Split(transcript, "\n\n")
	chunkDuration := 0
	if len(paragraphs) > 0 && ev.DurationSec > 0 {
		chunkDuration = ev.DurationSec / len(paragraphs) * 1000
	}
	for i, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		startMS := int64(i * chunkDuration)
		endMS := startMS + int64(chunkDuration)
		ev.EvidenceChunks = append(ev.EvidenceChunks, EvidenceChunk{
			StartMS: startMS,
			EndMS:   endMS,
			Text:    para,
		})
	}

	return ev
}
