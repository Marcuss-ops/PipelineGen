package script

import (
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/match"
)

func buildStockMatchingSection(repo *catalog.Repository, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis) ScriptSection {
	terms := collectTopicTerms(req.Topic)
	clips, err := repo.LoadStockCatalog()
	if err != nil {
		return ScriptSection{
			Title: "Stock Matching",
			Body:  "Stock catalog unavailable.",
		}
	}

	matches := make([]association.ScoredMatch, 0, len(clips))
	for _, clip := range clips {
		if strings.TrimSpace(string(clip.MediaType)) != "stock" {
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
			string(clip.MediaType),
			strings.Join(clip.Tags, " "),
			clip.DriveLink,
		}, " "))
		score := matching.ScoreText(candidate, terms)
		if score == 0 {
			continue
		}
		matches = append(matches, association.ScoredMatch{
			Title:   title,
			Path:    clip.StockPath(),
			Score:   score,
			Source:  "stock catalog",
			Link:    clip.PickLink(),
			Details: strings.Join(clip.Tags, ", "),
		})
	}

	matches = match.SortTopMatches(matches, 4)
	if len(matches) == 0 {
		return ScriptSection{
			Title: "Stock Matching",
			Body:  "None",
		}
	}

	return ScriptSection{
		Title: "Stock Matching",
		Body:  match.RenderMatches(matches),
	}
}
