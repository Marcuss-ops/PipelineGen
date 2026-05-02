package artlist

import "context"

type SearchInput struct {
	Query string
	Limit int
}

type ClipCandidate struct {
	ID       string
	Title    string
	URL      string
	Duration float64
	Tags     []string
}

type ArtlistAdapter interface {
	Search(ctx context.Context, input SearchInput) ([]ClipCandidate, error)
}
