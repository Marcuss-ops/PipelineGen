package termutil

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// TermOptions configures term filtering — see terms_from_text.go.
type TermOptions struct {
	MinLen      int
	Lowercase   bool
	RemoveStops bool
	StopWords   map[string]struct{}
	Unique      bool
	UniqueCI    bool
	Limit       int
}

// TermsFromText tokenizes text and returns filtered terms.
func TermsFromText(text string, opts TermOptions) []string {
	if text == "" {
		return nil
	}
	tokens := textutil.Tokenize(text)
	return filterTerms(tokens, opts)
}

// filterTerms is the canonical normalization pipeline consumed by
// TermsFromText. Keep this near its caller — extracted to a dedicated
// file only if a second consumer appears in a non-related concept file.
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
		if opts.RemoveStops {
			if _, isStop := opts.StopWords[term]; isStop {
				continue
			}
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
