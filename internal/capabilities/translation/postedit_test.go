// Package translation — postedit_test.go: contract tests for the canonical
// post-editing of a translated cue.
package translation

import "testing"

func TestPostEdit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "collapses whitespace and trims", in: "  ciao ,   come   stai  ", want: "ciao, come stai"},
		{name: "re-spaces a glued comma", in: "ciao,come", want: "ciao, come"},
		{name: "re-spaces a glued semicolon", in: "fine;poi", want: "fine; poi"},
		{name: "keeps a decimal comma", in: "1,5 milioni", want: "1,5 milioni"},
		{name: "removes space before a full stop", in: "rendering  affidabile .", want: "rendering affidabile."},
		{name: "removes inner space of a parenthetical", in: "( nota )", want: "(nota)"},
		{name: "collapses newlines", in: "hello\nworld", want: "hello world"},
		{name: "empty stays empty", in: "", want: ""},
		{name: "whitespace-only collapses to empty", in: "   ", want: ""},
		{name: "does not touch letter case", in: "la coerenza , il processo", want: "la coerenza, il processo"},
		{name: "leaves a percentage glued", in: "50 % di sconto", want: "50% di sconto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PostEdit(tc.in)
			if got != tc.want {
				t.Fatalf("PostEdit(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := PostEdit(got); again != got {
				t.Fatalf("PostEdit is not idempotent: once=%q twice=%q", got, again)
			}
		})
	}
}
