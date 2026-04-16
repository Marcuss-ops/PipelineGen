package download

import (
	"context"
	"testing"
)

// --- DetectPlatform Tests ---

func TestDetectPlatform(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected Platform
	}{
		// YouTube URLs
		{
			name:     "YouTube watch URL",
			url:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			expected: PlatformYouTube,
		},
		{
			name:     "YouTube short URL",
			url:      "https://youtu.be/dQw4w9WgXcQ",
			expected: PlatformYouTube,
		},
		{
			name:     "YouTube embed URL",
			url:      "https://www.youtube.com/embed/dQw4w9WgXcQ",
			expected: PlatformYouTube,
		},
		{
			name:     "YouTube uppercase",
			url:      "https://WWW.YOUTUBE.COM/watch?v=abc123",
			expected: PlatformYouTube,
		},
		// TikTok URLs
		{
			name:     "TikTok video URL",
			url:      "https://www.tiktok.com/@user/video/7123456789012345678",
			expected: PlatformTikTok,
		},
		{
			name:     "TikTok short URL",
			url:      "https://vm.tiktok.com/ZMabcdefg/",
			expected: PlatformTikTok,
		},
		{
			name:     "TikTok uppercase",
			url:      "https://WWW.TIKTOK.COM/@user/video/123",
			expected: PlatformTikTok,
		},
		// Unknown/unsupported URLs
		{
			name:     "Instagram URL",
			url:      "https://www.instagram.com/reel/abc123/",
			expected: "",
		},
		{
			name:     "Twitter URL",
			url:      "https://twitter.com/user/status/123",
			expected: "",
		},
		{
			name:     "Plain URL",
			url:      "https://example.com/video.mp4",
			expected: "",
		},
		{
			name:     "Empty URL",
			url:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectPlatform(tt.url)
			if result != tt.expected {
				t.Errorf("DetectPlatform(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

// --- ExtractVideoID Tests ---

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		// YouTube
		{
			name:     "YouTube watch URL",
			url:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10",
			expected: "dQw4w9WgXcQ",
		},
		{
			name:     "YouTube short URL",
			url:      "https://youtu.be/dQw4w9WgXcQ",
			expected: "dQw4w9WgXcQ",
		},
		{
			name:     "YouTube embed URL",
			url:      "https://www.youtube.com/embed/dQw4w9WgXcQ?autoplay=1",
			expected: "dQw4w9WgXcQ",
		},
		// TikTok
		{
			name:     "TikTok video URL",
			url:      "https://www.tiktok.com/@user/video/7123456789012345678",
			expected: "7123456789012345678",
		},
		{
			name:     "TikTok short URL",
			url:      "https://vm.tiktok.com/abc123/",
			expected: "", // extractTikTokID doesn't handle vm.tiktok.com short URLs
		},
		{
			name:     "TikTok short URL no trailing slash",
			url:      "https://vm.tiktok.com/abc123",
			expected: "abc123", // Returns last path segment
		},
		// Unsupported
		{
			name:     "Instagram URL",
			url:      "https://www.instagram.com/reel/abc123/",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractVideoID(tt.url)
			if result != tt.expected {
				t.Errorf("ExtractVideoID(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

// --- URL Validation Tests ---

func TestIsYouTubeURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://www.youtube.com/embed/abc", true},
		{"https://www.tiktok.com/@user/video/123", false},
		{"https://example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := isYouTubeURL(tt.url)
			if result != tt.expected {
				t.Errorf("isYouTubeURL(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

func TestIsTikTokURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://www.tiktok.com/@user/video/123", true},
		{"https://vm.tiktok.com/abc/", true},
		{"https://www.youtube.com/watch?v=abc", false},
		{"https://example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			result := isTikTokURL(tt.url)
			if result != tt.expected {
				t.Errorf("isTikTokURL(%q) = %v, want %v", tt.url, result, tt.expected)
			}
		})
	}
}

// --- extractYouTubeID Tests ---

func TestExtractYouTubeID(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "Standard watch URL",
			url:      "https://www.youtube.com/watch?v=abc123",
			expected: "abc123",
		},
		{
			name:     "Watch URL with params",
			url:      "https://www.youtube.com/watch?v=abc123&t=10&list=xyz",
			expected: "abc123",
		},
		{
			name:     "Short URL",
			url:      "https://youtu.be/abc123",
			expected: "abc123",
		},
		{
			name:     "Short URL with params",
			url:      "https://youtu.be/abc123?feature=share",
			expected: "abc123",
		},
		{
			name:     "Embed URL",
			url:      "https://www.youtube.com/embed/abc123",
			expected: "abc123",
		},
		{
			name:     "Embed URL with params",
			url:      "https://www.youtube.com/embed/abc123?autoplay=1",
			expected: "abc123",
		},
		{
			name:     "No ID found",
			url:      "https://www.youtube.com/channel/abc",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractYouTubeID(tt.url)
			if result != tt.expected {
				t.Errorf("extractYouTubeID(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

// --- extractTikTokID Tests ---

func TestExtractTikTokID(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "Standard video URL",
			url:      "https://www.tiktok.com/@user/video/7123456789012345678",
			expected: "7123456789012345678",
		},
		{
			name:     "Video URL with query",
			url:      "https://www.tiktok.com/@user/video/7123456789012345678?lang=en",
			expected: "7123456789012345678",
		},
		{
			name:     "Short URL",
			url:      "https://vm.tiktok.com/abc123",
			expected: "abc123",
		},
		{
			name:     "Short URL with trailing slash",
			url:      "https://vm.tiktok.com/xyz789/",
			expected: "", // Short URLs without /video/ pattern return empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTikTokID(tt.url)
			if result != tt.expected {
				t.Errorf("extractTikTokID(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

// --- NewDownloader Tests ---

func TestNewDownloader(t *testing.T) {
	tests := []struct {
		name        string
		outputDir   string
		expectedDir string
	}{
		{
			name:        "Custom output directory",
			outputDir:   "/tmp/test-downloads",
			expectedDir: "/tmp/test-downloads",
		},
		{
			name:        "Empty output directory defaults",
			outputDir:   "",
			expectedDir: "/tmp/velox/downloads",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDownloader(tt.outputDir)
			if d.outputDir != tt.expectedDir {
				t.Errorf("NewDownloader(%q).outputDir = %q, want %q",
					tt.outputDir, d.outputDir, tt.expectedDir)
			}
		})
	}
}

// --- Platform Constants Tests ---

func TestPlatformConstants(t *testing.T) {
	if PlatformYouTube != "youtube" {
		t.Errorf("PlatformYouTube = %q, want %q", PlatformYouTube, "youtube")
	}
	if PlatformTikTok != "tiktok" {
		t.Errorf("PlatformTikTok = %q, want %q", PlatformTikTok, "tiktok")
	}
}

// --- GetPlatformFolder Tests ---

func TestGetPlatformFolder(t *testing.T) {
	d := NewDownloader("/tmp/test-platform")

	tests := []struct {
		platform Platform
		expected string
	}{
		{PlatformYouTube, "/tmp/test-platform/youtube"},
		{PlatformTikTok, "/tmp/test-platform/tiktok"},
	}

	for _, tt := range tests {
		t.Run(string(tt.platform), func(t *testing.T) {
			result := d.GetPlatformFolder(tt.platform)
			if result != tt.expected {
				t.Errorf("GetPlatformFolder(%q) = %q, want %q", tt.platform, result, tt.expected)
			}
		})
	}
}

// --- DownloadResult Tests ---

func TestDownloadResultFields(t *testing.T) {
	result := DownloadResult{
		Platform:  PlatformYouTube,
		VideoID:   "abc123",
		Title:     "Test Video",
		FilePath:  "/tmp/video.mp4",
		Duration:  120.5,
		Thumbnail: "https://example.com/thumb.jpg",
		Author:    "TestAuthor",
	}

	if result.Platform != PlatformYouTube {
		t.Errorf("Platform = %q, want %q", result.Platform, PlatformYouTube)
	}
	if result.VideoID != "abc123" {
		t.Errorf("VideoID = %q, want %q", result.VideoID, "abc123")
	}
	if result.Title != "Test Video" {
		t.Errorf("Title = %q, want %q", result.Title, "Test Video")
	}
	if result.Duration != 120.5 {
		t.Errorf("Duration = %f, want %f", result.Duration, 120.5)
	}
}

// --- VideoInfo Tests ---

func TestVideoInfoStruct(t *testing.T) {
	info := VideoInfo{
		ID:        "abc123",
		Title:     "Test",
		Duration:  60.0,
		Uploader:  "Uploader",
		Thumbnail: "https://example.com/thumb.jpg",
	}

	if info.ID != "abc123" {
		t.Errorf("ID = %q, want %q", info.ID, "abc123")
	}
	if info.Title != "Test" {
		t.Errorf("Title = %q, want %q", info.Title, "Test")
	}
	if info.Duration != 60.0 {
		t.Errorf("Duration = %f, want %f", info.Duration, 60.0)
	}
}

// --- Unsupported URL Download Test ---

func TestDownloadUnsupportedPlatform(t *testing.T) {
	d := NewDownloader("/tmp/test-unsupported")

	// This should return an error immediately without calling yt-dlp
	ctx := context.Background()
	_, err := d.Download(ctx, "https://example.com/video.mp4")
	if err == nil {
		t.Error("expected error for unsupported URL, got nil")
	}
}
