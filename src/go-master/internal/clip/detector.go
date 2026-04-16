package clip

import (
	"strings"

	"velox/go-master/internal/nlp"
)

func (idx *Indexer) extractTags(clipName, folderPath, group string) []string {
	var tags []string

	nameKeywords := nlp.ExtractKeywords(clipName, 20)
	for _, kw := range nameKeywords {
		tag := strings.ToLower(kw.Word)
		if len(tag) > 2 && !idx.isStopWord(tag) {
			tags = append(tags, tag)
		}
	}

	pathParts := strings.Split(strings.ToLower(folderPath), "/")
	for _, part := range pathParts {
		cleaned := strings.ReplaceAll(part, "_", " ")
		cleaned = strings.ReplaceAll(cleaned, "-", " ")
		pathKeywords := nlp.ExtractKeywords(cleaned, 10)
		for _, kw := range pathKeywords {
			tag := strings.ToLower(kw.Word)
			if len(tag) > 2 && !idx.isStopWord(tag) && !containsTag(tags, tag) {
				tags = append(tags, tag)
			}
		}
	}

	if group != "" && !containsTag(tags, group) {
		tags = append(tags, strings.ToLower(group))
	}

	return tags
}

func (idx *Indexer) detectGroupFromPath(path string) string {
	pathLower := strings.ToLower(path)

	segments := strings.Split(path, "/")

	for i := len(segments) - 1; i >= 1; i-- {
		segment := strings.TrimSpace(segments[i])
		segmentLower := strings.ToLower(segment)

		for _, g := range ClipGroups {
			gLower := strings.ToLower(g.Name)
			gIDLower := strings.ToLower(g.ID)

			if segmentLower == gLower || segmentLower == gIDLower ||
				strings.Contains(segmentLower, gLower) || strings.Contains(segmentLower, gIDLower) {
				return g.ID
			}
		}
	}

	for _, g := range ClipGroups {
		if strings.Contains(pathLower, strings.ToLower(g.ID)) ||
			strings.Contains(pathLower, strings.ToLower(g.Name)) {
			return g.ID
		}
	}

	return "general"
}

func (idx *Indexer) detectResolutionFromName(filename string) string {
	nameLower := strings.ToLower(filename)

	switch {
	case strings.Contains(nameLower, "4k") || strings.Contains(nameLower, "2160"):
		return "3840x2160"
	case strings.Contains(nameLower, "1080") || strings.Contains(nameLower, "fhd"):
		return "1920x1080"
	case strings.Contains(nameLower, "720") || strings.Contains(nameLower, "hd"):
		return "1280x720"
	case strings.Contains(nameLower, "480"):
		return "854x480"
	default:
		return "unknown"
	}
}

func (idx *Indexer) detectMediaTypeFromPath(path string) string {
	pathLower := strings.ToLower(path)
	segments := strings.Split(pathLower, "/")

	if len(segments) == 0 {
		return MediaTypeClip
	}

	topFolder := strings.TrimSpace(segments[0])

	stockKeywords := []string{"stock", "stock cartella", "stockfootage", "stock_footage", "stock-video", "stockvideo"}
	for _, kw := range stockKeywords {
		if strings.Contains(topFolder, kw) || topFolder == kw {
			return MediaTypeStock
		}
	}

	clipKeywords := []string{"clip", "clips", "cartellaclip", "clip_cartella", "clip-folder", "clipfolder"}
	for _, kw := range clipKeywords {
		if strings.Contains(topFolder, kw) || topFolder == kw {
			return MediaTypeClip
		}
	}

	for _, kw := range clipKeywords {
		if strings.Contains(pathLower, kw) {
			return MediaTypeClip
		}
	}

	for _, g := range ClipGroups {
		gLower := strings.ToLower(g.ID)
		gNameLower := strings.ToLower(g.Name)
		if topFolder == gLower || topFolder == gNameLower ||
			strings.Contains(topFolder, gLower) || strings.Contains(topFolder, gNameLower) {
			return MediaTypeClip
		}
	}

	return MediaTypeStock
}

func (idx *Indexer) isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true,
		"this": true, "that": true, "from": true, "have": true,
		"has": true, "had": true, "was": true, "were": true,
		"clip": true, "video": true, "file": true, "final": true,
	}
	return stopWords[word]
}
