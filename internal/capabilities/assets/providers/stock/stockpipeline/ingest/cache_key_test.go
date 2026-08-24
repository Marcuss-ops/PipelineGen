package assets

import "testing"

func TestDeriveSourceCacheKeyCanonicalizesEquivalentYouTubeURLs(t *testing.T) {
	watch := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y", "", "", false)
	short := DeriveSourceCacheKey("https://youtu.be/QdSbtEo3x_Y", "", "", false)
	withList := DeriveSourceCacheKey("https://www.youtube.com/watch?v=QdSbtEo3x_Y&list=PLxyz", "", "", false)
	if watch == "" || watch != short || watch != withList {
		t.Fatalf("equivalent YouTube URLs produced keys %q, %q, %q", watch, short, withList)
	}
}

func TestDeriveSourceCacheKeyIncludesDownloadOptions(t *testing.T) {
	base := DeriveSourceCacheKey("https://example.test/video.mp4", "", "", false)
	section := DeriveSourceCacheKey("https://example.test/video.mp4", "10-20", "", false)
	format := DeriveSourceCacheKey("https://example.test/video.mp4", "", "mp4", false)
	keyframes := DeriveSourceCacheKey("https://example.test/video.mp4", "", "", true)
	if base == section || base == format || base == keyframes {
		t.Fatal("cache key ignored one or more download options")
	}
}

func TestNormalizeSourceURLLeavesNonYouTubeInputUnchanged(t *testing.T) {
	const raw = "https://example.test/video.mp4?token=abc"
	if got := NormalizeSourceURL(raw); got != raw {
		t.Fatalf("NormalizeSourceURL() = %q, want %q", got, raw)
	}
}

func TestExtractVideoIDSupportedForms(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "watch", raw: "https://www.youtube.com/watch?v=abc123&list=xyz", want: "abc123"},
		{name: "short", raw: "https://youtu.be/abc123", want: "abc123"},
		{name: "non youtube", raw: "https://example.test/video.mp4", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractVideoID(tc.raw); got != tc.want {
				t.Fatalf("ExtractVideoID(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestExtractVideoIDPreservesLegacyEdgeCaseBehavior(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "short query remains part of permissive id", raw: "https://youtu.be/abc123?feature=share", want: "abc123?feature=share"},
		{name: "shorts path is unsupported", raw: "https://www.youtube.com/shorts/abc123", want: ""},
		{name: "empty watch value remains empty", raw: "https://www.youtube.com/watch?v=", want: ""},
		{name: "surrounding whitespace is trimmed", raw: "  https://www.youtube.com/watch?v=abc123  ", want: "abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractVideoID(tc.raw); got != tc.want {
				t.Fatalf("ExtractVideoID(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestNormalizeSourceURLPreservesUnsupportedYouTubeForm(t *testing.T) {
	const raw = "https://www.youtube.com/shorts/abc123"
	if got := NormalizeSourceURL(raw); got != raw {
		t.Fatalf("NormalizeSourceURL(%q) = %q, want unchanged unsupported form", raw, got)
	}
}
