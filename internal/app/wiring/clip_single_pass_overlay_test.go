package wiring

import "testing"

// TestSinglePassOverlayEnabled pins the switch contract: single-pass overlay
// compositing is the DEFAULT (a clip carrying an overlay is encoded once), the
// documented escape hatch disables it without a code change, and a malformed
// value keeps the default instead of silently flipping behaviour.
func TestSinglePassOverlayEnabled(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true}, // unset → single-pass default
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{" 1 ", true}, // whitespace tolerated
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"maybe", true}, // unparseable → keep the default
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("PIPELINEGEN_CLIP_SINGLE_PASS_OVERLAY", tc.value)
			if got := singlePassOverlayEnabled(nil); got != tc.want {
				t.Fatalf("singlePassOverlayEnabled(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
