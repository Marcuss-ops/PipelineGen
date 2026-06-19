package files

import (
	"testing"
	"time"
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

func TestBuildTimestampedSlug(t *testing.T) {
	fixedTime := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		expected string
	}{
		{"My Script", "20240615_103000_my-script"},
		{"", "20240615_103000_generated-script"},
		{"C# & .NET", "20240615_103000_c-net"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildTimestampedSlug(tt.name, fixedTime)
			if got != tt.expected {
				t.Errorf("BuildTimestampedSlug(%q, %v) = %q, want %q", tt.name, fixedTime, got, tt.expected)
			}
		})
	}
}
