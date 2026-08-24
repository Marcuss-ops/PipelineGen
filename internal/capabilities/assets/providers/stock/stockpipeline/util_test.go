package assets

import (
	"math"
	"testing"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
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
	got := extractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	expected := "dQw4w9WgXcQ"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestExtractVideoID_WithExtraParams(t *testing.T) {
	got := extractVideoID("https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=120s&feature=shared")
	expected := "dQw4w9WgXcQ"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
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
	got := textutil.Slugify("Hello World")
	expected := "hello-world"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_TruncatesLong(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz0123456789_extra_characters_here"
	got := textutil.SlugifyWithMax(long, 40)
	if len(got) > 40 {
		t.Fatalf("slug too long: %d chars (%q)", len(got), got)
	}
}

func TestSlugify_SpecialChars(t *testing.T) {
	got := textutil.Slugify("Cus D'Amato!")
	expected := "cus-d-amato"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_TrimsUnderscores(t *testing.T) {
	got := textutil.Slugify("__hello__")
	expected := "hello"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_Numbers(t *testing.T) {
	got := textutil.Slugify("video 123 clip")
	expected := "video-123-clip"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_DigitsAndHyphens(t *testing.T) {
	got := textutil.Slugify("test-clip_v2")
	expected := "test-clip-v2"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestSlugify_LongTruncation(t *testing.T) {
	got := textutil.SlugifyWithMax("this-is-a-very-long-string-that-should-be-truncated-to-forty-chars", 40)
	if len(got) != 40 {
		t.Fatalf("expected 40 chars, got %d: %q", len(got), got)
	}
}

func TestSlugify_EmptyInput(t *testing.T) {
	got := textutil.Slugify("")
	expected := ""
	if got != expected {
		t.Fatalf("expected empty, got %q", got)
	}
}
