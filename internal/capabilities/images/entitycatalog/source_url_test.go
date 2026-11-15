package entitycatalog

import "testing"

func TestNormalizeSourceURLIsTheCandidateUniqueKey(t *testing.T) {
	want := "https://images.example/a.jpg?x=1"
	for _, input := range []string{
		"HTTPS://IMAGES.EXAMPLE:443/a.jpg?x=1&utm_source=feed#fragment",
		"https://images.example/a.jpg?utm_campaign=x&x=1&fbclid=abc",
		"https://images.example:443/a.jpg?x=1",
	} {
		if got := NormalizeSourceURL(input); got != want {
			t.Fatalf("NormalizeSourceURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeSourceURLPreservesMalformedInput(t *testing.T) {
	const raw = "not a url"
	if got := NormalizeSourceURL(raw); got != raw {
		t.Fatalf("malformed URL changed to %q", got)
	}
}
