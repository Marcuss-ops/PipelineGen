package association

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/core/scoring"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
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
		score := scoring.ScoreText(candidateText, terms)
		if score == 0 {
			continue
		}

		// Penalize single-word folder names: they are too generic and produce
		// false positive matches (e.g., folder "edifice" matching a documentary
		// about Floyd Mayweather's metaphorical "edifice"). Single-word folders
		// need a much stronger signal.
		folderWordCount := len(strings.Fields(name))
		if folderWordCount == 1 {
			// For single-word folders (both artlist and stock), divide the
			// base score to make them much less likely to rank above
			// multi-word, specific folders.
			score = score / 3
			if score < 1 {
				continue
			}
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
