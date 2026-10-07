package indexing

import "testing"

// TestFirstNonEmpty_TrimContract pins the migration contract for this
// package's helper. Unlike the canonical textutil.FirstNonEmpty (which returns
// the chosen value as-is), the payload builder has always trim-normalized the
// chosen value and skipped whitespace-only candidates; the helper now selects
// through the canonical text helper and re-applies TrimSpace. A future edit
// that drops the TrimSpace (returning textutil.FirstNonEmpty directly) changes
// every payload field sourced from operator metadata, so these cases must keep
// passing.
func TestFirstNonEmpty_TrimContract(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "skips empty and whitespace-only", values: []string{"", "   ", "\tvalue"}, want: "value"},
		{name: "trims the chosen value", values: []string{"  chosen  ", "later"}, want: "chosen"},
		{name: "all blank", values: []string{"  ", ""}, want: ""},
		{name: "no values", want: ""},
	}
	for _, tc := range cases {
		if got := firstNonEmpty(tc.values...); got != tc.want {
			t.Errorf("%s: firstNonEmpty(%q) = %q, want %q", tc.name, tc.values, got, tc.want)
		}
	}
}
