package script

import "testing"

func TestModeForSource(t *testing.T) {
	tests := []struct {
		source SourceType
		want   string
	}{
		{source: SourceText, want: "text"},
		{source: SourceClips, want: "clip_to_script"},
		{source: SourceCatalog, want: "clip_to_script"},
		{source: SourceSearch, want: "clip_to_script"},
		{source: SourceCurate, want: "clip_to_script"},
		{source: SourceResearch, want: "text"},
		{source: SourceType("unknown"), want: "text"},
	}

	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			if got := ModeForSource(tt.source); got != tt.want {
				t.Fatalf("ModeForSource(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}
