package drive

import (
	"testing"

	"google.golang.org/api/docs/v1"
)

func TestIsHTMLContent(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"<!DOCTYPE html><html><body><h1>Hi</h1></body></html>": true,
		"<html></html>": true,
		"<h1>Hi</h1>":   true,
		"<p>para</p>":   true,
		"<div>x</div>":  true,
		"plain text":    false,
		"":              false,
	}
	for in, want := range cases {
		if got := isHTMLContent(in); got != want {
			t.Fatalf("isHTMLContent(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBodyEndIndex(t *testing.T) {
	t.Parallel()

	if got := bodyEndIndex(nil); got != 1 {
		t.Fatalf("bodyEndIndex(nil) = %d, want 1", got)
	}
	doc := &docs.Document{Body: &docs.Body{Content: []*docs.StructuralElement{
		{EndIndex: 1},
		{EndIndex: 12},
	}}}
	if got := bodyEndIndex(doc); got != 12 {
		t.Fatalf("bodyEndIndex = %d, want 12", got)
	}
}
