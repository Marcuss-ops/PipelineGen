package filesystem

import (
	"testing"
)

func TestSafeFolderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "Hello World"},
		{"hello", "hello"},
		{"", "untitled"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SafeFolderName(tt.input)
			if got != tt.expected {
				t.Errorf("SafeFolderName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractStyleFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"images/downloaded/google-vids/photorealistic/real/abc123/def456.png", "photorealistic"},
		{"images/generated/cartoon/hash123.jpg", "cartoon"},
		{"images/downloaded/nvidia/medievale/italian/xyz789/abc.jpg", "medievale"},
		{"", ""},
		{"images/downloaded", ""},
		{"images/custom/whatever", ""},
		{"/home/user/images/generated/anime/face.png", ""},
		{"other/generated/test", "test"},
		{"image.png", ""},
		{"images/generated/anime", "anime"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ExtractStyleFromPath(tt.path)
			if got != tt.expected {
				t.Errorf("ExtractStyleFromPath(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}
