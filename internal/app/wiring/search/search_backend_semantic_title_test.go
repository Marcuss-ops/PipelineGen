package search

import "testing"

// TestSemanticDisplayTitle pins the label policy shared with the lexical leg:
// the SSOT title wins when the catalog carries one, and the canonical asset name
// is the fallback. Guards the regression where a Stock clip hit surfaced as
// "clip_010.mp4" even though media_assets.title held its real source title.
func TestSemanticDisplayTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
		asset string
		want  string
	}{
		{"title wins over name", "IRON MIKE TYSON IN ACTION", "clip_010.mp4", "IRON MIKE TYSON IN ACTION"},
		{"blank title falls back to name", "", "clip_010.mp4", "clip_010.mp4"},
		{"whitespace-only title falls back to name", "   ", "clip_010.mp4", "clip_010.mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := semanticDisplayTitle(tc.title, tc.asset); got != tc.want {
				t.Fatalf("semanticDisplayTitle(%q, %q) = %q, want %q", tc.title, tc.asset, got, tc.want)
			}
		})
	}
}
