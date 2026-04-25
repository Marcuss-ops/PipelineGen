package script

import (
	"strings"

	"velox/go-master/internal/ml/ollama/types"
)

func buildStockMatchingSection(dataDir string, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis) ScriptSection {
	terms := collectTopicTerms(req.Topic)
	clips, err := loadStockCatalog(dataDir)
	if err != nil {
		return ScriptSection{
			Title: " Stock Matching",
			Body:  "Stock catalog unavailable.",
		}
	}

	matches := make([]scoredMatch, 0, len(clips))
	for _, clip := range clips {
		if strings.TrimSpace(clip.MediaType) != "stock" {
			continue
		}
		title := strings.TrimSpace(clip.DisplayName())
		if title == "" {
			continue
		}
		candidate := strings.ToLower(strings.Join([]string{
			title,
			clip.Filename,
			clip.FullPath,
			clip.FolderPath,
			clip.Group,
			clip.MediaType,
			strings.Join(clip.Tags, " "),
			clip.DriveLink,
		}, " "))
		score := scoreText(candidate, terms)
		if score == 0 {
			continue
		}
		matches = append(matches, scoredMatch{
			Title:   title,
			Path:    clip.StockPath(),
			Score:   score,
			Source:  "stock catalog",
			Link:    clip.PickLink(),
			Details: strings.Join(clip.Tags, ", "),
		})
	}

	matches = sortTopMatches(matches, 4)
	if len(matches) == 0 {
		return ScriptSection{
			Title: " Stock Matching",
			Body:  "None",
		}
	}

	return ScriptSection{
		Title: " Stock Matching",
		Body:  renderMatches(matches),
	}
}
