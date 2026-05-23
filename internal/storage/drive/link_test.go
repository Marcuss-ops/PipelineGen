package drive

import "testing"

func TestFileIDFromLink(t *testing.T) {
	tests := []struct {
		name string
		link string
		want string
	}{
		{
			name: "file view link",
			link: "https://drive.google.com/file/d/ABC123/view?usp=sharing",
			want: "ABC123",
		},
		{
			name: "download link",
			link: "https://drive.google.com/uc?id=XYZ789",
			want: "XYZ789",
		},
		{
			name: "folder link",
			link: "https://drive.google.com/drive/folders/FOLDER123?usp=sharing",
			want: "FOLDER123",
		},
		{
			name: "empty link",
			link: "",
			want: "",
		},
		{
			name: "invalid link",
			link: "https://example.com/test",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FileIDFromLink(tt.link)
			if got != tt.want {
				t.Fatalf("FileIDFromLink(%q) = %q, want %q", tt.link, got, tt.want)
			}
		})
	}
}

func TestNormalizeDriveFolderLink(t *testing.T) {
	if got := NormalizeDriveFolderLink("https://docs.google.com/document/d/doc-id/edit", "folder-123"); got != "https://drive.google.com/drive/folders/folder-123" {
		t.Fatalf("expected folder fallback for docs link, got %q", got)
	}

	if got := NormalizeDriveFolderLink("https://drive.google.com/drive/folders/folder-123", "fallback"); got != "https://drive.google.com/drive/folders/folder-123" {
		t.Fatalf("expected existing folder link to win, got %q", got)
	}
}
