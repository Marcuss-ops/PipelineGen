package apiutil

import "testing"

// TestClampLimit covers the canonical pagination helper used by all
// list/handle endpoints (script helpers, asset scraper handler, ...).
// Migrated from internal/api/apiutil_test.go on 2026-06-27 as part of
// PR 4 (root file reduction): the test imports only pkg/apiutil so the
// canonical home for it is pkg/apiutil/clamp_test.go, not a sibling
// package that merely consumed the helper.
func TestClampLimit(t *testing.T) {
	tests := []struct {
		name string
		val  int
		def  int
		max  int
		want int
	}{
		{"zero uses default", 0, 10, 100, 10},
		{"negative uses default", -5, 10, 100, 10},
		{"within range", 50, 10, 100, 50},
		{"at max", 100, 10, 100, 100},
		{"exceeds max", 150, 10, 100, 100},
		{"at default", 10, 10, 100, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampLimit(tt.val, tt.def, tt.max)
			if got != tt.want {
				t.Errorf("ClampLimit(%d, %d, %d) = %d, want %d", tt.val, tt.def, tt.max, got, tt.want)
			}
		})
	}
}
