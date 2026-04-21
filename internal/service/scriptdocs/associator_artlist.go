package scriptdocs

import (
	"strings"

	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
	"math/rand"
)

func isArtlistPathLike(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return false
	}
	return strings.Contains(s, "/artlist") || strings.Contains(s, "stock/artlist") || strings.Contains(s, "stock cartella/artlist")
}

func isArtlistFilenameLike(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return false
	}
	return strings.Contains(s, "artlist")
}

func isArtlistPreviewLike(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return false
	}
	return strings.Contains(s, "preview")
}

func isPreviewArtlistClip(c ArtlistClip) bool {
	return isArtlistPreviewLike(c.Name) || isArtlistPreviewLike(c.URL) || isArtlistPreviewLike(c.Folder)
}

func isArtlistStockClipEntry(clip stockdb.StockClipEntry, folderPathByID map[string]string) bool {
	if strings.EqualFold(strings.TrimSpace(clip.Source), "artlist") {
		return true
	}
	if isArtlistFilenameLike(clip.Filename) {
		return true
	}
	if folderPath, ok := folderPathByID[clip.FolderID]; ok && isArtlistPathLike(folderPath) {
		return true
	}
	return false
}

func isArtlistDynamicResult(dc clipsearch.SearchResult, stockClipByID map[string]stockdb.StockClipEntry, folderPathByID map[string]string) bool {
	if isArtlistFilenameLike(dc.Filename) || isArtlistPathLike(dc.Folder) {
		return true
	}
	if clip, ok := stockClipByID[dc.DriveID]; ok {
		return isArtlistStockClipEntry(clip, folderPathByID)
	}
	return false
}

func pickRandomArtlistClip(
	rng *rand.Rand,
	clips []ArtlistClip,
	usedClipIDs map[string]bool,
	isValid func(string) bool,
) (*ArtlistClip, bool) {
	if len(clips) == 0 {
		return nil, false
	}
	candidateIdx := make([]int, 0, len(clips))
	for i := range clips {
		if isPreviewArtlistClip(clips[i]) || usedClipIDs[clips[i].URL] || !isValid(clips[i].URL) {
			continue
		}
		candidateIdx = append(candidateIdx, i)
	}
	if len(candidateIdx) == 0 {
		return nil, false
	}
	clip := clips[candidateIdx[rng.Intn(len(candidateIdx))]]
	return &clip, true
}

func pickFirstArtlistDynamicResult(
	results []clipsearch.SearchResult,
	stockClipByID map[string]stockdb.StockClipEntry,
	folderPathByID map[string]string,
) *clipsearch.SearchResult {
	for i := range results {
		if isArtlistDynamicResult(results[i], stockClipByID, folderPathByID) {
			return &results[i]
		}
	}
	return nil
}

func (s *ScriptDocService) artlistClipsForTerm(term string) []ArtlistClip {
	if s.artlistIndex == nil {
		return nil
	}
	normalized := normalizeKeyword(term)
	if normalized == "" {
		return nil
	}
	if clips, ok := s.artlistIndex.ByTerm[normalized]; ok && len(clips) > 0 {
		return clips
	}
	if clips, ok := s.artlistIndex.ByTerm[term]; ok && len(clips) > 0 {
		return clips
	}
	return nil
}

func buildDynamicArtlistClip(dc clipsearch.SearchResult, fallbackTerm string) ArtlistClip {
	term := normalizeKeyword(dc.Keyword)
	if term == "" {
		term = normalizeKeyword(fallbackTerm)
	}
	if term == "" {
		term = "people"
	}
	return ArtlistClip{
		Name:   dc.Filename,
		Term:   term,
		URL:    dc.DriveURL,
		Folder: "Stock/Artlist/" + term,
	}
}
