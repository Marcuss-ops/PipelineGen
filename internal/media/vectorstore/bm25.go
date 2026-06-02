package vectorstore

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// TokenizeBM25 converts text into a sparse vector (indices + values) for BM25 search.
// Uses a simple hash-based term → index mapping with TF (term frequency) weighting.
// Indices are sorted ascending as required by Qdrant.
//
// vocabSize controls the dimensionality of the sparse space (default 25000).
// Words are hashed to indices in [0, vocabSize) using FNV-1a.
func TokenizeBM25(text string, vocabSize int) *SparseVector {
	if text == "" || vocabSize <= 0 {
		return nil
	}

	// Normalize and split into tokens
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return nil
	}

	// Count term frequencies
	tf := make(map[uint32]float32, len(tokens))
	for _, tok := range tokens {
		idx := hashToken(tok, vocabSize)
		tf[idx]++
	}

	// Convert to sorted slice (Qdrant requires ascending indices)
	indices := make([]uint32, 0, len(tf))
	for idx := range tf {
		indices = append(indices, idx)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

	// Build values in the same order, applying BM25-style TF saturation
	values := make([]float32, len(indices))
	for i, idx := range indices {
		// BM25 TF saturation: tf / (tf + k1*(1-b + b*dl/avgdl))
		// Simplified: use log(1 + tf) for scale compression
		raw := tf[idx]
		values[i] = float32(1.0 + ln(raw))
	}

	return &SparseVector{Indices: indices, Values: values}
}

// ln computes ln(1+x) for BM25 TF saturation using math.Log1p for numerical stability.
func ln(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return float32(math.Log1p(float64(x)))
}

// tokenize splits text into lowercase alphanumeric tokens.
func tokenize(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	tokens := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.TrimSpace(w)
		if len(w) < 2 || isStopword(w) {
			continue
		}
		tokens = append(tokens, w)
	}
	return tokens
}

// isStopword filters common English and Italian stopwords.
func isStopword(word string) bool {
	switch word {
	case "the", "a", "an", "is", "are", "was", "were", "be", "been",
		"being", "have", "has", "had", "do", "does", "did", "will",
		"would", "could", "should", "may", "might", "can", "shall",
		"to", "of", "in", "for", "on", "with", "at", "by", "from",
		"as", "into", "through", "during", "before", "after",
		"above", "below", "between", "under", "over", "out",
		"and", "but", "or", "nor", "not", "so", "if", "then",
		"than", "too", "very", "just", "about", "also",
		"that", "this", "these", "those", "it", "its",
		"il", "la", "i", "gli", "le", "un", "una", "di", "da",
		"con", "su", "per", "tra", "fra", "che", "e",
		"non", "si", "lo", "del", "della", "dei", "delle", "al",
		"alla", "agli", "alle", "nel", "nella", "nei", "nelle":
		return true
	}
	return false
}

// hashToken hashes a token string to an index in [0, vocabSize) using FNV-1a.
func hashToken(token string, vocabSize int) uint32 {
	const (
		fnvOffset32 = 2166136261
		fnvPrime32  = 16777619
	)
	h := uint32(fnvOffset32)
	for _, b := range []byte(token) {
		h ^= uint32(b)
		h *= fnvPrime32
	}
	return h % uint32(vocabSize)
}
