package termutil

import (
	"strings"
	"velox/go-master/pkg/sliceutil"
	"velox/go-master/pkg/textutil"
)

type Options struct {
	MinLen       int
	Lowercase    bool
	RemoveStops  bool
	Unique       bool
	UniqueCI     bool
	Limit        int
}

func defaultOpts() Options {
	return Options{
		MinLen:      3,
		Lowercase:   true,
		RemoveStops: true,
		Unique:      true,
	}
}

// TermsFromText tokenizes text and returns filtered terms.
func TermsFromText(text string, opts Options) []string {
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
func CleanTerms(terms []string, opts Options) []string {
	return filterTerms(terms, opts)
}

func filterTerms(input []string, opts Options) []string {
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
