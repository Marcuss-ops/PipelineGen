package scripts

import "strings"

// NormalizeSearchText is the projection kept in `gemma_script_chunks.search_text`.
// It mirrors the LIKE-based query in FindSimilarChunksBySearchText:
//   - lowercase
//   - strip URL-ish tokens (http://, https://, www.)
//   - replace punctuation with spaces
//   - collapse whitespace
//
// The exact rule set is duplicated in pkg/sqlutil.BuildFallbackLikeConditions
// so the two stay in sync (token gating at >=3 chars on tokens level,
// NOT on search_text level — LIKE handles that).
func NormalizeSearchText(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ", "|", " ", "#", " ",
		"&", " ", "\n", " ", "\t", " ",
	).Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' {
			if !prevSpace {
				b.WriteRune(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
