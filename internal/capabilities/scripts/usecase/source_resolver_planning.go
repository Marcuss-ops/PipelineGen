package usecase

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

type searchResolutionPlan struct {
	Query       string
	Limit       int
	SearchLimit int
	MinCoverage float64
	MinScore    float64
}

func buildSearchResolutionPlan(src scriptpkg.SourceSpec) (searchResolutionPlan, error) {
	query := strings.TrimSpace(src.Query)
	if query == "" {
		return searchResolutionPlan{}, &scriptpkg.NoSourceError{Reason: "search source requires a query"}
	}
	limit := src.MaxClips
	if limit <= 0 {
		limit = 10
	}
	searchLimit := limit
	if searchLimit < 20 {
		searchLimit = 20
	}
	minScore := 0.0
	if src.MinQualityScore != nil {
		minScore = *src.MinQualityScore
	}
	return searchResolutionPlan{Query: query, Limit: limit, SearchLimit: searchLimit, MinCoverage: src.MinCoverage, MinScore: minScore}, nil
}

func researchTopic(src scriptpkg.SourceSpec) string {
	topic := strings.TrimSpace(src.Topic)
	if topic == "" {
		topic = strings.TrimSpace(src.Query)
	}
	return topic
}

func fallbackTitle(title, query string) string {
	return textutil.FirstNonEmpty(strings.TrimSpace(title), strings.TrimSpace(query))
}
