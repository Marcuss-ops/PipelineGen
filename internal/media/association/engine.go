package association

import (
	"context"
	"strings"
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
	sources []Association
}

func NewEngine(sources ...Association) *Engine {
	return &Engine{sources: sources}
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
	return allMatches
}

// ScoreMedia ordina e valuta i candidati usando un approccio Hybrid Search (Lineare + Semantico).
// Assumiamo che i candidati abbiano già un punteggio Lineare pre-calcolato in c.Score (0-100).
func (e *Engine) ScoreMedia(query string, queryEmb []float32, candidates []ScoredMatch) []ScoredMatch {
	var scoredCandidates []ScoredMatch
	
	// Normalizza la query per i check lineari
	queryLower := strings.ToLower(query)

	for _, c := range candidates {
		linear := float64(c.Score) // Punteggio lineare (es. calcolato da matching.ScoreText o dal DB)
		semantic := float64(0)
		
		if len(queryEmb) > 0 && len(c.Embedding) > 0 {
			// DotProduct tra due vettori normalizzati dà la Cosine Similarity
			semantic = DotProduct(queryEmb, c.Embedding) * 100 // scalato a 0-100
		} else if len(c.Embedding) == 0 {
			// Se manca l'embedding, usiamo solo il lineare
			semantic = linear 
		}

		// Fusione (40% Lineare, 60% Semantico)
		final := linear*0.4 + semantic*0.6

		// Bonus esatto: Se il nome file o path contiene la query (e la query non è cortissima)
		if len(queryLower) > 3 && (strings.Contains(strings.ToLower(c.Title), queryLower) || strings.Contains(strings.ToLower(c.Path), queryLower)) {
			final += 15.0
		}

		c.Score = int(final)
		if c.Score > 100 {
			c.Score = 100
		}
		
		// Filtro base: accettiamo solo se il punteggio supera una certa soglia
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
