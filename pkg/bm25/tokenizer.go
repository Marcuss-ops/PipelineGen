// Package bm25 provides a client-side BM25 tokenizer for Qdrant sparse
// vector search. It converts query text into the sparse vector format
// that Qdrant expects for hybrid (dense + sparse) retrieval via RRF fusion.
//
// QDRANT-004/005: The tokenizer produces (indices, values) pairs where:
//   - indices are FNV-1a 32-bit hashes of lowercase tokens
//   - values are term-frequency scores (count / max_count)
//
// Collection-wide IDF is NOT applied at query time — Qdrant's built-in
// BM25 modifier on the sparse vector channel handles IDF from the indexed
// payload. The client only needs to provide term-frequency-weighted tokens
// with consistent hash indices.
//
// The hash must MATCH the indexing side. Currently, indexing uses
// Qdrant's server-side BM25 configured via sparse_vectors in the
// collection schema (see qdrant.DefaultV3Schema). The client-side
// tokenizer mirrors the same normalization: lowercase, strip punctuation,
// split on whitespace, keep tokens ≥2 chars.
package bm25

import (
	"hash/fnv"
	"strings"
	"unicode"
)

// SparseVector is a Qdrant-compatible sparse vector representation.
type SparseVector struct {
	Indices []uint32  `json:"indices"`
	Values  []float32 `json:"values"`
}

// Tokenize converts query text into a BM25-weighted sparse vector.
// Returns nil when the input produces no valid tokens.
//
// Processing pipeline:
//  1. Lowercase
//  2. Split on whitespace
//  3. Drop tokens < 2 chars
//  4. Strip leading/trailing punctuation from each token
//  5. Hash each token with FNV-1a 32-bit
//  6. Compute term frequency (count / max_count)
func Tokenize(text string) *SparseVector {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return nil
	}

	// Split and clean tokens.
	raw := strings.Fields(text)
	type tokenData struct {
		hash  uint32
		count int
	}
	seen := make(map[uint32]*tokenData)
	maxCount := 0

	for _, t := range raw {
		t = stripPunct(t)
		if len(t) < 2 {
			continue
		}
		h := hashToken(t)
		td, ok := seen[h]
		if !ok {
			td = &tokenData{hash: h}
			seen[h] = td
		}
		td.count++
		if td.count > maxCount {
			maxCount = td.count
		}
	}

	if len(seen) == 0 {
		return nil
	}

	vec := &SparseVector{
		Indices: make([]uint32, 0, len(seen)),
		Values:  make([]float32, 0, len(seen)),
	}
	for _, td := range seen {
		vec.Indices = append(vec.Indices, td.hash)
		// TF normalization: count / max_count gives values in (0, 1].
		vec.Values = append(vec.Values, float32(td.count)/float32(maxCount))
	}
	return vec
}

// hashToken returns the FNV-1a 32-bit hash of a token.
func hashToken(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// stripPunct removes leading and trailing punctuation/whitespace from a token.
func stripPunct(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
}

// Compile-time assertion: SparseVector matches the Qdrant sparse format.
var _ = SparseVector{Indices: nil, Values: nil}
