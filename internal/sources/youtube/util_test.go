package youtube

import (
	"testing"
)

// ===== getGroupFromDestination tests =====

func TestGetGroupFromDestination_Nil(t *testing.T) {
	if got := getGroupFromDestination(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}

func TestGetGroupFromDestination_WithGroup(t *testing.T) {
	d := &DestinationRequest{Group: "Discovery"}
	if got := getGroupFromDestination(d); got != "Discovery" {
		t.Fatalf("expected 'Discovery', got %q", got)
	}
}

func TestGetGroupFromDestination_EmptyGroup(t *testing.T) {
	d := &DestinationRequest{}
	if got := getGroupFromDestination(d); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// ===== boolDefault tests =====

func TestBoolDefault_Nil(t *testing.T) {
	if got := boolDefault(nil, true); got != true {
		t.Fatalf("expected true (default), got %v", got)
	}
	if got := boolDefault(nil, false); got != false {
		t.Fatalf("expected false (default), got %v", got)
	}
}

func TestBoolDefault_SetTrue(t *testing.T) {
	v := true
	if got := boolDefault(&v, false); got != true {
		t.Fatalf("expected true (set), got %v", got)
	}
}

func TestBoolDefault_SetFalse(t *testing.T) {
	v := false
	if got := boolDefault(&v, true); got != false {
		t.Fatalf("expected false (set), got %v", got)
	}
}

// ===== parseTimestamp tests =====

func TestParseTimestamp_SecondsOnly(t *testing.T) {
	got, err := parseTimestamp("45")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 45 {
		t.Fatalf("expected 45, got %d", got)
	}
}

func TestParseTimestamp_MinutesSeconds(t *testing.T) {
	got, err := parseTimestamp("2:30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 150 {
		t.Fatalf("expected 150, got %d", got)
	}
}

func TestParseTimestamp_HoursMinutesSeconds(t *testing.T) {
	got, err := parseTimestamp("1:23:45")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 5025 {
		t.Fatalf("expected 5025, got %d", got)
	}
}

func TestParseTimestamp_Empty(t *testing.T) {
	_, err := parseTimestamp("")
	if err == nil {
		t.Fatal("expected error for empty timestamp")
	}
}

func TestParseTimestamp_Invalid(t *testing.T) {
	_, err := parseTimestamp("abc")
	if err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

func TestParseTimestamp_EdgeZero(t *testing.T) {
	got, err := parseTimestamp("0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestParseTimestamp_LeadingZeros(t *testing.T) {
	got, err := parseTimestamp("00:05:00")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 300 {
		t.Fatalf("expected 300, got %d", got)
	}
}

func TestParseTimestamp_Whitespace(t *testing.T) {
	got, err := parseTimestamp("  10:30  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 630 {
		t.Fatalf("expected 630, got %d", got)
	}
}

func TestParseTimestamp_FourParts(t *testing.T) {
	_, err := parseTimestamp("1:2:3:4")
	if err == nil {
		t.Fatal("expected error for 4-part timestamp")
	}
}

// ===== extractVideoID tests =====

func TestYouTubeExtractVideoID_StandardWatch(t *testing.T) {
	got := extractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if got != "dQw4w9WgXcQ" {
		t.Fatalf("expected dQw4w9WgXcQ, got %q", got)
	}
}

func TestYouTubeExtractVideoID_ShortURL(t *testing.T) {
	got := extractVideoID("https://youtu.be/dQw4w9WgXcQ")
	if got != "dQw4w9WgXcQ" {
		t.Fatalf("expected dQw4w9WgXcQ, got %q", got)
	}
}

func TestYouTubeExtractVideoID_ShortsURL(t *testing.T) {
	got := extractVideoID("https://www.youtube.com/shorts/abc123")
	if got != "abc123" {
		t.Fatalf("expected abc123, got %q", got)
	}
}

func TestYouTubeExtractVideoID_EmbedURL(t *testing.T) {
	got := extractVideoID("https://www.youtube.com/embed/dQw4w9WgXcQ")
	if got != "dQw4w9WgXcQ" {
		t.Fatalf("expected dQw4w9WgXcQ, got %q", got)
	}
}

func TestYouTubeExtractVideoID_LiveURL(t *testing.T) {
	got := extractVideoID("https://www.youtube.com/live/dQw4w9WgXcQ")
	if got != "dQw4w9WgXcQ" {
		t.Fatalf("expected dQw4w9WgXcQ, got %q", got)
	}
}

func TestYouTubeExtractVideoID_MobilePrefix(t *testing.T) {
	got := extractVideoID("https://m.youtube.com/watch?v=abc123")
	if got != "abc123" {
		t.Fatalf("expected abc123, got %q", got)
	}
}

func TestYouTubeExtractVideoID_InvalidURL(t *testing.T) {
	got := extractVideoID("")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestYouTubeExtractVideoID_NonYouTubeURL(t *testing.T) {
	got := extractVideoID("https://example.com/video")
	if got != "" {
		t.Fatalf("expected empty for non-youtube URL, got %q", got)
	}
}

func TestYouTubeExtractVideoID_ShortsWithExtraParams(t *testing.T) {
	got := extractVideoID("https://www.youtube.com/shorts/abc123?feature=share")
	if got != "abc123" {
		t.Fatalf("expected abc123, got %q", got)
	}
}

// ===== canonicalYouTubeURL tests =====

func TestCanonicalYouTubeURL_YoutubeCom(t *testing.T) {
	got := canonicalYouTubeURL("https://www.youtube.com/watch?v=abc123", "abc123")
	if got != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("expected canonical URL, got %q", got)
	}
}

func TestCanonicalYouTubeURL_YouTuBe(t *testing.T) {
	got := canonicalYouTubeURL("https://youtu.be/abc123", "abc123")
	if got != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("expected canonical URL, got %q", got)
	}
}

func TestCanonicalYouTubeURL_NonYouTube(t *testing.T) {
	got := canonicalYouTubeURL("https://example.com/video", "abc123")
	if got != "" {
		t.Fatalf("expected empty for non-youtube URL, got %q", got)
	}
}

func TestCanonicalYouTubeURL_EmptyVideoID(t *testing.T) {
	got := canonicalYouTubeURL("https://www.youtube.com/watch?v=abc123", "")
	if got != "" {
		t.Fatalf("expected empty when videoID is empty, got %q", got)
	}
}

func TestCanonicalYouTubeURL_InvalidURL(t *testing.T) {
	got := canonicalYouTubeURL("://invalid", "abc123")
	// url.Parse of "://invalid" returns an error, so it returns ""
	if got != "" {
		t.Fatalf("expected empty for invalid URL, got %q", got)
	}
}

func TestCanonicalYouTubeURL_CaseInsensitive(t *testing.T) {
	got := canonicalYouTubeURL("https://WWW.YOUTUBE.COM/watch?v=abc123", "abc123")
	if got != "https://www.youtube.com/watch?v=abc123" {
		t.Fatalf("expected canonical URL, got %q", got)
	}
}
