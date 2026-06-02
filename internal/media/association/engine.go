package association

import (
	"context"
	"strings"

	"velox/go-master/internal/media/vectorstore"
)

// SegmentInput provides data for association matching.
type SegmentInput struct {
	Topic     string
	Subject   string
	Keywords  []string
	Entities  []string
	Narrative string
}

// Association defines the interface for different media sources.
type Association interface {
	Associate(ctx context.Context, input SegmentInput) ([]ScoredMatch, error)
}

// Engine orchestrates multiple associations to find the best matches.
type Engine struct {
	sources     []Association
	vectorStore *vectorstore.Service
}

func NewEngine(sources ...Association) *Engine {
	return &Engine{sources: sources}
}

// SetVectorStore injects the vector store for Qdrant hybrid search (dense + sparse BM25).
// When set, ScoreMedia uses Qdrant RRF fusion instead of ad-hoc linear+semantic scoring.
func (e *Engine) SetVectorStore(vs *vectorstore.Service) {
	e.vectorStore = vs
}

func (e *Engine) AssociateAll(ctx context.Context, input SegmentInput) []ScoredMatch {
	var allMatches []ScoredMatch
	for _, source := range e.sources {
		if source == nil {
			continue
		}
		if matches, err := source.Associate(ctx, input); err == nil {
			allMatches = append(allMatches, matches...)
		}
	}
	return deduplicateMatches(allMatches)
}

func deduplicateMatches(matches []ScoredMatch) []ScoredMatch {
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]ScoredMatch, 0, len(matches))
	for _, m := range matches {
		key := m.ClipID
		if key == "" {
			key = m.Title + "|" + m.Path + "|" + m.Link
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}

// ScoreMedia re-scores candidates using Qdrant hybrid search (dense + sparse BM25 with RRF)
// when a vector store is configured. Falls back to local ad-hoc fusion if vectorstore is nil.
//
// Qdrant path: the query text is tokenized into sparse BM25 + dense embedding, and
// Qdrant performs RRF fusion natively. Results replace the candidates entirely.
// Fallback path: local dot product fusion (40% linear, 60% semantic).
func (e *Engine) ScoreMedia(ctx context.Context, query string, queryEmb []float32, candidates []ScoredMatch) []ScoredMatch {
	// Prefer Qdrant hybrid search if available
	if e.vectorStore != nil && len(queryEmb) > 0 && query != "" {
		results, err := e.vectorStore.HybridSearch(ctx, vectorstore.HybridSearchRequest{
			QueryText:    query,
			DenseVector:  queryEmb,
			Limit:        30,
		})
		if err == nil && len(results) > 0 {
			// Convert Qdrant results to ScoredMatch
			matches := make([]ScoredMatch, 0, len(results))
			for _, r := range results {
				match := ScoredMatch{
					ClipID: r.AssetID,
					Title:  r.Name,
					Path:   r.LocalPath,
					Score:  int(r.Score * 100), // scale 0-1 → 0-100
					Source: r.Source,
					Link:   r.DriveLink,
					Reason: "qdrant hybrid search (dense + BM25 + RRF)",
				}
				if match.Score > 100 {
					match.Score = 100
				}
				matches = append(matches, match)
			}
			return matches
		}
		// On error, fall through to ad-hoc scoring
	}

	// Fallback: ad-hoc linear+semantic fusion (legacy path for when Qdrant is unavailable)
	return e.scoreMediaLocal(query, queryEmb, candidates)
}

// scoreMediaLocal is the original ad-hoc hybrid scoring (fallback when Qdrant is unavailable).
func (e *Engine) scoreMediaLocal(query string, queryEmb []float32, candidates []ScoredMatch) []ScoredMatch {
	var scoredCandidates []ScoredMatch
	queryLower := strings.ToLower(query)

	for _, c := range candidates {
		linear := float64(c.Score)
		semantic := float64(0)

		if len(queryEmb) > 0 && len(c.Embedding) > 0 {
			semantic = DotProduct(queryEmb, c.Embedding) * 100
		} else if len(c.Embedding) == 0 {
			semantic = linear
		}

		final := linear*0.4 + semantic*0.6

		if len(queryLower) > 3 && (strings.Contains(strings.ToLower(c.Title), queryLower) || strings.Contains(strings.ToLower(c.Path), queryLower)) {
			final += 15.0
		}

		c.Score = int(final)
		if c.Score > 100 {
			c.Score = 100
		}

		if c.Score >= 15 {
			scoredCandidates = append(scoredCandidates, c)
		}
	}

	return scoredCandidates
}

// DotProduct calcola il prodotto scalare tra due vettori. 
// Se i vettori sono già stati normalizzati, questo equivale alla Cosine Similarity.
func DotProduct(a, b []float32) float64 {
	var sum float64
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		sum += float64(a[i] * b[i])
	}
	return sum
}
