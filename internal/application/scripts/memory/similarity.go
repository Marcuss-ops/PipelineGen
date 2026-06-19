package memory

import (
	"strings"
	"unicode"
)

// ngramSize is the token window used for similarity. 3-grams are a good
// compromise: long enough to be specific (single-word matches do not inflate
// the score), short enough to catch repeated phrasing without needing
// embeddings.
const ngramSize = 3

// ngramSet returns the set of normalised n-grams in text. Set semantics mean
// the same n-gram repeated 10 times counts as 1, which is what we want for
// Jaccard similarity (it measures vocabulary overlap, not bulk copy).
func ngramSet(text string) map[string]struct{} {
	tokens := tokenize(text)
	if len(tokens) < ngramSize {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(tokens))
	for i := 0; i+ngramSize <= len(tokens); i++ {
		out[strings.Join(tokens[i:i+ngramSize], " ")] = struct{}{}
	}
	return out
}

func tokenize(text string) []string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Fields(b.String())
}

// jaccard returns |A ∩ B| / |A ∪ B| for two n-gram sets. 0.0 = no overlap,
// 1.0 = identical n-gram vocabulary. Empty inputs return 0.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var intersect int
	// Iterate over the smaller set to keep the inner loop cheap.
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	for k := range small {
		if _, ok := large[k]; ok {
			intersect++
		}
	}
	union := len(a) + len(b) - intersect
	if union == 0 {
		return 0
	}
	return float64(intersect) / float64(union)
}

// DetectNearDuplicate compares newText against each of the previous outputs
// and returns the highest Jaccard score found, plus a boolean flag set when
// the score exceeds the policy threshold. Empty previous outputs are skipped.
//
// The check is intentionally lightweight (no embeddings, no model calls) so
// it can run on every generation without measurable overhead. Callers can use
// the flag to log a warning, store the score in job metadata, or — later —
// trigger a rewrite. Right now the job handler only logs a warning.
func DetectNearDuplicate(newText string, previousOutputs []string, policy MemoryPolicy) (score float64, flagged bool) {
	threshold := policy.SimilarityThreshold
	if threshold <= 0 {
		threshold = DefaultMemoryPolicy().SimilarityThreshold
	}
	newSet := ngramSet(strings.TrimSpace(newText))
	if len(newSet) == 0 {
		return 0, false
	}
	for _, prev := range previousOutputs {
		prev = strings.TrimSpace(prev)
		if prev == "" {
			continue
		}
		s := jaccard(newSet, ngramSet(prev))
		if s > score {
			score = s
			if score >= threshold {
				flagged = true
			}
		}
	}
	return score, flagged
}
