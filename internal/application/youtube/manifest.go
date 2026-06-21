package youtube

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// updateManifest updates the clip manifest with the processed segment.
func (s *Service) updateManifest(manifest *asset.ClipManifest, seg Segment, clipID string, item ExtractItem,
	startSec, endSec, duration int, localPath, fileHash string) {
	if manifest == nil {
		return
	}

	filename := item.Filename
	if filename == "" && localPath != "" {
		filename = filepath.Base(localPath)
	}
	if filename == "." {
		filename = ""
	}

	newMItem := asset.ClipManifestItem{
		ID:              clipID,
		Name:            item.Name,
		Start:           item.Start,
		End:             item.End,
		StartSeconds:    startSec,
		EndSeconds:      endSec,
		DurationSeconds: duration,
		Filename:        filename,
		LocalPath:       item.LocalPath,
		DriveLink:       item.DriveLink,
		FileHash:        fileHash,
		Status:          item.Status,
		Tags:            append([]string(nil), seg.Tags...),
	}

	// Read per-clip metadata file (already written locally by writeClipMetadataFile)
	// to enrich the combined manifest with description, transcript, and video info.
	perClipMetaPath := filepath.Join(filepath.Dir(localPath), "metadata_"+clipID+".json")
	if metaBytes, err := os.ReadFile(perClipMetaPath); err == nil {
		var clipMeta ClipMetadataFile
		if err := json.Unmarshal(metaBytes, &clipMeta); err == nil {
			newMItem.RawName = clipMeta.RawTitle
			newMItem.CleanTitle = clipMeta.CleanTitle
			newMItem.ShortTitle = clipMeta.ShortTitle
			newMItem.EmbeddingText = clipMeta.EmbeddingText
			newMItem.VideoTitle = clipMeta.VideoTitle
			newMItem.Channel = clipMeta.Channel
			newMItem.Description = clipMeta.Description
			newMItem.RawTranscript = clipMeta.RawTranscript
			newMItem.Transcript = clipMeta.Transcript
			newMItem.CleanTranscript = clipMeta.CleanTranscript
			newMItem.ClipSummary = clipMeta.ClipSummary
			newMItem.Hook = clipMeta.Hook
			newMItem.Topics = append([]string(nil), clipMeta.Topics...)
			newMItem.Speakers = append([]string(nil), clipMeta.Speakers...)
			newMItem.People = append([]string(nil), clipMeta.People...)
			newMItem.MentionedPeople = append([]string(nil), clipMeta.MentionedPeople...)
			newMItem.SourceTags = append([]string(nil), clipMeta.SourceTags...)
			newMItem.ClipTags = append([]string(nil), clipMeta.ClipTags...)
			newMItem.SearchKeywords = append([]string(nil), clipMeta.SearchKeywords...)
			newMItem.QualityScore = clipMeta.QualityScore
			newMItem.SearchVisibility = clipMeta.SearchVisibility
			newMItem.DuplicateGroupID = clipMeta.DuplicateGroupID
			newMItem.DuplicateOf = clipMeta.DuplicateOf
			newMItem.IsDuplicate = clipMeta.IsDuplicate
			newMItem.IsBestVersion = clipMeta.IsBestVersion
			newMItem.DuplicateReason = clipMeta.DuplicateReason
			newMItem.DuplicateScore = clipMeta.DuplicateScore
			newMItem.TopicClusterID = clipMeta.TopicClusterID
			newMItem.TopicClusterLabel = clipMeta.TopicClusterLabel
			newMItem.TopicClusterSize = clipMeta.TopicClusterSize
			newMItem.TopicClusterRank = clipMeta.TopicClusterRank
			newMItem.YouTubeURL = clipMeta.YouTubeURL
			if len(clipMeta.Tags) > 0 {
				tagSet := make(map[string]struct{}, len(newMItem.Tags)+len(clipMeta.Tags))
				merged := make([]string, 0, len(newMItem.Tags)+len(clipMeta.Tags))
				for _, tag := range newMItem.Tags {
					normalized := strings.ToLower(strings.TrimSpace(tag))
					if normalized == "" {
						continue
					}
					if _, ok := tagSet[normalized]; ok {
						continue
					}
					tagSet[normalized] = struct{}{}
					merged = append(merged, tag)
				}
				for _, tag := range clipMeta.Tags {
					normalized := strings.ToLower(strings.TrimSpace(tag))
					if normalized == "" {
						continue
					}
					if _, ok := tagSet[normalized]; ok {
						continue
					}
					tagSet[normalized] = struct{}{}
					merged = append(merged, tag)
				}
				newMItem.Tags = merged
			}
		}
	}

	// Replace existing or append new
	for j, mItem := range manifest.Clips {
		if mItem.ID == clipID {
			manifest.Clips[j] = newMItem
			return
		}
	}
	manifest.Clips = append(manifest.Clips, newMItem)
}

// ── Clip Name Cleanup ───────────────────────────────────────────────────────

// cleanClipName strips ugly artifacts from segment names produced by subtitle
// extraction or Ollama analysis. These artifacts ("gt gt", "[music]", HTML entities)
// are meaningless in UI and reduce semantic search quality.
//
// Examples:
//
//	"gt gt Mhm"                 → "Mhm"
//	"gt gt to the production"   → "to the production"
//	"they said that he just"    → "they said that he just" (unchanged, valid speech)
//	"[music] Introduction"      → "Introduction"
func cleanClipName(name string) string {
	// Strip HTML entities that survive subtitle extraction.
	// Order matters: replace longer entities first so shorter ones
	// don't consume them (e.g. "&gt;" would consume part of "&gt;&gt;").
	name = strings.ReplaceAll(name, "&gt;&gt;", "")
	name = strings.ReplaceAll(name, "&gt;", "")
	name = strings.ReplaceAll(name, "&nbsp;", " ")

	// Strip subtitle artifacts
	name = strings.ReplaceAll(name, "gt gt", "")
	name = strings.ReplaceAll(name, "[music]", "")
	name = strings.ReplaceAll(name, "[Music]", "")
	name = strings.ReplaceAll(name, "[MUSIC]", "")
	name = strings.ReplaceAll(name, "[Applause]", "")
	name = strings.ReplaceAll(name, "[__]", "")
	name = strings.ReplaceAll(name, "[ __ ]", "")

	// Collapse multiple spaces and trim
	name = strings.Join(strings.Fields(name), " ")
	name = strings.TrimSpace(name)

	// Truncate to safe filesystem length (80 runes, not bytes)
	const maxClipNameRunes = 80
	runes := []rune(name)
	if len(runes) > maxClipNameRunes {
		name = string(runes[:maxClipNameRunes])
		name = strings.TrimRight(name, "-_ ")
	}

	if name == "" {
		name = "clip"
	}

	return name
}
