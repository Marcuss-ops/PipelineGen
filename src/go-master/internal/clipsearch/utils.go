package clipsearch

import (
	"regexp"
	"strings"
)

func sanitizeFilename(name string) string {
	result := strings.ReplaceAll(name, " ", "_")
	result = strings.ReplaceAll(result, "/", "_")
	result = strings.ReplaceAll(result, "\\", "_")
	result = strings.ReplaceAll(result, ":", "_")
	result = strings.ReplaceAll(result, "*", "_")
	result = strings.ReplaceAll(result, "?", "_")
	result = strings.ReplaceAll(result, "\"", "_")
	result = strings.ReplaceAll(result, "<", "_")
	result = strings.ReplaceAll(result, ">", "_")
	result = strings.ReplaceAll(result, "|", "_")
	return result
}

func sanitizeDriveFolderName(keyword string) string {
	name := strings.TrimSpace(strings.ToLower(keyword))
	name = sanitizeFilename(name)
	if name == "" {
		return "misc"
	}
	return name
}

func firstKeywordToken(keyword string) string {
	parts := strings.Fields(strings.TrimSpace(keyword))
	if len(parts) == 0 {
		return ""
	}
	return sanitizeDriveFolderName(parts[0])
}

func firstNKeywordTokens(keyword string, n int) string {
	if n <= 0 {
		return ""
	}
	parts := strings.Fields(strings.TrimSpace(keyword))
	if len(parts) == 0 {
		return ""
	}
	if len(parts) < n {
		n = len(parts)
	}
	return sanitizeDriveFolderName(strings.Join(parts[:n], " "))
}

func keywordFolderCandidates(keyword string) []string {
	full := sanitizeDriveFolderName(keyword)
	firstTwo := firstNKeywordTokens(keyword, 2)
	first := firstKeywordToken(keyword)

	out := []string{full}
	if firstTwo != "" && firstTwo != full {
		out = append(out, firstTwo)
	}
	if first != "" && first != full {
		out = append(out, first)
	}

	raw := strings.TrimSpace(strings.ToLower(keyword))
	if raw != "" && raw != full && raw != first {
		out = append(out, raw)
	}
	withSpaces := strings.ReplaceAll(full, "_", " ")
	if withSpaces != "" && withSpaces != full && withSpaces != first && withSpaces != raw {
		out = append(out, withSpaces)
	}
	return out
}

var folderNormRx = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeFolderComparable(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.ReplaceAll(name, "-", " ")
	name = folderNormRx.ReplaceAllString(name, " ")
	name = strings.Join(strings.Fields(name), " ")
	return name
}

func normalizeKeywords(keywords []string) []string {
	out := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw != "" {
			out = append(out, kw)
		}
	}
	return out
}
