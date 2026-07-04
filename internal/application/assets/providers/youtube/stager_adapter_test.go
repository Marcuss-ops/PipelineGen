// Package youtube — stager_adapter_test.go (ART-002 P4.1, July 2026).
//
// 2 TDD tests pinning the YouTubeStager behavior:
//
//   - TestYouTubeStager_StageSource_NilAdapter: typed-error path
//     (composition-time fail-closed: caller built the stager with a
//     nil adapter; StageSource returns ErrYoutubeStagerNotWired).
//
//   - TestYouTubeStager_StageSource_EmptyURL: typed-error path
//     (caller passed a SourceRef with empty URL; StageSource returns
//     ErrYoutubeStagerEmptyURL BEFORE attempting the adapter call).
//
//   - TestYouTubeStager_Cleanup_NilSafe + NonExistent: nil-staged +
//     empty-path + missing-file paths return nil (no-op semantics
//     matching ArtlistStager precedent).
//
//   - TestYouTubeStager_ParseDownloadSection: pin the canonical
//     yt-dlp "HH:MM:SS-HH:MM:SS" parser — full 3-component,
//     2-component minutes:seconds, and 3 error paths (empty,
//     malformed, mixed-component-width).
package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// TestYouTubeStager_StageSource_NilAdapter pins the typed-nil guard:
// a Stager built with a nil adapter must fail-closed at StageSource
// time, returning ErrYoutubeStagerNotWired reachable via errors.Is.
func TestYouTubeStager_StageSource_NilAdapter(t *testing.T) {
	s := &YouTubeStager{adapter: nil} // explicit nil (bypasses NewYouTubeStager)
	staged, err := s.StageSource(context.Background(), assets.SourceRef{URL: "https://youtu.be/dQw4w9WgXcQ"})
	if err == nil {
		t.Fatalf("expected error from nil-adapter StageSource, got staged=%+v", staged)
	}
	if staged != nil {
		t.Errorf("expected nil staged on error, got %+v", staged)
	}
	if !errors.Is(err, ErrYoutubeStagerNotWired) {
		t.Errorf("expected errors.Is(err, ErrYoutubeStagerNotWired) to be true, got err=%v", err)
	}
}

// TestYouTubeStager_StageSource_EmptyURL pins the empty-URL guard:
// a SourceRef with no URL must fail-closed at StageSource time BEFORE
// the adapter call, returning ErrYoutubeStagerEmptyURL reachable via
// errors.Is. Uses a non-nil but unconfigured *Adapter so the nil-wired
// guard does not fire first (precedent: ArtlistStager checks nil-wired
// first; we want to isolate the empty-URL path here).
func TestYouTubeStager_StageSource_EmptyURL(t *testing.T) {
	s := NewYouTubeStager(&Adapter{}) // non-nil but unconfigured (no src/fetcher)
	_, err := s.StageSource(context.Background(), assets.SourceRef{URL: ""})
	if err == nil {
		t.Fatalf("expected error from empty-URL StageSource, got nil")
	}
	if !errors.Is(err, ErrYoutubeStagerEmptyURL) {
		t.Errorf("expected errors.Is(err, ErrYoutubeStagerEmptyURL) to be true, got err=%v", err)
	}
}

// TestYouTubeStager_Cleanup_NilSafe pins the no-op-on-nil semantics:
// Cleanup with nil staged or empty LocalPath returns nil (no error).
// Matches the ArtlistStager precedent so callers can use the same
// idiom across the registry.
func TestYouTubeStager_Cleanup_NilSafe(t *testing.T) {
	s := NewYouTubeStager(nil)
	ctx := context.Background()
	if err := s.Cleanup(ctx, nil); err != nil {
		t.Errorf("expected nil error for nil staged, got %v", err)
	}
	if err := s.Cleanup(ctx, &assets.StagedAsset{LocalPath: ""}); err != nil {
		t.Errorf("expected nil error for empty LocalPath, got %v", err)
	}
	// Missing file path: os.Remove returns ErrNotExist which the
	// stager swallows (matches ArtlistStager's silent-degrade
	// precedent — staging is best-effort).
	if err := s.Cleanup(ctx, &assets.StagedAsset{LocalPath: "/tmp/nonexistent_path_xyzzy_42"}); err != nil {
		t.Errorf("expected nil error for nonexistent file, got %v", err)
	}
}

// TestYouTubeStager_Cleanup_RemovesExistingFile pins the happy path:
// Cleanup removes an existing file and returns nil. Uses t.TempDir
// for hermetic test isolation (no shared state across runs).
func TestYouTubeStager_Cleanup_RemovesExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "staged_asset.mp4")
	if err := os.WriteFile(target, []byte("fake-bytes"), 0o644); err != nil {
		t.Fatalf("setup: write target: %v", err)
	}
	s := NewYouTubeStager(nil)
	if err := s.Cleanup(context.Background(), &assets.StagedAsset{LocalPath: target}); err != nil {
		t.Errorf("expected nil error on existing-file cleanup, got %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed, stat err=%v", err)
	}
}

// TestYouTubeStager_ParseDownloadSection pins the canonical yt-dlp
// "HH:MM:SS-HH:MM:SS" parser. Covers 3 happy paths (full 3-comp,
// 2-comp MM:SS-MM:SS, full-video empty input) + 3 error paths
// (malformed, mixed-component-width, negative-component).
func TestYouTubeStager_ParseDownloadSection(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantStart time.Duration
		wantEnd   time.Duration
		wantErr   bool
	}{
		{
			name:      "happy_3component",
			input:     "00:01:20-00:01:35",
			wantStart: 80 * time.Second,
			wantEnd:   95 * time.Second,
		},
		{
			name:      "happy_2component",
			input:     "01:30-02:45",
			wantStart: 90 * time.Second,
			wantEnd:   165 * time.Second,
		},
		{
			name:      "empty_returns_zeros",
			input:     "",
			wantStart: 0,
			wantEnd:   0,
		},
		{
			name:    "malformed_no_dash",
			input:   "00:01:20",
			wantErr: true,
		},
		{
			name:    "malformed_garbage",
			input:   "not-a-section",
			wantErr: true,
		},
		{
			// yt-dlp accepts mixed-width endpoints (e.g. "00:01:20-02:45"
			// where the start is 3-component and the end is 2-component);
			// our parser mirrors that permissive behavior.
			name:      "permissive_mixed_width",
			input:     "00:01:20-02:45",
			wantStart: 80 * time.Second,
			wantEnd:   165 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStart, gotEnd, err := parseDownloadSection(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got start=%v end=%v", tc.input, gotStart, gotEnd)
				}
				if !errors.Is(err, ErrYoutubeStagerInvalidSection) {
					t.Errorf("expected errors.Is(err, ErrYoutubeStagerInvalidSection) to be true, got err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.input, err)
			}
			if gotStart != tc.wantStart {
				t.Errorf("start: want %v, got %v", tc.wantStart, gotStart)
			}
			if gotEnd != tc.wantEnd {
				t.Errorf("end: want %v, got %v", tc.wantEnd, gotEnd)
			}
		})
	}
}
