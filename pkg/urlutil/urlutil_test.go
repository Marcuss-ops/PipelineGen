package urlutil

import (
	"testing"
)

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{"empty", "", "", true},
		{"standard watch", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"short youtu.be", "https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"short youtu.be with query", "https://youtu.be/dQw4w9WgXcQ?t=42", "dQw4w9WgXcQ", false},
		{"shorts", "https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"embed", "https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"live", "https://www.youtube.com/live/dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"mobile", "https://m.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ", false},
		{"with extra params", "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s", "dQw4w9WgXcQ", false},
		{"not youtube", "https://example.com/video", "", true},
		{"invalid url", "://bad-url", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractVideoID(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractVideoID(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ExtractVideoID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestFileIDFromDriveLink(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"file d view", "https://drive.google.com/file/d/1abc123/view", "1abc123", false},
		{"file d edit", "https://drive.google.com/file/d/1abc123/edit", "1abc123", false},
		{"file d with query", "https://drive.google.com/file/d/1abc123?usp=drivesdk", "1abc123", false},
		{"uc legacy", "https://drive.google.com/uc?id=1abc123", "1abc123", false},
		{"open legacy", "https://drive.google.com/open?id=1abc123", "1abc123", false},
		{"bare id", "1abc123def456", "1abc123def456", false},
		{"short bare id", "abc123", "", true},
		{"folder", "https://drive.google.com/drive/folders/1abc123", "", true},
		{"not drive", "https://example.com/file", "", true},
		{"invalid url", "://bad-url", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FileIDFromDriveLink(tt.link)
			if (err != nil) != tt.wantErr {
				t.Errorf("FileIDFromDriveLink(%q) error = %v, wantErr %v", tt.link, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("FileIDFromDriveLink(%q) = %q, want %q", tt.link, got, tt.want)
			}
		})
	}
}
