// Package semantic is the ownership boundary for deterministic semantic
// salience scoring of already-segmented script sentences. It consumes
// precomputed sentence embeddings; model inference and sentence segmentation
// belong to callers.
//
// Phrase text must still be grounded against canonical narration and speech
// timestamps must still come from the canonical word-level timing artifact.
// Scores only enrich ranking: callers retain the existing overlay budget and
// certified renderer/motion catalog.
package semantic

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	centralityWeight = 0.55
	noveltyWeight    = 0.45
	localContextSize = 3
	graphNeighbors   = 6
	pageRankDamping  = 0.85
	pageRankSteps    = 50
	pageRankEpsilon  = 1e-8
)

var ErrInvalidSentence = errors.New("semantic scoring: invalid sentence")

// Sentence is one already-segmented narration sentence. Embedding is supplied
// by the caller so this package has no model-runtime dependency.
type Sentence struct {
	Text      string    `json:"text"`
	StartUS   int64     `json:"start_us"`
	EndUS     int64     `json:"end_us"`
	Embedding []float32 `json:"embedding"`
}

// Impact contains normalized TextRank centrality, local novelty and their
// weighted V1 impact. Values are bounded to [0, 1].
type Impact struct {
	SentenceIndex int     `json:"index"`
	Text          string  `json:"text"`
	StartUS       int64   `json:"start_us"`
	EndUS         int64   `json:"end_us"`
	Centrality    float64 `json:"centrality"`
	Novelty       float64 `json:"novelty"`
	Score         float64 `json:"impact"`
}

// StageTimings reports pure ranking-stage work; it excludes embedding-model
// initialization, inference, process startup and caller-side JSON conversion.
type StageTimings struct {
	NormalizeMS  float64 `json:"normalize_ms"`
	SimilarityMS float64 `json:"similarity_ms"`
	PageRankMS   float64 `json:"pagerank_ms"`
	NoveltyMS    float64 `json:"novelty_ms"`
	TotalMS      float64 `json:"total_ms"`
}

// ScoreSentences combines weighted semantic TextRank centrality and novelty
// against the three previous sentences. Inputs can be normalized or raw,
// non-zero vectors; results retain source order.
func ScoreSentences(sentences []Sentence) ([]Impact, error) {
	impacts, _, err := ScoreSentencesWithTiming(sentences)
	return impacts, err
}

// ScoreSentencesWithTiming is ScoreSentences plus phase timings used by the
// offline benchmark. The scorer does not include any embedding inference.
func ScoreSentencesWithTiming(sentences []Sentence) ([]Impact, StageTimings, error) {
	var timings StageTimings
	if len(sentences) == 0 {
		return nil, timings, nil
	}
	totalStart := time.Now()
	dimension := len(sentences[0].Embedding)
	if dimension == 0 {
		return nil, timings, invalidSentence(0, "embedding is empty")
	}

	phaseStart := time.Now()
	vectors := make([][]float64, len(sentences))
	hasTimedSentence, hasUntimedSentence := false, false
	for _, sentence := range sentences {
		if sentence.StartUS != 0 || sentence.EndUS != 0 {
			hasTimedSentence = true
		} else {
			hasUntimedSentence = true
		}
	}
	if hasTimedSentence && hasUntimedSentence {
		return nil, timings, invalidSentence(0, "timing must be present for every sentence or none")
	}
	timed := hasTimedSentence
	var previousTimedStart int64
	havePreviousTimedSentence := false
	for i, sentence := range sentences {
		if strings.TrimSpace(sentence.Text) == "" {
			return nil, timings, invalidSentence(i, "text is empty")
		}
		if timed && (sentence.StartUS < 0 || sentence.EndUS <= sentence.StartUS) {
			return nil, timings, invalidSentence(i, "timestamp interval is invalid")
		}
		if timed {
			if havePreviousTimedSentence && sentence.StartUS < previousTimedStart {
				return nil, timings, invalidSentence(i, "sentences are not ordered by start time")
			}
			previousTimedStart, havePreviousTimedSentence = sentence.StartUS, true
		}
		if len(sentence.Embedding) != dimension {
			return nil, timings, invalidSentence(i, "embedding dimension differs from sentence 0")
		}
		vector, err := normalizedVector(sentence.Embedding, i)
		if err != nil {
			return nil, timings, err
		}
		vectors[i] = vector
	}
	timings.NormalizeMS = elapsedMS(phaseStart)

	phaseStart = time.Now()
	similarities := cosineMatrix(vectors)
	timings.SimilarityMS = elapsedMS(phaseStart)

	phaseStart = time.Now()
	centralities := percentileScale(pageRank(topKGraph(similarities, graphNeighbors)))
	timings.PageRankMS = elapsedMS(phaseStart)

	phaseStart = time.Now()
	novelties := make([]float64, len(sentences))
	for i := range sentences {
		novelties[i] = localNovelty(similarities, i, localContextSize)
	}
	novelties = percentileScale(novelties)
	timings.NoveltyMS = elapsedMS(phaseStart)

	out := make([]Impact, len(sentences))
	for i, sentence := range sentences {
		score := centralityWeight*centralities[i] + noveltyWeight*novelties[i]
		out[i] = Impact{
			SentenceIndex: i,
			Text:          strings.TrimSpace(sentence.Text),
			StartUS:       sentence.StartUS,
			EndUS:         sentence.EndUS,
			Centrality:    centralities[i],
			Novelty:       novelties[i],
			Score:         clamp01(score),
		}
	}
	timings.TotalMS = elapsedMS(totalStart)
	return out, timings, nil
}

func elapsedMS(start time.Time) float64 { return float64(time.Since(start).Nanoseconds()) / 1e6 }

func invalidSentence(index int, reason string) error {
	return fmt.Errorf("%w %d: %s", ErrInvalidSentence, index, reason)
}

func normalizedVector(input []float32, index int) ([]float64, error) {
	vector := make([]float64, len(input))
	norm := 0.0
	for i, value := range input {
		v := float64(value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, invalidSentence(index, "embedding contains a non-finite value")
		}
		vector[i] = v
		norm += v * v
	}
	if norm == 0 {
		return nil, invalidSentence(index, "embedding has zero norm")
	}
	inverseNorm := 1 / math.Sqrt(norm)
	for i := range vector {
		vector[i] *= inverseNorm
	}
	return vector, nil
}

func cosineMatrix(vectors [][]float64) [][]float64 {
	matrix := make([][]float64, len(vectors))
	for i := range vectors {
		matrix[i] = make([]float64, len(vectors))
		for j := 0; j < i; j++ {
			similarity := 0.0
			for k, value := range vectors[i] {
				similarity += value * vectors[j][k]
			}
			matrix[i][j], matrix[j][i] = clamp01(similarity), clamp01(similarity)
		}
	}
	return matrix
}

type graphNeighbor struct {
	index  int
	weight float64
}

// topKGraph retains each sentence's k strongest positive non-self cosine
// edges and symmetrizes their union into adjacency lists. PageRank traverses
// only those edges, not an N×N matrix. Equal weights break ties by sentence
// index.
func topKGraph(similarities [][]float64, neighbors int) [][]graphNeighbor {
	n := len(similarities)
	adjacency := make([][]graphNeighbor, n)
	if n < 2 || neighbors <= 0 {
		return adjacency
	}
	if neighbors > n-1 {
		neighbors = n - 1
	}
	for i := 0; i < n; i++ {
		indices := make([]int, 0, n-1)
		for j := 0; j < n; j++ {
			if i != j {
				indices = append(indices, j)
			}
		}
		sort.SliceStable(indices, func(a, b int) bool {
			left, right := similarities[i][indices[a]], similarities[i][indices[b]]
			if left != right {
				return left > right
			}
			return indices[a] < indices[b]
		})
		for _, j := range indices[:neighbors] {
			weight := similarities[i][j]
			if weight <= 0 {
				continue
			}
			if !hasNeighbor(adjacency[i], j) {
				adjacency[i] = append(adjacency[i], graphNeighbor{index: j, weight: weight})
			}
			if !hasNeighbor(adjacency[j], i) {
				adjacency[j] = append(adjacency[j], graphNeighbor{index: i, weight: weight})
			}
		}
	}
	for i := range adjacency {
		sort.Slice(adjacency[i], func(a, b int) bool { return adjacency[i][a].index < adjacency[i][b].index })
	}
	return adjacency
}

func hasNeighbor(neighbors []graphNeighbor, index int) bool {
	for _, neighbor := range neighbors {
		if neighbor.index == index {
			return true
		}
	}
	return false
}

// pageRank computes weighted centrality on the undirected, sparse cosine graph.
// Isolated nodes use the uniform dangling-node transition.
func pageRank(adjacency [][]graphNeighbor) []float64 {
	n := len(adjacency)
	if n == 0 {
		return nil
	}
	rank := make([]float64, n)
	for i := range rank {
		rank[i] = 1 / float64(n)
	}
	if n == 1 {
		return rank
	}
	degree := make([]float64, n)
	for i := range adjacency {
		for _, neighbor := range adjacency[i] {
			degree[i] += neighbor.weight
		}
	}
	for step := 0; step < pageRankSteps; step++ {
		next := make([]float64, n)
		for i := range next {
			next[i] = (1 - pageRankDamping) / float64(n)
		}
		danglingMass := 0.0
		for i := range rank {
			if degree[i] == 0 {
				danglingMass += rank[i]
				continue
			}
			for _, neighbor := range adjacency[i] {
				next[neighbor.index] += pageRankDamping * rank[i] * neighbor.weight / degree[i]
			}
		}
		share := pageRankDamping * danglingMass / float64(n)
		delta := 0.0
		for i := range next {
			next[i] += share
			delta += math.Abs(next[i] - rank[i])
		}
		rank = next
		if delta < pageRankEpsilon {
			break
		}
	}
	return rank
}

func localNovelty(similarities [][]float64, index, contextSize int) float64 {
	if index == 0 {
		return 0
	}
	start := index - contextSize
	if start < 0 {
		start = 0
	}
	mostSimilar := 0.0
	for previous := start; previous < index; previous++ {
		if similarities[index][previous] > mostSimilar {
			mostSimilar = similarities[index][previous]
		}
	}
	return clamp01(1 - mostSimilar)
}

// percentileScale maps p10..p90 linearly to [0,1], clipping outliers. A
// constant signal maps to zero rather than fabricating salience.
func percentileScale(values []float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	low, high := percentile(ordered, 0.10), percentile(ordered, 0.90)
	out := make([]float64, len(values))
	if high-low < 1e-9 {
		return out
	}
	for i, value := range values {
		out[i] = clamp01((value - low) / (high - low))
	}
	return out
}

func percentile(sorted []float64, quantile float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	position := quantile * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	fraction := position - float64(lower)
	return sorted[lower]*(1-fraction) + sorted[upper]*fraction
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
