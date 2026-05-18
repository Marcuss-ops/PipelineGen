package association

import (
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/pkg/textutil"
)

// scoreFolderCandidates calcola lo score per una lista di cartelle candidate.
func scoreFolderCandidates(database, source string, folders []FolderCandidate, terms []string, focusTexts ...string) []Candidate {
	candidates := make([]Candidate, 0, len(folders))
	focusKeys := make([]string, 0, len(focusTexts))
	for _, focusText := range focusTexts {
		if key := normalizeKey(focusText); key != "" {
			focusKeys = append(focusKeys, key)
		}
	}
	for _, folder := range folders {
		name := strings.TrimSpace(folder.Name)
		path := strings.TrimSpace(folder.Path)
		link := strings.TrimSpace(folder.Link)
		if name == "" && path == "" && link == "" {
			continue
		}

		candidateText := strings.ToLower(strings.Join([]string{name, path, link}, " "))
		score := matching.ScoreText(candidateText, terms)
		if score == 0 {
			continue
		}
		if name != "" {
			slug := strings.ToLower(strings.ReplaceAll(name, " ", "-"))
			for _, term := range terms {
				termSlug := strings.ToLower(strings.ReplaceAll(term, " ", "-"))
				if strings.Contains(slug, termSlug) || strings.Contains(termSlug, slug) {
					score += 15
					break
				}
			}
		}
		if source == "stock_folder" {
			folderKey := normalizeKey(name)
			pathKey := normalizeKey(path)
			focusTokenCount := 0
			for _, focusKey := range focusKeys {
				if count := len(textutil.Tokenize(focusKey)); count > 0 && (focusTokenCount == 0 || count < focusTokenCount) {
					focusTokenCount = count
				}
			}
			for _, focusKey := range focusKeys {
				if focusKey == "" {
					continue
				}
				if folderKey == focusKey || pathKey == focusKey {
					score += 60
					break
				}
				if strings.HasSuffix(pathKey, "/"+focusKey) {
					score += 35
					break
				}
			}
			if focusTokenCount > 0 {
				candidateTokenCount := len(textutil.Tokenize(name + " " + path))
				if candidateTokenCount >= focusTokenCount+4 {
					continue
				}
				if candidateTokenCount > focusTokenCount {
					score -= (candidateTokenCount - focusTokenCount) / 2
				}
			}
		}
		if score > 100 {
			score = 100
		}

		candidates = append(candidates, Candidate{
			Database: database,
			Source:   source,
			Name:     name,
			Path:     path,
			FolderID: folder.FolderID,
			Link:     link,
			Score:    score,
			Reason:   "token overlap on segment subject/keywords/entities",
		})
	}
	return candidates
}
