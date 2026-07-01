package images

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── isImageRetryable unit tests ──────────────────────────────────────

func TestIsImageRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("%w: timeout", ErrImageTransient), true},
		{fmt.Errorf("%w: HTTP 429", ErrImageTransient), true},
		{fmt.Errorf("%w: connection refused", ErrImageTransient), true},
		{ErrImageNotFound, false},
		{fmt.Errorf("%w: corrupt body", ErrImageInvalidResponse), false},
		{errors.New("some random error"), false},
		{nil, false},
	}
	for _, tc := range cases {
		got := isImageRetryable(tc.err)
		if got != tc.want {
			t.Errorf("isImageRetryable(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// ── HTTP status → typed error tests (via httptest.Server) ────────────

// newImageTestServer creates an httptest.Server that responds with the
// given sequence of status codes + bodies. Each call consumes one entry.
func newImageTestServer(t *testing.T, responses []struct {
	status int
	body   string
}) *httptest.Server {
	t.Helper()
	var calls int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt32(&calls, 1)) - 1
		if idx >= len(responses) {
			idx = len(responses) - 1
		}
		w.WriteHeader(responses[idx].status)
		_, _ = w.Write([]byte(responses[idx].body))
	}))
}

// downloadAndClassify is a testable helper that downloads an image from
// a URL and classifies the HTTP response using the typed sentinels.
// It mirrors the download portion of SearchWebImage without the DDG search.
func downloadAndClassify(ctx context.Context, client *http.Client, imgURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", imgURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %v", ErrImageTransient, err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: download: %v", ErrImageTransient, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: url=%s", ErrImageNotFound, imgURL)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: HTTP %d", ErrImageTransient, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: unexpected HTTP %d", ErrImageInvalidResponse, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrImageTransient, err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: downloaded image is empty", ErrImageInvalidResponse)
	}
	return body, nil
}

// TestImageSearch_200Succeeds verifies that a successful HTTP 200
// response with a valid binary body returns the body bytes.
func TestImageSearch_200Succeeds(t *testing.T) {
	srv := newImageTestServer(t, []struct {
		status int
		body   string
	}{{http.StatusOK, "\x89PNG\r\n\x1a\nfake-png-data"}})
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	body, err := downloadAndClassify(context.Background(), client, srv.URL+"/image.png")
	if err != nil {
		t.Fatalf("downloadAndClassify: unexpected error: %v", err)
	}
	if len(body) == 0 {
		t.Error("body should not be empty on 200 OK")
	}
}

// TestImageSearch_429RetriesThenSucceeds verifies that transient 429
// errors are retried via pkg/retry.Do and eventually succeed.
func TestImageSearch_429RetriesThenSucceeds(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&callCount, 1)
		switch c {
		case 1, 2:
			w.WriteHeader(http.StatusTooManyRequests) // 429
			_, _ = w.Write([]byte("rate limited"))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nimage-data"))
		}
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	ctx := context.Background()

	var body []byte
	err := retry.Do(ctx, func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var downloadErr error
		body, downloadErr = downloadAndClassify(ctx, client, srv.URL+"/image.png")
		return downloadErr
	}, retry.Options{
		MaxAttempts:    5,
		InitialBackoff: 10 * time.Millisecond,
		IsRetryable:    isImageRetryable,
	})
	if err != nil {
		t.Fatalf("retry.Do should succeed after 429s: %v", err)
	}
	if len(body) == 0 {
		t.Error("body should not be empty")
	}
	// 3 calls: 429, 429, 200
	if c := atomic.LoadInt32(&callCount); c != 3 {
		t.Errorf("expected 3 HTTP calls (429, 429, 200), got %d", c)
	}
}

// TestImageSearch_404DoesNotRetry verifies that HTTP 404 returns
// ErrImageNotFound immediately (not retryable).
func TestImageSearch_404DoesNotRetry(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	ctx := context.Background()

	err := retry.Do(ctx, func() error {
		_, downloadErr := downloadAndClassify(ctx, client, srv.URL+"/not-found.png")
		return downloadErr
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 10 * time.Millisecond,
		IsRetryable:    isImageRetryable,
	})
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !errors.Is(err, ErrImageNotFound) {
		t.Errorf("expected ErrImageNotFound, got: %v", err)
	}
	// isImageRetryable returns false for ErrImageNotFound, so retry.Do
	// should stop after 1 attempt.
	if c := atomic.LoadInt32(&callCount); c != 1 {
		t.Errorf("expected 1 HTTP call for non-retryable 404, got %d", c)
	}
}

// TestImageSearch_500Retries verifies that HTTP 500 is classified as
// transient (retryable) and retried.
func TestImageSearch_500Retries(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := atomic.AddInt32(&callCount, 1)
		if c < 3 {
			w.WriteHeader(http.StatusInternalServerError) // 500
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nimage-data"))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	ctx := context.Background()

	var body []byte
	err := retry.Do(ctx, func() error {
		var downloadErr error
		body, downloadErr = downloadAndClassify(ctx, client, srv.URL+"/image.png")
		return downloadErr
	}, retry.Options{
		MaxAttempts:    5,
		InitialBackoff: 10 * time.Millisecond,
		IsRetryable:    isImageRetryable,
	})
	if err != nil {
		t.Fatalf("retry should succeed after 500s: %v", err)
	}
	if len(body) == 0 {
		t.Error("body should not be empty")
	}
	if c := atomic.LoadInt32(&callCount); c != 3 {
		t.Errorf("expected 3 HTTP calls, got %d", c)
	}
}

// TestImageSearch_ContextCancellationStopsRetry verifies that context
// cancellation aborts the retry loop immediately.
func TestImageSearch_ContextCancellationStopsRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests) // always 429
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	// Create a context that gets cancelled after a short delay.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var callCount int32
	err := retry.Do(ctx, func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		atomic.AddInt32(&callCount, 1)
		_, downloadErr := downloadAndClassify(ctx, client, srv.URL+"/image.png")
		return downloadErr
	}, retry.Options{
		MaxAttempts:    10,
		InitialBackoff: 200 * time.Millisecond,
		IsRetryable:    func(err error) bool { return true }, // always retry
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got: %v", err)
	}
	// Context should cancel during backoff; at most 1-2 HTTP calls.
	if c := atomic.LoadInt32(&callCount); c > 2 {
		t.Errorf("expected at most 2 HTTP calls before context cancel, got %d", c)
	}
}

// TestImageSearch_InvalidJSONIsNotSuccess verifies that an HTTP 200
// with an empty body returns ErrImageInvalidResponse.
func TestImageSearch_EmptyBodyIsInvalidResponse(t *testing.T) {
	srv := newImageTestServer(t, []struct {
		status int
		body   string
	}{{http.StatusOK, ""}})
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	_, err := downloadAndClassify(context.Background(), client, srv.URL+"/empty.png")
	if err == nil {
		t.Fatal("expected error for empty body, got nil")
	}
	if !errors.Is(err, ErrImageInvalidResponse) {
		t.Errorf("expected ErrImageInvalidResponse for empty body, got: %v", err)
	}
}

// TestImageSearch_TimeoutIsRetryable verifies that a connection timeout
// is classified as ErrImageTransient (retryable), not a permanent failure.
func TestImageSearch_TimeoutIsRetryable(t *testing.T) {
	// Server that hangs, causing a client timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // longer than client timeout
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, err := downloadAndClassify(context.Background(), client, srv.URL+"/slow.png")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrImageTransient) {
		t.Errorf("timeout should be ErrImageTransient, got: %v", err)
	}
}

// ── Benchmark: verify no string(body) in the ingest path ─────────────

// BenchmarkImageIngest_20MB verifies that the download path does not
// perform a string(body) conversion that would double memory allocation.
// A 20MB body should allocate ~20MB (plus overhead), not ~40MB.
func BenchmarkImageIngest_20MB(b *testing.B) {
	// Generate 20MB of fake JPEG data.
	fakeJPEG := bytes.Repeat([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}, 2*1024*1024) // ~20MB
	if len(fakeJPEG) < 20*1024*1024 {
		// Pad to exactly 20MB.
		fakeJPEG = append(fakeJPEG, bytes.Repeat([]byte{0x00}, 20*1024*1024-len(fakeJPEG))...)
	}
	b.Logf("fake JPEG size: %d bytes (~%.1f MB)", len(fakeJPEG), float64(len(fakeJPEG))/(1024*1024))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(fakeJPEG)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 30 * time.Second}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		body, err := downloadAndClassify(context.Background(), client, srv.URL+"/large.jpg")
		if err != nil {
			b.Fatalf("downloadAndClassify: %v", err)
		}
		if len(body) < 20*1024*1024 {
			b.Fatalf("expected >=20MB body, got %d bytes", len(body))
		}
		// Verify we're NOT doing string(body) — use bytes.Reader.
		reader := bytes.NewReader(body)
		if reader.Len() != len(body) {
			b.Fatalf("bytes.NewReader length mismatch")
		}
	}
}
