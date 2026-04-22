package scriptdocs

import (
	"strings"
	"unicode"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
)

func clipSearchKeys(clip stockdb.StockClipEntry) []string {
	seen := make(map[string]bool)
	var keys []string

	add := func(raw string) {
		token := strings.TrimSpace(strings.ToLower(raw))
		if len(token) < 3 || seen[token] {
			return
		}
		seen[token] = true
		keys = append(keys, token)
	}

	for _, tag := range clip.Tags {
		for _, part := range strings.FieldsFunc(tag, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			add(part)
		}
	}

	for _, part := range strings.FieldsFunc(clip.Filename, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		add(part)
	}

	return keys
}

func extractDriveFileIDFromURL(raw string) string {
	u := strings.TrimSpace(raw)
	if u == "" {
		return ""
	}
	if i := strings.Index(u, "/file/d/"); i >= 0 {
		rest := u[i+len("/file/d/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
		return strings.TrimSpace(rest)
	}
	if i := strings.Index(u, "id="); i >= 0 {
		rest := u[i+len("id="):]
		if j := strings.Index(rest, "&"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// pickArtlistClipRoundRobin picks a clip from a list, using round-robin per keyword for variety.
func pickArtlistClipRoundRobin(term string, clips []ArtlistClip, used map[string]bool, rr map[string]int, filter func(string) bool) (*ArtlistClip, bool) {
	if len(clips) == 0 {
		return nil, false
	}
	startIdx := rr[term] % len(clips)
	for i := 0; i < len(clips); i++ {
		idx := (startIdx + i) % len(clips)
		c := clips[idx]
		if !used[c.URL] && filter(c.URL) {
			rr[term] = idx + 1
			return &c, true
		}
	}
	// Fallback: pick any not used
	for _, c := range clips {
		if !used[c.URL] && filter(c.URL) {
			return &c, true
		}
	}
	return nil, false
}

func pickFirstArtlistDynamicResult(results []clipsearch.SearchResult, stockClips map[string]stockdb.StockClipEntry, folderPaths map[string]string) *clipsearch.SearchResult {
	for _, res := range results {
		// Basic check if it's already in stockDB via driveID
		if _, ok := stockClips[res.DriveID]; ok {
			continue
		}
		return &res
	}
	return nil
}

func buildDynamicArtlistClip(res clipsearch.SearchResult, term string) ArtlistClip {
	return ArtlistClip{
		Term:     term,
		Name:     res.Filename,
		DriveID:  res.DriveID,
		URL:      res.DriveURL,
		Folder:   res.Folder,
		FolderID: res.FolderID,
	}
}

func normalizeTopicKey(topic string) string {
	t := strings.ToLower(strings.TrimSpace(topic))
	t = strings.ReplaceAll(t, " ", "")
	return t
}
