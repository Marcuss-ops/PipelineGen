package assets

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

type TermOptions struct {
	MinLen      int
	Lowercase   bool
	RemoveStops bool
	Unique      bool
	UniqueCI    bool
	Limit       int
}

func defaultOpts() TermOptions {
	return TermOptions{
		MinLen:      3,
		Lowercase:   true,
		RemoveStops: true,
		Unique:      true,
	}
}

// TermsFromText tokenizes text and returns filtered terms.
func TermsFromText(text string, opts TermOptions) []string {
	if text == "" {
		return nil
	}
	tokens := textutil.Tokenize(text)
	return filterTerms(tokens, opts)
}

// TermsFromFields collects terms from multiple string fields.
func TermsFromFields(fields ...string) []string {
	opts := defaultOpts()
	var all []string
	for _, f := range fields {
		if f != "" {
			all = append(all, textutil.Tokenize(f)...)
		}
	}
	return filterTerms(all, opts)
}

// CleanTerms filters and normalizes an existing slice of terms.
func CleanTerms(terms []string, opts TermOptions) []string {
	return filterTerms(terms, opts)
}

func filterTerms(input []string, opts TermOptions) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	for _, term := range input {
		term = strings.TrimSpace(term)
		if opts.Lowercase {
			term = strings.ToLower(term)
		}
		if term == "" {
			continue
		}
		if opts.RemoveStops && textutil.IsStopWord(term) {
			continue
		}
		if opts.MinLen > 0 && len(term) < opts.MinLen {
			continue
		}
		out = append(out, term)
	}
	if opts.Unique {
		out = sliceutil.UniqueStrings(out)
	} else if opts.UniqueCI {
		out = sliceutil.UniqueStringsCI(out)
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out
}

// LooksLikePersonName checks if the text looks like a person's name.
func LooksLikePersonName(text string) bool {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || len(parts) > 5 {
		return false
	}
	score := 0
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		first := []rune(part)[0]
		if first >= 'A' && first <= 'Z' {
			score++
		}
	}
	return score >= 1 && len(parts) <= 4
}

// ExtractLikelyNames extracts words that look like names (capitalized, >2 chars).
func ExtractLikelyNames(text string) []string {
	var names []string
	words := strings.Fields(text)
	for _, w := range words {
		w = strings.Trim(w, ".,!?:;\"'()")
		if len(w) > 2 && len(w) > 0 && w[0] >= 'A' && w[0] <= 'Z' {
			names = append(names, w)
		}
	}
	return sliceutil.UniqueStrings(names)
}
