package stockpipeline

import (
	"math"
	"testing"
)

// ===== Utility function tests =====

func TestFormatDuration_Zero(t *testing.T) {
	got := formatDuration(0)
	expected := "00:00:00.000"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatDuration_Negative(t *testing.T) {
	got := formatDuration(-5.0)
	expected := "00:00:00.000"
	if got != expected {
		t.Fatalf("expected %q for negative, got %q", expected, got)
	}
}

func TestFormatDuration_SecondsOnly(t *testing.T) {
	got := formatDuration(45.0)
	expected := "00:00:45.000"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatDuration_MinutesAndSeconds(t *testing.T) {
	got := formatDuration(125.0)
	expected := "00:02:05.000"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatDuration_Hours(t *testing.T) {
	got := formatDuration(3661.5)
	expected := "01:01:01.500"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatDuration_Milliseconds(t *testing.T) {
	got := formatDuration(1.123)
	expected := "00:00:01.123"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatDuration_Large(t *testing.T) {
	got := formatDuration(10000.0)
	expected := "02:46:40.000"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestFormatDuration_MillisecondsTruncation(t *testing.T) {
	got := formatDuration(math.Pi)
	// Pi seconds = 00:00:03.141
	expected := "00:00:03.141"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestExtractVideoID_StandardURL(t *testing.T) {
	// stockpipeline's extractVideoID is simple string-based, not URL-aware
	got := extractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	// The simple implementation falls through to last path segment
	if got == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestExtractVideoID_WithExtraParams(t *testing.T) {
	got := extractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=120s&feature=shared")
	// Falls through to last path segment
	if got != "watch?v=dQw4w9WgXcQ&t=120s&feature=shared" {
		t.Fatalf("expected last path segment, got %q", got)
	}
}

func TestExtractVideoID_ShortURL(t *testing.T) {
	got := extractVideoID("https://youtu.be/dQw4w9WgXcQ")
	expected := "dQw4w9WgXcQ"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestExtractVideoID_PlainString(t *testing.T) {
	got := extractVideoID("just a string/with/slashes")
	expected := "slashes"
	if got != expected {
		t.Fatalf("expected %q (last path segment), got %q", expected, got)
	}
}

func TestExtractVideoID_EmptyURL(t *testing.T) {
	got := extractVideoID("")
	expected := ""
	if got != expected {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestSlugify_BasicString(t *testing.T) {
	got := slugify("Hello World")
	expected := "hello_world"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_TruncatesLong(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz0123456789_extra_characters_here"
	got := slugify(long)
	if len(got) > 40 {
		t.Fatalf("slug too long: %d chars (%q)", len(got), got)
	}
}

func TestSlugify_SpecialChars(t *testing.T) {
	got := slugify("Cus D'Amato!")
	expected := "cus_d_amato"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_TrimsUnderscores(t *testing.T) {
	got := slugify("__hello__")
	expected := "hello"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_Numbers(t *testing.T) {
	got := slugify("video 123 clip")
	expected := "video_123_clip"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_DigitsAndHyphens(t *testing.T) {
	got := slugify("test-clip_v2")
	expected := "test-clip_v2"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_LongTruncation(t *testing.T) {
	got := slugify("this-is-a-very-long-string-that-should-be-truncated-to-forty-chars")
	if len(got) != 40 {
		t.Fatalf("expected 40 chars, got %d: %q", len(got), got)
	}
}

func TestSlugify_EmptyInput(t *testing.T) {
	got := slugify("")
	expected := ""
	if got != expected {
		t.Fatalf("expected empty, got %q", got)
	}
}
