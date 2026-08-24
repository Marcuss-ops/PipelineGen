package assets

import (
	"strings"
	"testing"
	"time"
)

// ── Test 1: valid YouTube watch URL extracts videoID ─────────────────────

func TestResolveClipMetadata_ValidWatchURL_ExtractsVideoID(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md.VideoID != "dQw4w9WgXcQ" {
		t.Errorf("expected videoID dQw4w9WgXcQ, got %q", md.VideoID)
	}
	if md.Name != "dQw4w9WgXcQ" {
		t.Errorf("expected name fallback to videoID, got %q", md.Name)
	}
	if md.Source != "youtube-manual" {
		t.Errorf("expected default source youtube-manual, got %q", md.Source)
	}
}

// ── Test 2: youtu.be short URL extracts videoID ─────────────────────────

func TestResolveClipMetadata_YoutuBeShortURL_ExtractsVideoID(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL: "https://youtu.be/dQw4w9WgXcQ?t=30",
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md.VideoID != "dQw4w9WgXcQ" {
		t.Errorf("expected videoID dQw4w9WgXcQ, got %q", md.VideoID)
	}
}

// ── Test 3: invalid/non-YouTube URL falls back to timestamp-based ID ─────

func TestResolveClipMetadata_InvalidURL_FallsBackToTimestamp(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL: "not-a-valid-youtube-url",
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	// The videoID should be a numeric timestamp (nanos), not "not-a-valid-youtube-url".
	if md.VideoID == "" || strings.HasPrefix(md.VideoID, "not") {
		t.Errorf("expected numeric-timestamp fallback videoID, got %q", md.VideoID)
	}
	// rawURL should be rebuilt to canonical form with v= parameter.
	wantPrefix := "https://www.youtube.com/watch?v="
	if !strings.HasPrefix(md.RawURL, wantPrefix) {
		t.Errorf("expected rawURL to start with %q, got %q", wantPrefix, md.RawURL)
	}
}

// ── Test 4: name fallback chain (cmd.Name → fetched.Name → videoID) ─────

func TestResolveClipMetadata_NameFallbackChain(t *testing.T) {
	// Case A: explicit name wins.
	cmd := ResolveMetadataCommand{
		URL:         "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name:        "My Custom Title",
		FetchedName: "Rick Astley - Never Gonna Give You Up",
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md.Name != "My Custom Title" {
		t.Errorf("expected explicit name, got %q", md.Name)
	}

	// Case B: no explicit name → fetched name wins.
	cmd2 := ResolveMetadataCommand{
		URL:         "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name:        "",
		FetchedName: "Rick Astley - Never Gonna Give You Up",
	}
	md2, err := ResolveClipMetadata(cmd2)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md2.Name != "Rick Astley - Never Gonna Give You Up" {
		t.Errorf("expected fetched name, got %q", md2.Name)
	}

	// Case C: neither explicit nor fetched → videoID wins.
	cmd3 := ResolveMetadataCommand{
		URL:  "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name: "",
	}
	md3, err := ResolveClipMetadata(cmd3)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md3.Name != "dQw4w9WgXcQ" {
		t.Errorf("expected videoID fallback, got %q", md3.Name)
	}
}

// ── Test 5: description truncation (fetched description > 1000 chars) ────

func TestResolveClipMetadata_DescriptionTruncation(t *testing.T) {
	longDesc := strings.Repeat("x", 1500)
	cmd := ResolveMetadataCommand{
		URL:                "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		FetchedDescription: longDesc,
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(md.Description) > 1000 {
		t.Errorf("expected description truncated to ≤1000, got %d chars", len(md.Description))
	}
	// Short description passes through unchanged.
	cmd2 := ResolveMetadataCommand{
		URL:                "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Description:        "Short desc",
		FetchedDescription: longDesc,
	}
	md2, err := ResolveClipMetadata(cmd2)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md2.Description != "Short desc" {
		t.Errorf("expected explicit description to win, got %q", md2.Description)
	}
}

// ── Test 6: duration from fetched asset ──────────────────────────────────

func TestResolveClipMetadata_DurationFromFetched(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL:             "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		StartSec:        10,
		EndSec:          30,
		FetchedDuration: 213 * time.Second, // full video duration
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md.DurationSec != 213.0 {
		t.Errorf("expected durationSec 213.0 from fetched, got %.1f", md.DurationSec)
	}
	if md.Duration != 213 {
		t.Errorf("expected int duration 213, got %d", md.Duration)
	}
}

// ── Test 7: duration from segment delta when fetched is zero ─────────────

func TestResolveClipMetadata_DurationFromSegmentDelta(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		StartSec: 10,
		EndSec:   35,
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if md.DurationSec != 25.0 {
		t.Errorf("expected durationSec 25.0 from delta, got %.1f", md.DurationSec)
	}
}

// ── Test 8: start exceeds video duration ─────────────────────────────────

func TestResolveClipMetadata_StartExceedsDuration_ReturnsError(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL:             "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		StartSec:        120,
		EndSec:          150,
		FetchedDuration: 100 * time.Second, // full video is shorter than start
	}
	_, err := ResolveClipMetadata(cmd)
	if err == nil {
		t.Fatal("expected error when start exceeds video duration")
	}
	if !strings.Contains(err.Error(), "exceeds video duration") {
		t.Errorf("expected 'exceeds video duration' in error, got %v", err)
	}
}

// ── Test 9: start >= end returns error ───────────────────────────────────

func TestResolveClipMetadata_StartNotLessThanEnd_ReturnsError(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		StartSec: 50,
		EndSec:   30,
	}
	_, err := ResolveClipMetadata(cmd)
	if err == nil {
		t.Fatal("expected error when start >= end")
	}
	if !strings.Contains(err.Error(), "must be less than end") {
		t.Errorf("expected 'must be less than end' in error, got %v", err)
	}
}

// ── Test 10: negative timestamp returns error ────────────────────────────

func TestResolveClipMetadata_NegativeTimestamp_ReturnsError(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL:      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		StartSec: -1,
		EndSec:   30,
	}
	_, err := ResolveClipMetadata(cmd)
	if err == nil {
		t.Fatal("expected error for negative start")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Errorf("expected 'non-negative' in error, got %v", err)
	}
}

// ── Test 11: endSec == 0 with startSec > 0 is valid (full video from offset) ──

func TestResolveClipMetadata_EndZeroWithPositiveStart_Valid(t *testing.T) {
	cmd := ResolveMetadataCommand{
		URL:             "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		StartSec:        60,
		EndSec:          0,
		FetchedDuration: 213 * time.Second,
	}
	md, err := ResolveClipMetadata(cmd)
	if err != nil {
		t.Fatalf("endSec=0 should be valid (use full video), got error: %v", err)
	}
	if md.DurationSec != 213.0 {
		t.Errorf("expected duration from fetched, got %.1f", md.DurationSec)
	}
}
