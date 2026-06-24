package drive

import "testing"

func TestGoogleDocsEditURL(t *testing.T) {
	t.Parallel()

	got := googleDocsEditURL("doc-123")
	want := "https://docs.google.com/document/d/doc-123/edit"
	if got != want {
		t.Fatalf("unexpected URL: got %q want %q", got, want)
	}
}

func TestGoogleDocsEditURL_Empty(t *testing.T) {
	t.Parallel()

	if got := googleDocsEditURL("   "); got != "" {
		t.Fatalf("expected empty URL, got %q", got)
	}
}
