// Package delivery — TDD test surface for ResolveDriveDestination.
// PR-CANONICAL-DRIVE-DESTINATION (July 2026).
//
// Each test exercises one aspect of the canonical resolver:
// per-domain derivation, cascade fallback chains, helper functions,
// and edge cases (empty/nil inputs, punctuation-only slugs, etc.).
package delivery

import (
	"testing"
	"time"
)

// ── ResolveDriveDestination: Stock pipeline ───────────────────────────

func TestResolveDriveDestination_Stock_HappyPath(t *testing.T) {
	in := DriveDestinationInput{
		Category:       "Boxe",
		Subject:        "pacquiao-broner",
		Provider:       "pexels",
		RunFolderName:  "Pacquiao Vs Broner",
		ClipSlug:       "round-7-broner-barcolla",
		ClipTitle:      "Round 7 - Broner barcolla",
		ClipStartSec:   32,
		ClipEndSec:     51,
		RunContextTime: time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
	}
	dest := ResolveDriveDestination(in)

	// LocationInput must carry the canonical semantic fields
	if dest.LocationInput.Category != "Boxe" {
		t.Errorf("LocationInput.Category = %q, want %q", dest.LocationInput.Category, "Boxe")
	}
	if dest.LocationInput.Subject != "pacquiao-broner" {
		t.Errorf("LocationInput.Subject = %q, want %q", dest.LocationInput.Subject, "pacquiao-broner")
	}
	if dest.LocationInput.Provider != "pexels" {
		t.Errorf("LocationInput.Provider = %q, want %q", dest.LocationInput.Provider, "pexels")
	}

	// RootFolderName from RunFolderName (highest priority).
	// pathutil.SafeFolderName preserves spaces — only unsafe chars
	// (like /, :, etc.) are replaced with underscores.
	if dest.RootFolderName != "Pacquiao Vs Broner" {
		t.Errorf("RootFolderName = %q, want %q", dest.RootFolderName, "Pacquiao Vs Broner")
	}

	// PathLeafName from ClipSlug (highest priority)
	if dest.PathLeafName != "round-7-broner-barcolla" {
		t.Errorf("PathLeafName = %q, want %q", dest.PathLeafName, "round-7-broner-barcolla")
	}

	// ParentFolderID should be empty (no override set)
	if dest.ParentFolderID != "" {
		t.Errorf("ParentFolderID = %q, want empty", dest.ParentFolderID)
	}
}

func TestResolveDriveDestination_Stock_RootFolderNameCascade(t *testing.T) {
	tests := []struct {
		name string
		in   DriveDestinationInput
		want string
	}{
		{
			name: "RunFolderName wins over all",
			in: DriveDestinationInput{
				RunFolderName:    "Best-Folder",
				RunSubfolder:     "Sub-Folder",
				RunSearchQueries: []string{"boxing gym"},
				RunDirectURLs:    []string{"https://example.com/video.mp4"},
				RunContextTime:   time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			},
			want: "Best-Folder",
		},
		{
			name: "RunSubfolder when RunFolderName empty",
			in: DriveDestinationInput{
				RunSubfolder:   "Sub-Folder",
				RunContextTime: time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			},
			want: "Sub-Folder",
		},
		{
			name: "RunSearchQueries[0] when both empty (spaces preserved by SafeFolderName)",
			in: DriveDestinationInput{
				RunSearchQueries: []string{"boxing training gym"},
				RunContextTime:   time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			},
			want: "boxing training gym",
		},
		{
			name: "RunDirectURLs[0] basename when all empty",
			in: DriveDestinationInput{
				RunDirectURLs:  []string{"https://example.com/my-video.mp4?quality=hd"},
				RunContextTime: time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC),
			},
			want: "my-video",
		},
		{
			name: "Date fallback when everything else empty",
			in: DriveDestinationInput{
				RunContextTime: time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
			},
			want: "stock_2026-07-08",
		},
		{
			name: "Empty when all inputs empty (no date fallback)",
			in:   DriveDestinationInput{},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dest := ResolveDriveDestination(tc.in)
			if dest.RootFolderName != tc.want {
				t.Errorf("RootFolderName = %q, want %q", dest.RootFolderName, tc.want)
			}
		})
	}
}

func TestResolveDriveDestination_Stock_PathLeafNameCascade(t *testing.T) {
	tests := []struct {
		name string
		in   DriveDestinationInput
		want string
	}{
		{
			name: "ClipSlug wins over title",
			in: DriveDestinationInput{
				ClipSlug:  "round-1",
				ClipTitle: "Some Title",
			},
			want: "round-1",
		},
		{
			name: "Title-derived slug when ClipSlug empty",
			in: DriveDestinationInput{
				ClipTitle: "Round 7 - Broner barcolla",
			},
			want: "round-7-broner-barcolla",
		},
		{
			name: "Start-end literal when both empty",
			in: DriveDestinationInput{
				ClipStartSec: 32,
				ClipEndSec:   51,
			},
			want: "00-00-32_to_00-00-51",
		},
		{
			name: "Empty when all inputs zero",
			in:   DriveDestinationInput{},
			want: "",
		},
		{
			name: "Pure-punctuation slug falls through to title",
			in: DriveDestinationInput{
				ClipSlug:  "///",
				ClipTitle: "Real Title",
			},
			want: "real-title",
		},
		{
			name: "Pure-punctuation slug with empty title falls through to time range",
			in: DriveDestinationInput{
				ClipSlug:     "!!!",
				ClipStartSec: 100,
				ClipEndSec:   200,
			},
			want: "00-01-40_to_00-03-20",
		},
		{
			name: "'untitled' slug rejected, falls through to title",
			in: DriveDestinationInput{
				ClipSlug:  "   ", // SafeFolderName of whitespace → "untitled"
				ClipTitle: "Boxing Round 3",
			},
			want: "boxing-round-3",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dest := ResolveDriveDestination(tc.in)
			if dest.PathLeafName != tc.want {
				t.Errorf("PathLeafName = %q, want %q", dest.PathLeafName, tc.want)
			}
		})
	}
}

// ── ResolveDriveDestination: YouTube pipeline ─────────────────────────

func TestResolveDriveDestination_YouTube_HappyPath(t *testing.T) {
	in := DriveDestinationInput{
		Category:     "Boxing-Legends-Channel",
		Subject:      "vdC5GXxS-qU",
		Name:         "Pacquiao vs Broner",
		ClipTitle:    "Sfuriata contro Pacquiao",
		ClipStartSec: 146,
		ClipEndSec:   155,
	}
	dest := ResolveDriveDestination(in)

	if dest.LocationInput.Category != "Boxing-Legends-Channel" {
		t.Errorf("Category = %q, want %q", dest.LocationInput.Category, "Boxing-Legends-Channel")
	}
	if dest.LocationInput.Subject != "vdC5GXxS-qU" {
		t.Errorf("Subject = %q, want %q", dest.LocationInput.Subject, "vdC5GXxS-qU")
	}
	if dest.LocationInput.Name != "Pacquiao vs Broner" {
		t.Errorf("Name = %q, want %q", dest.LocationInput.Name, "Pacquiao vs Broner")
	}
	// YouTube doesn't populate RunFolderName → RootFolderName empty
	if dest.RootFolderName != "" {
		t.Errorf("RootFolderName = %q, want empty (YouTube has no run-level context)", dest.RootFolderName)
	}
	// PathLeafName from ClipTitle slug
	if dest.PathLeafName != "sfuriata-contro-pacquiao" {
		t.Errorf("PathLeafName = %q, want %q", dest.PathLeafName, "sfuriata-contro-pacquiao")
	}
}

// ── ResolveDriveDestination: Voiceover pipeline ───────────────────────

func TestResolveDriveDestination_Voiceover_ProjectAndLanguage(t *testing.T) {
	in := DriveDestinationInput{
		Project:  "mike-tyson-documentary",
		Language: "it-IT",
	}
	dest := ResolveDriveDestination(in)

	if dest.LocationInput.Project != "mike-tyson-documentary" {
		t.Errorf("Project = %q, want %q", dest.LocationInput.Project, "mike-tyson-documentary")
	}
	if dest.LocationInput.Language != "it-IT" {
		t.Errorf("Language = %q, want %q", dest.LocationInput.Language, "it-IT")
	}
	// Voiceover doesn't populate Run/Clip fields
	if dest.RootFolderName != "" {
		t.Errorf("RootFolderName = %q, want empty", dest.RootFolderName)
	}
	if dest.PathLeafName != "" {
		t.Errorf("PathLeafName = %q, want empty", dest.PathLeafName)
	}
}

// ── DriveFolderOverride ───────────────────────────────────────────────

func TestResolveDriveDestination_ParentFolderID_PassedThrough(t *testing.T) {
	in := DriveDestinationInput{
		ParentFolderID: "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2wtIs",
	}
	dest := ResolveDriveDestination(in)
	if dest.ParentFolderID != "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgVE2wtIs" {
		t.Errorf("ParentFolderID = %q, want passthrough", dest.ParentFolderID)
	}
}

// ── Edge cases ────────────────────────────────────────────────────────

func TestResolveDriveDestination_EmptyInput_ProducesEmptyDestination(t *testing.T) {
	dest := ResolveDriveDestination(DriveDestinationInput{})
	if !dest.LocationInput.IsEmpty() {
		t.Errorf("LocationInput should be IsEmpty() for zero-value input")
	}
	if dest.RootFolderName != "" {
		t.Errorf("RootFolderName should be empty")
	}
	if dest.PathLeafName != "" {
		t.Errorf("PathLeafName should be empty")
	}
	if dest.ParentFolderID != "" {
		t.Errorf("ParentFolderID should be empty")
	}
}

func TestResolveDriveDestination_WhitespaceFields_TrimmedToEmpty(t *testing.T) {
	in := DriveDestinationInput{
		Category:  "   ",
		Subject:   "   ",
		ClipSlug:  "   ",
		ClipTitle: "   ",
	}
	dest := ResolveDriveDestination(in)
	if dest.LocationInput.Category != "" {
		t.Errorf("Category should be trimmed to empty")
	}
	if dest.LocationInput.Subject != "" {
		t.Errorf("Subject should be trimmed to empty")
	}
	if dest.PathLeafName != "" {
		t.Errorf("PathLeafName should be empty for whitespace-only inputs")
	}
}

// ── Determinism (same input → same output) ────────────────────────────

func TestResolveDriveDestination_DeterministicAcrossCalls(t *testing.T) {
	in := DriveDestinationInput{
		Category:       "Boxe",
		Subject:        "round-7",
		RunFolderName:  "Pacquiao Vs Broner",
		ClipSlug:       "round-7-broner-barcolla",
		RunContextTime: time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC),
	}
	a := ResolveDriveDestination(in)
	b := ResolveDriveDestination(in)
	if a.RootFolderName != b.RootFolderName {
		t.Errorf("RootFolderName drift: %q vs %q", a.RootFolderName, b.RootFolderName)
	}
	if a.PathLeafName != b.PathLeafName {
		t.Errorf("PathLeafName drift: %q vs %q", a.PathLeafName, b.PathLeafName)
	}
	if a.LocationInput.Category != b.LocationInput.Category {
		t.Errorf("Category drift: %q vs %q", a.LocationInput.Category, b.LocationInput.Category)
	}
}

// ── Helper function tests ─────────────────────────────────────────────

func TestSanitizedFolderName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"spaces preserved by SafeFolderName", "Pacquiao Vs Broner", "Pacquiao Vs Broner"},
		{"trimmed", "  trimmed  ", "trimmed"},
		{"empty returns empty (not 'untitled')", "", ""},
		{"whitespace-only returns empty", "   ", ""},
		{"slash replaced with underscore", "hello/world", "hello_world"},
		{"colon replaced with underscore", "Round 5: Broner", "Round 5_ Broner"},
		{"underscore preserved", "Round_7_Broner", "Round_7_Broner"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizedFolderName(tc.in)
			if got != tc.want {
				t.Errorf("sanitizedFolderName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFirstSanitizedQuery(t *testing.T) {
	if got := firstSanitizedQuery([]string{"boxing training", "gym"}); got != "boxing training" {
		t.Errorf("got %q, want %q", got, "boxing training")
	}
	if got := firstSanitizedQuery([]string{"", "  ", "gym"}); got != "gym" {
		t.Errorf("got %q, want %q", got, "gym")
	}
	if got := firstSanitizedQuery(nil); got != "" {
		t.Errorf("got %q, want empty for nil", got)
	}
}

func TestSanitizedURLBasename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips query param", "https://example.com/my-video.mp4?quality=hd", "my-video"},
		{"strips fragment", "https://example.com/path/to/clip.mov#t=10", "clip"},
		{"empty string", "", ""},
		{"root path slash only", "https://example.com/", "_"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizedURLBasename(tc.in)
			if got != tc.want {
				t.Errorf("sanitizedURLBasename(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatTimeRange(t *testing.T) {
	got := formatTimeRange(32, 51)
	want := "00-00-32_to_00-00-51"
	if got != want {
		t.Errorf("formatTimeRange(32, 51) = %q, want %q", got, want)
	}
	got = formatTimeRange(3720, 3780) // 1h2m0s to 1h3m0s
	want = "01-02-00_to_01-03-00"
	if got != want {
		t.Errorf("formatTimeRange(3720, 3780) = %q, want %q", got, want)
	}
}

func TestContainsAlphanumeric(t *testing.T) {
	if !containsAlphanumeric("round-1") {
		t.Error("expected true for 'round-1'")
	}
	if containsAlphanumeric("___") {
		t.Error("expected false for '___'")
	}
	if containsAlphanumeric("---...") {
		t.Error("expected false for '---...'")
	}
	if !containsAlphanumeric("a") {
		t.Error("expected true for 'a'")
	}
	if !containsAlphanumeric("1") {
		t.Error("expected true for '1'")
	}
}
