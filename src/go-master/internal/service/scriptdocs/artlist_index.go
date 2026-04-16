package scriptdocs

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	return &idx, nil
}

// Search searches the ArtlistIndex for clips matching the given terms.
func (idx *ArtlistIndex) Search(terms []string, maxResults int) []ArtlistClip {
	var results []ArtlistClip
	used := make(map[string]bool)

	for _, clip := range idx.Clips {
		if len(results) >= maxResults {
			break
		}
		if used[clip.URL] {
			continue
		}

		matched := false
		for _, term := range terms {
			t := strings.ToLower(term)
			if strings.Contains(strings.ToLower(clip.Name), t) || strings.Contains(strings.ToLower(clip.Term), t) {
				matched = true
				break
			}
		}

		if matched {
			results = append(results, clip)
			used[clip.URL] = true
		}
	}

	return results
}
