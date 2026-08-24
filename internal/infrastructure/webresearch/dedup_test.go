package webresearch

import (
	"testing"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

func TestNormalizeWebURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"empty", "", "", true},
		{"no scheme", "example.com/path", "", true},
		{"ftp scheme", "ftp://example.com/path", "", true},
		{"http basic", "http://example.com/path", "http://example.com/path", false},
		{"https basic", "https://example.com/path", "https://example.com/path", false},
		{"strip www", "https://www.example.com/path", "https://example.com/path", false},
		{"lowercase host", "https://EXAMPLE.COM/Path", "https://example.com/Path", false},
		{"strip trailing slash", "https://example.com/", "https://example.com/", false},
		{"root path no trailing slash", "https://example.com", "https://example.com/", false},
		{"strip utm params", "https://example.com/page?utm_source=google&utm_medium=cpc&id=42", "https://example.com/page?id=42", false},
		{"strip fbclid", "https://example.com/page?fbclid=abc123", "https://example.com/page", false},
		{"strip gclid", "https://example.com/page?gclid=xyz", "https://example.com/page", false},
		{"keep non-tracking params", "https://example.com/page?q=test&page=2", "https://example.com/page?page=2&q=test", false},
		{"strip fragment", "https://example.com/page#section", "https://example.com/page", false},
		{"www subdomain stripped", "https://www.www.example.com/path", "https://www.example.com/path", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeWebURL(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("NormalizeWebURL(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("NormalizeWebURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDeduplicateHits(t *testing.T) {
	tests := []struct {
		name string
		hits []scriptports.WebSearchHit
		want int
	}{
		{
			name: "empty",
			hits: nil,
			want: 0,
		},
		{
			name: "unique URLs",
			hits: []scriptports.WebSearchHit{
				{Title: "A", URL: "https://a.com/page1", Content: "c1"},
				{Title: "B", URL: "https://b.com/page2", Content: "c2"},
				{Title: "C", URL: "https://c.com/page3", Content: "c3"},
			},
			want: 3,
		},
		{
			name: "same host different path casing are distinct",
			hits: []scriptports.WebSearchHit{
				{Title: "A", URL: "https://example.com/Page", Content: "c1"},
				{Title: "B", URL: "https://example.com/page", Content: "c2"},
			},
			want: 2, // HTTP paths are case-sensitive
		},
		{
			name: "www vs non-www",
			hits: []scriptports.WebSearchHit{
				{Title: "A", URL: "https://www.example.com/page", Content: "c1"},
				{Title: "B", URL: "https://example.com/page", Content: "c2"},
			},
			want: 1,
		},
		{
			name: "same host same title",
			hits: []scriptports.WebSearchHit{
				{Title: "Boxing News", URL: "https://example.com/box", Content: "c1"},
				{Title: "Boxing News", URL: "https://example.com/box2", Content: "c2"},
			},
			want: 1,
		},
		{
			name: "same host different title",
			hits: []scriptports.WebSearchHit{
				{Title: "Boxing News Today", URL: "https://example.com/a", Content: "c1"},
				{Title: "Boxing Results Yesterday", URL: "https://example.com/b", Content: "c2"},
			},
			want: 2,
		},
		{
			name: "with tracking params",
			hits: []scriptports.WebSearchHit{
				{Title: "A", URL: "https://example.com/page?utm_source=google&id=1", Content: "c1"},
				{Title: "B", URL: "https://example.com/page?id=1", Content: "c2"},
			},
			want: 1,
		},
		{
			name: "invalid URLs are dropped",
			hits: []scriptports.WebSearchHit{
				{Title: "A", URL: "not-a-url", Content: "c1"},
				{Title: "B", URL: "https://example.com/ok", Content: "c2"},
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeduplicateHits(tt.hits)
			if len(got) != tt.want {
				t.Errorf("DeduplicateHits() returned %d hits, want %d", len(got), tt.want)
				for i, h := range got {
					t.Logf("  [%d] %s %s", i, h.Title, h.URL)
				}
			}
		})
	}
}

func TestNormalizeTitle(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Hello World", "hello world"},
		{"  Spaces  ", "spaces"},
		{"Multiple   Spaces", "multiple spaces"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeTitle(tt.in)
		if got != tt.want {
			t.Errorf("normalizeTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
