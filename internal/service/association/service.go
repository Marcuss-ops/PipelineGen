package association

import (
	"context"
	"sort"
	"strings"

	"velox/go-master/internal/matching"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/sliceutil"
)

type Service struct {
	dataDir        string
	nodeScraperDir string
	stockRepo      *clips.Repository
	artlistRepo    *clips.Repository
	clipsRepo      *clips.Repository
}

func NewService(dataDir, nodeScraperDir string, stockRepo, artlistRepo, clipsRepo *clips.Repository) *Service {
	return &Service{
		dataDir:        dataDir,
		nodeScraperDir: nodeScraperDir,
		stockRepo:      stockRepo,
		artlistRepo:    artlistRepo,
		clipsRepo:      clipsRepo,
	}
}

func (s *Service) BuildCandidates(ctx context.Context, req CandidatesRequest) (*CandidatesResponse, error) {
	req.Normalize()

	// 1. Direct match logic (simplified for now, ideally moved to a provider)
	
	terms := s.collectTerms(req)
	rawCandidates := make([]Candidate, 0)

	// Here we would call the different providers.
	// For now, this is a placeholder for the refactored logic.

	deduped := s.dedupe(rawCandidates)
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Score == deduped[j].Score {
			return deduped[i].Name < deduped[j].Name
		}
		return deduped[i].Score > deduped[j].Score
	})
	if req.TopK > 0 && len(deduped) > req.TopK {
		deduped = deduped[:req.TopK]
	}

	return &CandidatesResponse{
		OK:         true,
		Topic:      req.Topic,
		SegmentKey: req.SegmentKey,
		Timestamp:  req.Timestamp,
		Subject:    req.Subject,
		TopK:       req.TopK,
		Candidates: deduped,
	}, nil
}

func (s *Service) collectTerms(req CandidatesRequest) []string {
	terms := make([]string, 0)
	seen := make(map[string]struct{})
	add := func(text string) {
		for _, term := range matching.Tokenize(text) {
			term = strings.TrimSpace(term)
			if term == "" || len(term) < 3 || matching.IsStopWord(term) {
				continue
			}
			key := strings.ToLower(term)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			terms = append(terms, term)
		}
	}

	add(req.Topic)
	add(req.Subject)
	add(req.Narrative)
	add(strings.Join(req.Keywords, " "))
	add(strings.Join(req.Entities, " "))

	return terms
}

func (s *Service) dedupe(candidates []Candidate) []Candidate {
	if len(candidates) == 0 {
		return nil
	}
	seen := make(map[string]Candidate, len(candidates))
	for _, candidate := range candidates {
		key := strings.ToLower(strings.TrimSpace(candidate.Database) + "|" + strings.TrimSpace(candidate.Name) + "|" + strings.TrimSpace(candidate.Path) + "|" + strings.TrimSpace(candidate.Link))
		if existing, ok := seen[key]; ok {
			if candidate.Score > existing.Score {
				seen[key] = candidate
			}
			continue
		}
		seen[key] = candidate
	}

	out := make([]Candidate, 0, len(seen))
	for _, candidate := range seen {
		out = append(out, candidate)
	}
	return out
}
