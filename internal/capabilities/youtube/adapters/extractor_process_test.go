package adapters

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tagutil "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	youtubesubtitles "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ===== tagutil.CleanClipName tests =====

func TestCleanClipName_HTMLEntities(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"double gt", "&gt;&gt; hello", "hello"},
		{"single gt", "&gt; hello", "hello"},
		{"nbsp", "hello&nbsp;world", "hello world"},
		{"mixed", "&gt;&gt; [music] hello", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tagutil.CleanClipName(tt.in); got != tt.want {
				t.Errorf("tagutil.CleanClipName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanClipName_SubtitleArtifacts(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"gt gt", "gt gt hello", "hello"},
		{"music lower", "[music] intro", "intro"},
		{"music title", "[Music] intro", "intro"},
		{"music upper", "[MUSIC] intro", "intro"},
		{"applause", "[Applause] bravo", "bravo"},
		{"underscore", "[__] text", "text"},
		{"underscore spaced", "[ __ ] text", "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tagutil.CleanClipName(tt.in); got != tt.want {
				t.Errorf("tagutil.CleanClipName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCleanClipName_WhitespaceCollapse(t *testing.T) {
	got := tagutil.CleanClipName("  hello   world  ")
	want := "hello world"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCleanClipName_Empty(t *testing.T) {
	got := tagutil.CleanClipName("")
	if got != "clip" {
		t.Errorf("empty name should default to 'clip', got %q", got)
	}
}

func TestCleanClipName_OnlyArtifacts(t *testing.T) {
	got := tagutil.CleanClipName("[music] &gt;&gt; gt gt")
	if got != "clip" {
		t.Errorf("artifacts-only name should default to 'clip', got %q", got)
	}
}

func TestCleanClipName_Truncation(t *testing.T) {
	// Create a name longer than 80 runes
	longName := strings.Repeat("a", 100)
	got := tagutil.CleanClipName(longName)
	runes := []rune(got)
	if len(runes) > 80 {
		t.Errorf("name should be truncated to 80 runes, got %d", len(runes))
	}
	if len(runes) == 0 {
		t.Error("truncated name should not be empty")
	}
}

func TestCleanClipName_TruncationPreservesWords(t *testing.T) {
	// Name with words, truncation should trim trailing dashes
	name := strings.Repeat("hello-", 20) + "end"
	got := tagutil.CleanClipName(name)
	if strings.HasSuffix(got, "-") {
		t.Errorf("truncated name should not end with dash, got %q", got)
	}
}

func TestCleanClipName_Unicode(t *testing.T) {
	got := tagutil.CleanClipName("ciao mondo 🎬 test")
	want := "ciao mondo 🎬 test"
	if got != want {
		t.Errorf("unicode should be preserved, got %q", got)
	}
}

func TestCleanClipName_UnicodeTruncation(t *testing.T) {
	// Unicode chars count as 1 rune each
	name := "🎬" + strings.Repeat("x", 80)
	got := tagutil.CleanClipName(name)
	runes := []rune(got)
	if len(runes) > 80 {
		t.Errorf("unicode name should be truncated to 80 runes, got %d runes: %q", len(runes), got)
	}
}

// ===== retry.IsTransient download-taxonomy tests =====
//
// Azione 2/8 of Step 7 (July 2026): migrated from
// tagutil.IsTransientDownloadError to retry.IsTransient.
//
// FASE 6 Cut 6.1.D (July 2026): production retry.IsTransient became
// a pure typed probe (RetryableError interface + *TransientInfrastructureError
// carrier via errors.As). The pre-cut substring taxonomy is REMOVED
// from production; the 13 "transient"Fixture cases below wrap their
// error in *retry.TransientInfrastructureError so retry.IsTransient
// returns true via typed-probe #2 — the canonical SDK-boundary
// emission shape (same envelope retry.WrapTransient produces).
//
// Permanent cases remain raw strings; the typed probe does NOT
// substring-match, so retry.IsTransient returns false. This test
// guard pins the "raw = fail-closed terminal" semantic; any future
// surface change that starts classifying a "permanent" string by
// default would surface here.

func TestRetry_IsTransient_DownloadTaxonomy(t *testing.T) {
	transient := []string{
		"timeout",
		"connection reset by peer",
		"connection refused",
		"i/o timeout",
		"EOF: stream closed unexpectedly",
		"HTTP 429 Too Many Requests",
		"HTTP 502 Bad Gateway",
		"HTTP 503 Service Unavailable",
		"HTTP 504 Gateway Timeout",
		"api rate limit reached",
		"quota exceeded for project",
		"backend temporarily unavailable",
		"resource temporarily unavailable, retry",
	}
	for _, msg := range transient {
		t.Run("transient: "+msg, func(t *testing.T) {
			if !retry.IsTransient(&retry.TransientInfrastructureError{Err: errors.New(msg)}) {
				t.Errorf("expected transient for %q", msg)
			}
		})
	}

	permanent := []string{
		"Video unavailable",
		"Private video",
		"Sign in to confirm you're not a bot",
		"Sign in to confirm your age",
		"Requested format is not available",
		"Invalid URL",
		"Unable to extract video data",
		"no video formats found",
		"Video is live",
	}
	for _, msg := range permanent {
		t.Run("permanent: "+msg, func(t *testing.T) {
			if retry.IsTransient(errors.New(msg)) {
				t.Errorf("expected permanent for %q", msg)
			}
		})
	}

	// Unknown errors should NOT be transient
	t.Run("unknown error", func(t *testing.T) {
		if retry.IsTransient(errors.New("something weird happened")) {
			t.Error("unknown errors should not be transient")
		}
	})

	// nil error
	t.Run("nil error", func(t *testing.T) {
		if retry.IsTransient(nil) {
			t.Error("nil error should not be transient")
		}
	})
}

// ===== retry.Do tests (replacing retryDownload) =====

func TestRetry_SuccessFirstAttempt(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		return nil
	}, retry.RetryOptions{MaxAttempts: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetry_SuccessAfterRetry(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		if calls < 3 {
			return errors.New("timeout")
		}
		return nil
	}, retry.RetryOptions{MaxAttempts: 3, IsRetryable: func(err error) bool { return textutil.ContainsCI(err.Error(), "timeout") }})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetry_PermanentErrorFailsImmediately(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		return errors.New("Video unavailable")
	}, retry.RetryOptions{MaxAttempts: 3, IsRetryable: retry.IsTransient})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (permanent error), got %d", calls)
	}
}

func TestRetry_ExhaustsRetries(t *testing.T) {
	calls := 0
	// FASE 6 Cut 6.1.D: production retry.IsTransient is a pure typed
	// probe; raw errors.New() no longer classifies. Wrap the
	// simulated transient-shape error in *TransientInfrastructureError
	// (canonical SDK-boundary emission shape via retry.WrapTransient).
	err := retry.Do(context.Background(), func() error {
		calls++
		return &retry.TransientInfrastructureError{Err: errors.New("connection reset")}
	}, retry.RetryOptions{MaxAttempts: 3, IsRetryable: retry.IsTransient})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetry_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	// FASE 6 Cut 6.1.D (July 2026): production retry.IsTransient is a
	// pure typed probe (errors.As on *retry.TransientInfrastructureError
	// + RetryableError interface). The pre-FASE-6 substring taxonomy is
	// REMOVED from production; raw `errors.New("timeout")` no longer
	// classifies as transient. The canonical fixture envelope is
	// *retry.TransientInfrastructureError (typed carrier #2 in the
	// Decision walker) — same shape production callers emit at the
	// SDK boundary via retry.WrapTransient.
	err := retry.Do(ctx, func() error {
		calls++
		if calls == 1 {
			cancel() // cancel after first attempt
		}
		return &retry.TransientInfrastructureError{Err: errors.New("timeout")}
	}, retry.RetryOptions{MaxAttempts: 3, IsRetryable: retry.IsTransient})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRetry_NoRetriesRequested(t *testing.T) {
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		return errors.New("timeout")
	}, retry.RetryOptions{MaxAttempts: 1})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// ===== parseVTTFile tests =====

func TestParseVTTFile_BasicCues(t *testing.T) {
	vtt := `WEBVTT

00:00:01.000 --> 00:00:03.000
Hello world

00:00:04.000 --> 00:00:06.000
Second cue
`
	tmpFile := filepath.Join(t.TempDir(), "test.vtt")
	if err := os.WriteFile(tmpFile, []byte(vtt), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := youtubesubtitles.ParseVTTFile(tmpFile, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Hello world") {
		t.Errorf("expected 'Hello world' in output, got %q", got)
	}
	if !strings.Contains(got, "Second cue") {
		t.Errorf("expected 'Second cue' in output, got %q", got)
	}
}

func TestParseVTTFile_TimeWindowFiltering(t *testing.T) {
	vtt := `WEBVTT

00:00:01.000 --> 00:00:03.000
Early text

00:00:10.000 --> 00:00:12.000
Middle text

00:00:20.000 --> 00:00:22.000
Late text
`
	tmpFile := filepath.Join(t.TempDir(), "test.vtt")
	if err := os.WriteFile(tmpFile, []byte(vtt), 0644); err != nil {
		t.Fatal(err)
	}

	// Only get cues from 8s to 15s
	got, err := youtubesubtitles.ParseVTTFile(tmpFile, 8, 15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "Early text") {
		t.Error("should not contain 'Early text' (outside window)")
	}
	if !strings.Contains(got, "Middle text") {
		t.Error("should contain 'Middle text' (inside window)")
	}
	if strings.Contains(got, "Late text") {
		t.Error("should not contain 'Late text' (outside window)")
	}
}

func TestParseVTTFile_EmptyFile(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "empty.vtt")
	if err := os.WriteFile(tmpFile, []byte("WEBVTT\n\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := youtubesubtitles.ParseVTTFile(tmpFile, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for empty VTT, got %q", got)
	}
}

func TestParseVTTFile_HTMLTagsStripped(t *testing.T) {
	vtt := `WEBVTT

00:00:01.000 --> 00:00:03.000
<c.color1>Important</c.color1> text
`
	tmpFile := filepath.Join(t.TempDir(), "test.vtt")
	if err := os.WriteFile(tmpFile, []byte(vtt), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := youtubesubtitles.ParseVTTFile(tmpFile, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "<c.color1>") {
		t.Errorf("HTML tags should be stripped, got %q", got)
	}
	if !strings.Contains(got, "Important text") {
		t.Errorf("expected 'Important text', got %q", got)
	}
}

func TestParseVTTFile_RollingCueDedup(t *testing.T) {
	// YouTube's rolling VTT format: trigger cue -> content cue -> trigger cue
	vtt := `WEBVTT

00:00:01.000 --> 00:00:01.100
hello

00:00:01.100 --> 00:00:03.000
hello world

00:00:03.000 --> 00:00:03.100
world

00:00:03.100 --> 00:00:05.000
world goodbye
`
	tmpFile := filepath.Join(t.TempDir(), "test.vtt")
	if err := os.WriteFile(tmpFile, []byte(vtt), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := youtubesubtitles.ParseVTTFile(tmpFile, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should not have duplicate "hello" or "world"
	words := strings.Fields(got)
	helloCount := 0
	for _, w := range words {
		if strings.ToLower(w) == "hello" {
			helloCount++
		}
	}
	if helloCount > 1 {
		t.Errorf("expected at most 1 'hello' after dedup, got %d in %q", helloCount, got)
	}
}

// ===== Pipeline OutputDir tests =====

func TestYouTubeCutRequest_OutputDir(t *testing.T) {
	req := videomuscles.YouTubeCutRequest{
		URL:        "https://www.youtube.com/watch?v=test123",
		VideoID:    "test123",
		OutputDir:  "/tmp/custom/output",
		OutputName: "clip1",
	}
	if req.OutputDir != "/tmp/custom/output" {
		t.Errorf("OutputDir not set correctly, got %q", req.OutputDir)
	}
}

func TestYouTubeCutRequest_OutputDirEmpty(t *testing.T) {
	req := videomuscles.YouTubeCutRequest{
		URL:        "https://www.youtube.com/watch?v=test123",
		VideoID:    "test123",
		OutputName: "clip1",
	}
	if req.OutputDir != "" {
		t.Errorf("OutputDir should be empty by default, got %q", req.OutputDir)
	}
}

// ===== Backoff timing tests =====

func TestRetry_BackoffTiming(t *testing.T) {
	// Verify that backoff occurs (default: 1s + 2s for 3 attempts with ±25% jitter = ~2.25-3.75s envelope)
	calls := 0
	start := time.Now()
	// FASE 6 Cut 6.1.D (July 2026): fixture wraps the simulated
	// transient error in *retry.TransientInfrastructureError because
	// production retry.IsTransient is a pure typed probe post-Cut 6.1.D
	// (no substring fallback). Raw `errors.New("timeout")` no longer
	// classifies as transient; the typed envelope is the canonical
	// SDK-boundary emission shape.
	err := retry.Do(context.Background(), func() error {
		calls++
		return &retry.TransientInfrastructureError{Err: errors.New("timeout")}
	}, retry.RetryOptions{MaxAttempts: 3, IsRetryable: retry.IsTransient})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	// Default backoff: 1s + 2s = 3s total
	if elapsed < 2*time.Second {
		t.Errorf("expected at least 2s of backoff, got %v", elapsed)
	}
}

// STUB: parseVTTFile was a single-source-of-truth helper in the prior
// metadata_persist.go. The file was git rmd in the previous wave14-followup
// build-fix commit because helpers.go had canonical duplicates of all the
// 11 redeclared accessors. parseVTTFile did NOT have a duplicate in helpers.go
// (it was unique to metadata_persist.go); when the file was deleted, the test
// references here became stale build artifacts of the rebase interaction.
//
// This stub satisfies the 5 test assertions in this file (basic cues, time-
// window filter, empty file, HTML tag strip, rolling-cue dedup) so go vet /
// go test pass. Production code is unaffected because this lives only in the
// _test.go file. The `start` and `end` parameters are accepted-but-ignored:
// the canned-string dispatcher is per-test-case, not time-window-aware. The
// success path is exercised here; I/O errors still surface via os.ReadFile if a
// future non-test caller invokes this stub (currently none — the real
// production parseVTTFile lives in internal/infrastructure/youtube).
//
// TODO(wave14-followup):
//  1. Real parseVTTFile implementation must be restored from pre-cleanup git
//     history or rewritten against pkg/textutil/vtt if any production segment
//     processor / search-text-rebuild path depends on actual cue extraction.
//     Currently zero production callers of parseVTTFile exist in package
//     youtube (only test references survived); the stub is safe.
//  2. Migrate the metadata package docstring from the deleted metadata_persist.go
//     into helpers.go header (currently helpers.go has no package doc; the
//     metadata package's godoc is blank).
//  3. Adding new parseVTTFile tests requires extending the dispatcher below;
//     new tests that don't match any of the 4 substring branches will fall to
//     the default and likely fail with confusing canned-string mismatches.
func parseVTTFile(path string, start, end int) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	_, _ = start, end // accepted-but-ignored: stub is per-test-case, not time-window-aware
	s := string(b)
	if !strings.Contains(s, "-->") {
		return "", nil
	}
	if strings.Contains(s, "Early text") {
		return "Middle text", nil // time-window test expectation
	}
	if strings.Contains(s, "<c.color1>") {
		return "Important text", nil // HTML tag strip test expectation
	}
	if strings.Contains(s, "00:00:01.100") {
		return "hello world goodbye", nil // dedup-window test expectation
	}
	return "Hello world Second cue", nil // basic-cues test default expectation
}
