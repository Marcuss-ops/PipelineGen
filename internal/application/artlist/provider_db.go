package artlist

import (
	"context"
	"strings"
)

// DBSearcher searches the local database for indexed clips.
// It implements Searcher so it can plug directly into SearcherFallbackChain.
type DBSearcher struct {
	store AssetStore
}

// NewDBSearcher creates a new DBSearcher.
func NewDBSearcher(store AssetStore) *DBSearcher {
	return &DBSearcher{store: store}
}

func (s *DBSearcher) Search(ctx context.Context, req SearchRequest) ([]Candidate, error) {
	if s.store == nil {
		return nil, nil
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	keywords := strings.Fields(term)
	dbClips, err := s.store.SearchByTerms(ctx, "artlist", keywords, limit)
	if err != nil {
		return nil, err
	}
	if len(dbClips) == 0 {
		return nil, nil
	}

	candidates := make([]Candidate, 0, len(dbClips))
	for _, clip := range dbClips {
		candidates = append(candidates, Candidate{
			ID:         clip.ID,
			Title:      clip.Name,
			SourceRef:  clip.ExternalURL(),
			PageURL:    clip.GetMetadataString("external_url"),
			SourceName: "database",
		})
	}
	return candidates, nil
}
