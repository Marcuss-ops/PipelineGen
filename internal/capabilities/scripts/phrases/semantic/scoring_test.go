package semantic

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestScoreSentencesRanksSemanticClusterAndNovelty(t *testing.T) {
	sentences := []Sentence{
		{Text: "The company introduced a new device.", StartUS: 0, EndUS: 1_000_000, Embedding: []float32{1, 0, 0}},
		{Text: "The device brings a faster processor.", StartUS: 1_100_000, EndUS: 2_000_000, Embedding: []float32{0.98, 0.2, 0}},
		{Text: "The processor improves daily performance.", StartUS: 2_100_000, EndUS: 3_000_000, Embedding: []float32{0.95, 0.3, 0}},
		{Text: "But the company removed the charging port.", StartUS: 3_100_000, EndUS: 4_000_000, Embedding: []float32{0, 0, 1}},
	}

	got, timings, err := ScoreSentencesWithTiming(sentences)
	if err != nil {
		t.Fatalf("ScoreSentencesWithTiming() error = %v", err)
	}
	if len(got) != len(sentences) {
		t.Fatalf("ScoreSentences() returned %d items, want %d", len(got), len(sentences))
	}
	if got[1].Centrality <= got[3].Centrality {
		t.Fatalf("cluster centrality %.3f should exceed outlier centrality %.3f", got[1].Centrality, got[3].Centrality)
	}
	if got[3].Novelty <= got[2].Novelty {
		t.Fatalf("trajectory change novelty %.3f should exceed prior sentence novelty %.3f", got[3].Novelty, got[2].Novelty)
	}
	for i, impact := range got {
		if impact.SentenceIndex != i || impact.Text != sentences[i].Text {
			t.Fatalf("impact[%d] lost source order or text: %+v", i, impact)
		}
		wantScore := centralityWeight*impact.Centrality + noveltyWeight*impact.Novelty
		if math.Abs(impact.Score-wantScore) > 1e-12 {
			t.Errorf("impact[%d].Score = %.12f, want 0.55*centrality + 0.45*novelty = %.12f", i, impact.Score, wantScore)
		}
		for name, value := range map[string]float64{"centrality": impact.Centrality, "novelty": impact.Novelty, "score": impact.Score} {
			if math.IsNaN(value) || value < 0 || value > 1 {
				t.Errorf("impact[%d].%s = %v, want finite score in [0,1]", i, name, value)
			}
		}
	}
	if timings.TotalMS < timings.NormalizeMS || timings.TotalMS < timings.SimilarityMS || timings.TotalMS < timings.PageRankMS || timings.TotalMS < timings.NoveltyMS {
		t.Fatalf("phase timings exceed total: %+v", timings)
	}
	repeated, err := ScoreSentences(sentences)
	if err != nil {
		t.Fatalf("repeated ScoreSentences() error = %v", err)
	}
	for i := range got {
		if got[i].Score != repeated[i].Score || got[i].Centrality != repeated[i].Centrality || got[i].Novelty != repeated[i].Novelty {
			t.Fatalf("scoring is nondeterministic at index %d: first=%+v second=%+v", i, got[i], repeated[i])
		}
	}
}

func TestTopKGraphIsSparseSymmetricAndDeterministic(t *testing.T) {
	similarities := [][]float64{
		{1, .9, .8, .1},
		{.9, 1, .7, .6},
		{.8, .7, 1, .5},
		{.1, .6, .5, 1},
	}
	got := topKGraph(similarities, 1)
	want := [][]graphNeighbor{
		{{index: 1, weight: .9}, {index: 2, weight: .8}},
		{{index: 0, weight: .9}, {index: 3, weight: .6}},
		{{index: 0, weight: .8}},
		{{index: 1, weight: .6}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topKGraph() = %v, want sparse top-1 union %v", got, want)
	}
	for i := range got {
		for _, edge := range got[i] {
			if !containsNeighbor(got[edge.index], i, edge.weight) {
				t.Fatalf("edge %d->%d is not symmetrized: %v", i, edge.index, got)
			}
		}
	}
}

func containsNeighbor(neighbors []graphNeighbor, index int, weight float64) bool {
	for _, neighbor := range neighbors {
		if neighbor.index == index && neighbor.weight == weight {
			return true
		}
	}
	return false
}

func TestPageRankNormalizesMassAndTreatsIsolatedNodesUniformly(t *testing.T) {
	got := pageRank([][]graphNeighbor{{{index: 1, weight: .5}}, {{index: 0, weight: .5}}, nil})
	if len(got) != 3 {
		t.Fatalf("pageRank() = %v, want 3 values", got)
	}
	sum := got[0] + got[1] + got[2]
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("pageRank mass = %.12f, want 1", sum)
	}
	if got[0] != got[1] || got[2] <= 0 {
		t.Fatalf("symmetric pair and dangling node ranks = %v, want equal pair and positive dangling-node mass", got)
	}
}

func TestTopKGraphBreaksTiesBySentenceIndex(t *testing.T) {
	similarities := [][]float64{
		{1, .5, .5, 0},
		{.5, 1, .9, 0},
		{.5, .9, 1, 0},
		{0, 0, 0, 1},
	}
	got := topKGraph(similarities, 1)
	if len(got[0]) != 1 || got[0][0] != (graphNeighbor{index: 1, weight: .5}) {
		t.Fatalf("equal-weight neighbors should prefer lower index from node 0: %v", got)
	}
}

func TestScoreSentencesValidatesInputs(t *testing.T) {
	valid := Sentence{Text: "A sentence.", StartUS: 0, EndUS: 1, Embedding: []float32{1, 0}}
	tests := []struct {
		name string
		in   []Sentence
	}{
		{name: "empty text", in: []Sentence{{Text: " ", StartUS: 0, EndUS: 1, Embedding: []float32{1}}}},
		{name: "invalid timestamps", in: []Sentence{{Text: "sentence", StartUS: 2, EndUS: 2, Embedding: []float32{1}}}},
		{name: "unordered timestamps", in: []Sentence{{Text: "first in source, later in time", StartUS: 2, EndUS: 3, Embedding: []float32{1, 0}}, {Text: "later in source, earlier in time", StartUS: 1, EndUS: 2, Embedding: []float32{1, 0}}}},
		{name: "dimension mismatch", in: []Sentence{valid, {Text: "sentence two", StartUS: 2, EndUS: 3, Embedding: []float32{1}}}},
		{name: "zero vector", in: []Sentence{{Text: "sentence", StartUS: 0, EndUS: 1, Embedding: []float32{0, 0}}}},
		{name: "non-finite vector", in: []Sentence{{Text: "sentence", StartUS: 0, EndUS: 1, Embedding: []float32{float32(math.NaN())}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ScoreSentences(test.in); !errors.Is(err, ErrInvalidSentence) {
				t.Fatalf("ScoreSentences() error = %v, want ErrInvalidSentence", err)
			}
		})
	}
}

func TestScoreSentencesAllowsFullyUntimedDocumentsButRejectsPartialTiming(t *testing.T) {
	untimed := []Sentence{
		{Text: "first", Embedding: []float32{1, 0}},
		{Text: "second", Embedding: []float32{0, 1}},
	}
	if got, err := ScoreSentences(untimed); err != nil || len(got) != 2 || got[1].StartUS != 0 {
		t.Fatalf("untimed scoring = %+v, %v; want scores with absent timestamps", got, err)
	}
	partial := []Sentence{untimed[0], {Text: "second", StartUS: 2, EndUS: 3, Embedding: []float32{0, 1}}}
	if _, err := ScoreSentences(partial); !errors.Is(err, ErrInvalidSentence) {
		t.Fatalf("partially timed document error = %v, want ErrInvalidSentence", err)
	}
}

func TestScoreSentencesEmptyAndSingleton(t *testing.T) {
	if got, err := ScoreSentences(nil); err != nil || got != nil {
		t.Fatalf("ScoreSentences(nil) = %v, %v; want nil, nil", got, err)
	}
	got, err := ScoreSentences([]Sentence{{Text: "only sentence", StartUS: 0, EndUS: 5, Embedding: []float32{3, 4}}})
	if err != nil {
		t.Fatalf("ScoreSentences(singleton) error = %v", err)
	}
	if len(got) != 1 || got[0].Centrality != 0 || got[0].Novelty != 0 || got[0].Score != 0 {
		t.Fatalf("singleton score = %+v, want zero salience for constant signals", got)
	}
}
