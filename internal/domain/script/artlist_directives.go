package script

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

var artlistDirectivePattern = regexp.MustCompile(`(?im)^[ \t]*\[ARTLIST:[ \t]*([^\]]+?)[ \t]*\](?:\r?\n|$)`)

// ParseArtlistDirectives removes line-oriented Artlist directives from
// editorial text and returns their comma-separated visual queries.
func ParseArtlistDirectives(text string) (string, []string) {
	var queries []string
	clean := artlistDirectivePattern.ReplaceAllStringFunc(text, func(line string) string {
		match := artlistDirectivePattern.FindStringSubmatch(line)
		if len(match) != 2 {
			return ""
		}
		for _, raw := range strings.Split(match[1], ",") {
			if q := strings.Join(strings.Fields(strings.TrimSpace(raw)), " "); q != "" {
				queries = append(queries, q)
			}
		}
		return ""
	})
	clean = strings.TrimSpace(strings.ReplaceAll(clean, "\n\n\n", "\n\n"))
	return clean, uniqueArtlistQueries(queries)
}

// ArtlistSearchIntentHash is the canonical cache identity for explicit
// Artlist intent. Ordering and casing do not create different intents.
func ArtlistSearchIntentHash(queries []string) string {
	normalized := uniqueArtlistQueries(queries)
	sort.Strings(normalized)
	return digest.SHA256String(strings.Join(normalized, "\x00"))
}

func uniqueArtlistQueries(queries []string) []string {
	seen := make(map[string]struct{}, len(queries))
	out := make([]string, 0, len(queries))
	for _, raw := range queries {
		q := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(raw)), " "))
		if q == "" {
			continue
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		out = append(out, q)
	}
	return out
}
