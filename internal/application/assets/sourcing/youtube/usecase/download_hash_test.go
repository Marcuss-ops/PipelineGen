package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubFetcher is a test-only Fetcher that returns a configurable result/error.
type stubFetcher struct {
	lastReq FetchRequest
	result  *FetchedAsset
	err     error
}

func (s *stubFetcher) Fetch(_ context.Context, req FetchRequest) (*FetchedAsset, error) {
	s.lastReq = req
	return s.result, s.err
}

// stubHasher is a test-only FileHasher that returns a configurable hash.
type stubHasher struct {
	lastPath string
	hash     string
}

func (s *stubHasher) MD5File(path string) (string, error) {
	s.lastPath = path
	return s.hash, nil
}

// ── Test 1: happy path — fetch + hash both succeed, clipID derived ──────

func TestDownloadAndHashClip_HappyPath(t *testing.T) {
	fetcher := &stubFetcher{
		result: &FetchedAsset{
			LocalPath: "/tmp/downloads/dQw4w9WgXcQ.mp4",
			AssetID:   "dQw4w9WgXcQ",
			Name:      "Rick Astley - Never Gonna Give You Up",
			Duration:  213 * time.Second,
			Bytes:     10485760,
			Metadata: map[string]string{
				"youtube_description": "Official music video",
				"youtube_uploader":    "RickAstleyVEVO",
			},
		},
	}
	hasher := &stubHasher{
		hash: "a1b2c3d4e5f6a7b8",
	}

	cmd := DownloadAndHashCommand{
		VideoID:      "dQw4w9WgXcQ",
		SourceRef:    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		SegmentStart: 10 * time.Second,
		SegmentEnd:   30 * time.Second,
	}

	result, err := DownloadAndHashClip(context.Background(), fetcher, hasher, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.LocalPath != "/tmp/downloads/dQw4w9WgXcQ.mp4" {
		t.Errorf("expected LocalPath, got %q", result.LocalPath)
	}
	if result.LegacyFileMD5 != "a1b2c3d4e5f6a7b8" {
		t.Errorf("expected LegacyFileMD5 a1b2c3d4e5f6a7b8, got %q", result.LegacyFileMD5)
	}
	wantClipID := "yt_dQw4w9WgXcQ_a1b2c3d4"
	if result.ClipID != wantClipID {
		t.Errorf("expected ClipID %q, got %q", wantClipID, result.ClipID)
	}
	if result.Name != "Rick Astley - Never Gonna Give You Up" {
		t.Errorf("expected Name, got %q", result.Name)
	}
	if result.Duration != 213*time.Second {
		t.Errorf("expected Duration 213s, got %v", result.Duration)
	}

	// Verify command fields flow through to fetch request.
	req := fetcher.lastReq
	if req.AssetID != cmd.VideoID {
		t.Errorf("expected AssetID %q, got %q", cmd.VideoID, req.AssetID)
	}
	if req.SourceRef != cmd.SourceRef {
		t.Errorf("expected SourceRef %q, got %q", cmd.SourceRef, req.SourceRef)
	}
	if req.SegmentStart != cmd.SegmentStart {
		t.Errorf("expected SegmentStart %v, got %v", cmd.SegmentStart, req.SegmentStart)
	}
	if req.SegmentEnd != cmd.SegmentEnd {
		t.Errorf("expected SegmentEnd %v, got %v", cmd.SegmentEnd, req.SegmentEnd)
	}

	// Verify hasher received the correct local path.
	if hasher.lastPath != result.LocalPath {
		t.Errorf("expected hasher path %q, got %q", result.LocalPath, hasher.lastPath)
	}
}

// ── Test 1b: NoAudio propagation ─────────────────────────────────────

// TestDownloadAndHashClip_NoAudio_ForwardsToFetcher locks the
// critical use-case-layer propagation: when DownloadAndHashCommand.NoAudio
// is true, the FetchRequest forwarded to the fetcher MUST also carry
// NoAudio=true. This is the second of three adapter layers where
// the flag must survive.
//
// Regression guard for PR-YT-NO-AUDIO-THREAD (July 2026): pre-fix,
// DownloadAndHashCommand had no NoAudio field, so the flag was
// silently dropped at this boundary.
func TestDownloadAndHashClip_NoAudio_ForwardsToFetcher(t *testing.T) {
	fetcher := &stubFetcher{
		result: &FetchedAsset{
			LocalPath: "/tmp/downloads/noaudio.mp4",
			AssetID:   "noaudio-123",
			Name:      "No Audio Test",
		},
	}
	hasher := &stubHasher{hash: "deadbeef"}

	cmd := DownloadAndHashCommand{
		VideoID:   "noaudio-123",
		SourceRef: "https://www.youtube.com/watch?v=noaudio",
		NoAudio:   true,
	}

	_, err := DownloadAndHashClip(context.Background(), fetcher, hasher, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fetcher.lastReq.NoAudio {
		t.Errorf("expected fetcher.FetchRequest.NoAudio=true when cmd.NoAudio=true, got false")
	}
}

// TestDownloadAndHashClip_NoAudio_False_ForwardsFalse locks the
// backward-compatible default: when cmd.NoAudio is false (zero-value),
// FetchRequest.NoAudio MUST also be false.
func TestDownloadAndHashClip_NoAudio_False_ForwardsFalse(t *testing.T) {
	fetcher := &stubFetcher{
		result: &FetchedAsset{
			LocalPath: "/tmp/downloads/withaudio.mp4",
			AssetID:   "withaudio-456",
			Name:      "With Audio Test",
		},
	}
	hasher := &stubHasher{hash: "cafebabe"}

	cmd := DownloadAndHashCommand{
		VideoID:   "withaudio-456",
		SourceRef: "https://www.youtube.com/watch?v=withaudio",
		NoAudio:   false,
	}

	_, err := DownloadAndHashClip(context.Background(), fetcher, hasher, cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fetcher.lastReq.NoAudio {
		t.Errorf("expected fetcher.FetchRequest.NoAudio=false when cmd.NoAudio=false (default), got true")
	}
}

// ── Test 2: nil fetcher → typed error ────────────────────────────────────

func TestDownloadAndHashClip_NilFetcher_ReturnsError(t *testing.T) {
	cmd := DownloadAndHashCommand{VideoID: "test"}
	_, err := DownloadAndHashClip(context.Background(), nil, &stubHasher{hash: "deadbeef"}, cmd)
	if err == nil {
		t.Fatal("expected error when fetcher is nil")
	}
	if !strings.Contains(err.Error(), "fetcher is nil") {
		t.Errorf("expected 'fetcher is nil' in error, got %v", err)
	}
}

// ── Test 3: fetcher error → wrapped error ────────────────────────────────

func TestDownloadAndHashClip_FetcherError_WrapsError(t *testing.T) {
	sentinel := errors.New("yt-dlp exited with code 1")
	fetcher := &stubFetcher{err: sentinel}
	cmd := DownloadAndHashCommand{VideoID: "test"}

	_, err := DownloadAndHashClip(context.Background(), fetcher, &stubHasher{hash: "deadbeef"}, cmd)
	if err == nil {
		t.Fatal("expected error when fetcher fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel), got %v", err)
	}
	if !strings.Contains(err.Error(), "usecase.DownloadAndHashClip") {
		t.Errorf("expected usecase prefix in error, got %v", err)
	}
}

// ── Test 4: nil hasher → empty hash, clipID still derived (best-effort) ──

func TestDownloadAndHashClip_NilHasher_EmptyHashButClipIDDerived(t *testing.T) {
	fetcher := &stubFetcher{
		result: &FetchedAsset{
			LocalPath: "/tmp/clip.mp4",
			Name:      "Test Video",
		},
	}

	cmd := DownloadAndHashCommand{VideoID: "test123"}

	result, err := DownloadAndHashClip(context.Background(), fetcher, nil, cmd)
	if err != nil {
		t.Fatalf("expected nil error (nil hasher is allowed), got %v", err)
	}
	if result.LegacyFileMD5 != "" {
		t.Errorf("expected empty LegacyFileMD5 when hasher is nil, got %q", result.LegacyFileMD5)
	}
	// clipID still derived with empty suffix: yt_videoID_
	if result.ClipID != "yt_test123_" {
		t.Errorf("expected ClipID yt_test123_, got %q", result.ClipID)
	}
}
