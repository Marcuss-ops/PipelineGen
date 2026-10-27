package detail

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStringArrayColumnCodec pins the media_assets string-array column
// format contract (audit point 8 — the search_terms ” vs '[]' ambiguity):
// values are JSON arrays of strings, and every empty spelling means "no
// terms". The wire sites (app/wiring encodeStringSliceJSON, postgres/media
// clipTagsJSON + decodeTagsJSON) delegate to this codec; these cases are
// the behaviour they must not drift from.
func TestStringArrayColumnCodec(t *testing.T) {
	t.Run("encode", func(t *testing.T) {
		require.Equal(t, "", EncodeStringArrayColumn(nil),
			"empty list leaves the column at its schema default ''")
		require.Equal(t, "", EncodeStringArrayColumn([]string{}),
			"empty list leaves the column at its schema default ''")
		require.Equal(t, `["tokyo","news"]`, EncodeStringArrayColumn([]string{"tokyo", "news"}))
	})

	t.Run("parse_empty_spellings", func(t *testing.T) {
		// The audit's ambiguity: 070/097 default '' vs 091's documented '[]'.
		// Both spellings — and malformed legacy content — mean "no terms".
		require.Nil(t, ParseStringArrayColumn(""))
		require.Nil(t, ParseStringArrayColumn("[]"))
		require.Nil(t, ParseStringArrayColumn("  "))
		require.Nil(t, ParseStringArrayColumn("null"))
		require.Nil(t, ParseStringArrayColumn("not-json"))
		require.Nil(t, ParseStringArrayColumn(`{"a":1}`))
	})

	t.Run("parse_values", func(t *testing.T) {
		require.Equal(t, []string{"tokyo", "news"}, ParseStringArrayColumn(`["tokyo","news"]`))
		require.Equal(t, []string{"tokyo"}, ParseStringArrayColumn(`  ["tokyo"]  `))
		require.NotNil(t, ParseStringArrayColumn(`[null]`), "JSON null entries decode as empty strings, not a failure")
	})

	t.Run("round_trip", func(t *testing.T) {
		for _, values := range [][]string{nil, {}, {"a"}, {"a", "b", "c"}} {
			require.Equal(t, emptyToNil(values), emptyToNil(ParseStringArrayColumn(EncodeStringArrayColumn(values))))
		}
	})
}

// emptyToNil normalises the codec's nil-for-empty read result so the
// round-trip case can compare against the original slice literal.
func emptyToNil(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}
