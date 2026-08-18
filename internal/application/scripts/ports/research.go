package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

type WebSearchHit struct {
	Title, URL, Content string
}

type WebSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]WebSearchHit, error)
}

// WebSearchProvider is a named search backend. Unlike WebSearcher (the
// minimal contract consumed by the research resolver), WebSearchProvider
// carries a provider name for logging and diagnostics. The
// MultiWebSearcher and ResearchSearchCoordinator use this interface to
// orchestrate fallback across SearXNG, DuckDuckGo, and future backends.
//
// The contract deliberately has no health-check method: a health probe
// is a second provider-specific API that says nothing about whether a
// real search will work. Fallback is driven by search errors or
// insufficient results, which are observable through Search.
type WebSearchProvider interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]WebSearchHit, error)
}

type WebPage struct {
	URL, Title, Publisher, PublishedAt, Text string
}

type WebPageFetcher interface {
	Fetch(ctx context.Context, rawURL string, maxChars int) (WebPage, error)
}

type ResearchCandidateRankingInput struct {
	CandidateID string
	Label       string
	Sources     []scriptpkg.ResearchWebSource
	Claims      []scriptpkg.ResearchClaim
}

type ResearchCandidateRanking struct {
	CandidateID string
	Rank        int
	Score       float64
	Rationale   string
}

type ResearchRanker interface {
	Rank(context.Context, string, []ResearchCandidateRankingInput) ([]ResearchCandidateRanking, error)
}

type ResearchRankerFunc func(context.Context, string, []ResearchCandidateRankingInput) ([]ResearchCandidateRanking, error)

func (f ResearchRankerFunc) Rank(ctx context.Context, topic string, inputs []ResearchCandidateRankingInput) ([]ResearchCandidateRanking, error) {
	return f(ctx, topic, inputs)
}
