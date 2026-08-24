package delivery

import (
	"context"
	"testing"
)

func TestDefaultLicenseAndAuthor(t *testing.T) {
	cases := []struct {
		source      string
		wantLicense string
		wantAuthor  string
	}{
		{"wikipedia", "CC-BY-SA-4.0", "Wikipedia Contributors"},
		{"google-slides", "proprietary", "PipelineGen"},
		{"duckduckgo", "unknown", "Unknown"},
		{"unknown-source", "Unknown", "Unknown"},
		{"", "Unknown", "Unknown"},
	}
	for _, tt := range cases {
		license, author := defaultLicenseAndAuthor(context.Background(), tt.source)
		if license != tt.wantLicense {
			t.Errorf("defaultLicenseAndAuthor(%q) license = %q, want %q", tt.source, license, tt.wantLicense)
		}
		if author != tt.wantAuthor {
			t.Errorf("defaultLicenseAndAuthor(%q) author = %q, want %q", tt.source, author, tt.wantAuthor)
		}
	}
}
