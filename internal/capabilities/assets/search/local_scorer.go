package search

import "context"

// LocalScoreQuery carries the query signals used to rank PostgreSQL catalog
// rows after retrieval.
type LocalScoreQuery struct {
	Text          string   `json:"text"`
	Tags          []string `json:"tags"`
	Source        string   `json:"source"`
	MinDurationMS int      `json:"min_duration_ms"`
}

// LocalScoreCandidate is one catalog row sent to the batch ranker.
type LocalScoreCandidate struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Tags       []string `json:"tags"`
	Source     string   `json:"source"`
	DurationMS int64    `json:"duration_ms"`
}

// LocalScoreResult binds the computed relevance score to its catalog row.
type LocalScoreResult struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// LocalScorer scores a complete local catalog result batch in one call.
type LocalScorer interface {
	ScoreLocal(ctx context.Context, query LocalScoreQuery, candidates []LocalScoreCandidate) ([]LocalScoreResult, error)
}
