package scriptdocs

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// LoadArtlistIndex loads the Artlist clip index from JSON file.
func LoadArtlistIndex(path string) (*ArtlistIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read Artlist index: %w", err)
	}

	var idx ArtlistIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("failed to parse Artlist index: %w", err)
	}

	// Build ByTerm map
	idx.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range idx.Clips {
		normalized := normalizeSearchToken(clip.Term)
		if normalized != "" {
			idx.ByTerm[normalized] = append(idx.ByTerm[normalized], clip)
		}
		// Keep raw key for backwards compatibility with any pre-normalized lookups.
		raw := strings.TrimSpace(clip.Term)
		if raw != "" && raw != normalized {
			idx.ByTerm[raw] = append(idx.ByTerm[raw], clip)
		}
	}

	return &idx, nil
}

// Search searches the ArtlistIndex for clips matching the given terms.
func (idx *ArtlistIndex) Search(terms []string, maxResults int) []ArtlistClip {
	type scoredClip struct {
		clip  ArtlistClip
		score int
	}

	cleanTerms := make([]string, 0, len(terms))
	seenTerms := make(map[string]bool)
	for _, term := range terms {
		t := normalizeSearchToken(term)
		if len(t) < 3 || seenTerms[t] {
			continue
		}
		seenTerms[t] = true
		cleanTerms = append(cleanTerms, t)
	}
	if len(cleanTerms) == 0 || maxResults <= 0 {
		return nil
	}

	scored := make([]scoredClip, 0, len(idx.Clips))
	for _, clip := range idx.Clips {
		clipName := strings.ToLower(clip.Name)
		clipTerm := strings.ToLower(clip.Term)
		clipFolder := strings.ToLower(clip.Folder)

		score := 0
		for _, t := range cleanTerms {
			if clipTerm == t {
				score += 24
			}
			if strings.Contains(" "+clipTerm+" ", " "+t+" ") {
				score += 12
			}
			if strings.Contains(" "+clipName+" ", " "+t+" ") {
				score += 10
			}
			if strings.Contains(" "+clipFolder+" ", " "+t+" ") {
				score += 6
			}
			if strings.Contains(clipName, t) {
				score += 4
			}
		}

		if score > 0 {
			scored = append(scored, scoredClip{clip: clip, score: score})
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if len(scored[i].clip.Term) != len(scored[j].clip.Term) {
			return len(scored[i].clip.Term) > len(scored[j].clip.Term)
		}
		return strings.ToLower(scored[i].clip.Name) < strings.ToLower(scored[j].clip.Name)
	})

	results := make([]ArtlistClip, 0, maxResults)
	used := make(map[string]bool)
	for _, sc := range scored {
		if len(results) >= maxResults {
			break
		}
		if used[sc.clip.URL] {
			continue
		}
		used[sc.clip.URL] = true
		results = append(results, sc.clip)
	}

	return results
}

func normalizeSearchToken(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
