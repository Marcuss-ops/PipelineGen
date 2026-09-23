package ytagent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

const testVideoID = "dQw4w9WgXcQ"
const testVideoURL = "https://www.youtube.com/watch?v=" + testVideoID

// transcriptBody builds a GET /api/clips/transcript 200 payload.
func transcriptBody(language, sourceType string) []byte {
	body, _ := json.Marshal(map[string]any{
		"ok":          true,
		"video_id":    testVideoID,
		"language":    language,
		"source_type": sourceType,
		"is_original": true,
		"provider":    "yt-dlp",
		"text":        "hello transcript",
		"cue_count":   1,
		"cues":        []map[string]any{{"start_ms": 0, "end_ms": 900, "text": "hello transcript"}},
	})
	return body
}

// recordingSleep captures backoff durations instead of waiting.
type recordingSleep struct {
	calls []time.Duration
	err   error
}

func (r *recordingSleep) sleep(_ context.Context, d time.Duration) error {
	r.calls = append(r.calls, d)
	return r.err
}

// ── T1.1: 429 backoff ────────────────────────────────────────────────────────

// TestFetch_RateLimitedRetriesThenSucceeds pins the core T1.1 behaviour: a
// 429 is retried with jittered backoff inside [MinSleep, cap], and the third
// attempt's answer is returned. Sleeps are recorded, never waited.
func TestFetch_RateLimitedRetriesThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != veloxclient.RouteClipsTranscript {
			t.Errorf("path = %s, want %s", r.URL.Path, veloxclient.RouteClipsTranscript)
		}
		switch hits.Add(1) {
		case 1, 2:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"kind":"rate_limited","error":"slow down","retry_after_seconds":2}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(transcriptBody("en", "youtube_subtitle"))
		}
	}))
	defer srv.Close()

	sleep := &recordingSleep{}
	f := New(
		mustClient(t, srv.URL),
		WithDiskCache(t.TempDir()),
		WithBackoff(10*time.Millisecond, 40*time.Millisecond),
		WithMaxAttempts(3),
		WithSleep(sleep.sleep),
		WithRandFloat(func() float64 { return 1 }), // upper bound → deterministic
	)

	res, err := f.Fetch(context.Background(), testVideoURL, 0, 0)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("server hits = %d, want 3 (2 rate-limited + 1 success)", hits.Load())
	}
	if res.Cached {
		t.Error("first fetch must not be a cache hit")
	}
	if res.Transcript.Text != "hello transcript" {
		t.Errorf("text = %q", res.Transcript.Text)
	}
	if len(sleep.calls) != 2 {
		t.Fatalf("sleeps = %d, want 2 (one per retry)", len(sleep.calls))
	}
	// Exponential from MinSleep: attempt 1 waits the base, attempt 2
	// doubles it (deterministic rand=1 → upper bound of the jitter window;
	// MaxSleep=40ms caps attempt 3, which never happens here).
	if want := 10 * time.Millisecond; sleep.calls[0] != want {
		t.Errorf("backoff[0] = %v, want %v (first retry waits the base MinSleep)", sleep.calls[0], want)
	}
	if want := 20 * time.Millisecond; sleep.calls[1] != want {
		t.Errorf("backoff[1] = %v, want %v (exponential doubling of MinSleep)", sleep.calls[1], want)
	}
}

// TestFetch_PersistentRateLimitSurfacesTypedError pins the exhaustion path:
// after MaxAttempts the error still wraps veloxclient.ErrRateLimited so the
// caller can tell "upstream congested" from "bad request", and every attempt
// happened.
func TestFetch_PersistentRateLimitSurfacesTypedError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	f := New(
		mustClient(t, srv.URL),
		WithBackoff(time.Millisecond, 2*time.Millisecond),
		WithMaxAttempts(3),
		WithSleep(func(context.Context, time.Duration) error { return nil }),
	)

	_, err := f.Fetch(context.Background(), testVideoURL, 0, 0)
	if err == nil {
		t.Fatal("Fetch must fail on persistent 429")
	}
	if !errors.Is(err, veloxclient.ErrRateLimited) {
		t.Errorf("error must wrap ErrRateLimited, got: %v", err)
	}
	if hits.Load() != 3 {
		t.Errorf("server hits = %d, want 3", hits.Load())
	}
}

// TestFetch_NonRateLimitErrorIsNotRetried pins the retry boundary: only 429
// is congestion. A 500 (ErrServer) surfaces immediately with ONE hit —
// retrying request problems would multiply load on an already-failing server.
func TestFetch_NonRateLimitErrorIsNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sleep := &recordingSleep{}
	f := New(mustClient(t, srv.URL), WithSleep(sleep.sleep), WithMaxAttempts(3))

	if _, err := f.Fetch(context.Background(), testVideoURL, 0, 0); err == nil {
		t.Fatal("Fetch must fail on 500")
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1 (500 must not be retried)", hits.Load())
	}
	if len(sleep.calls) != 0 {
		t.Errorf("sleeps = %d, want 0", len(sleep.calls))
	}
}

// TestFetch_CancelledContextStopsBackoff pins cancellability: a cancelled
// context aborts during the backoff sleep instead of burning attempts.
func TestFetch_CancelledContextStopsBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	f := New(
		mustClient(t, srv.URL),
		WithMaxAttempts(5),
		WithSleep(func(ctx context.Context, _ time.Duration) error {
			cancel() // the run dies while waiting for the next attempt
			return ctx.Err()
		}),
	)
	_, err := f.Fetch(ctx, testVideoURL, 0, 0)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled wrapped", err)
	}
}

// ── T1.4: disk cache + provenance ───────────────────────────────────────────

// TestFetch_SecondReadIsServedFromDiskCache pins the T1.4 contract: the
// second Fetch of the same video does ZERO network (1 server hit total),
// reports Cached=true, keeps the original fetch instant, and the persisted
// entry carries the provenance (language + source).
func TestFetch_SecondReadIsServedFromDiskCache(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(transcriptBody("it", "whisper"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	f := New(mustClient(t, srv.URL), WithDiskCache(dir))

	first, err := f.Fetch(context.Background(), testVideoURL, 0, 0)
	if err != nil {
		t.Fatalf("first Fetch: %v", err)
	}
	if first.Cached {
		t.Error("first fetch must be a network answer")
	}

	// A NEW fetcher over the SAME dir simulates a later session (crash, reboot).
	f2 := New(mustClient(t, srv.URL), WithDiskCache(dir))
	second, err := f2.Fetch(context.Background(), testVideoURL, 0, 0)
	if err != nil {
		t.Fatalf("second Fetch: %v", err)
	}
	if !second.Cached {
		t.Error("second fetch must be a cache hit")
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1 (second read must not hit the network)", hits.Load())
	}
	if !second.FetchedAt.Equal(first.FetchedAt) {
		t.Errorf("cached FetchedAt = %v, want original %v", second.FetchedAt, first.FetchedAt)
	}
	if second.Transcript.SourceType != "whisper" || second.Transcript.Language != "it" {
		t.Errorf("cached provenance lost: lang=%q source=%q", second.Transcript.Language, second.Transcript.SourceType)
	}
}

// TestFetch_ExpiredCacheEntryRefetches pins the 24h TTL: an entry older than
// the TTL is invisible to Get and the next Fetch goes back to the network.
func TestFetch_ExpiredCacheEntryRefetches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(transcriptBody("en", "youtube_subtitle"))
	}))
	defer srv.Close()

	cache := NewCache(t.TempDir())
	base := time.Now()
	now := base
	cache.now = func() time.Time { return now }

	f := New(mustClient(t, srv.URL), WithCache(cache))
	if _, err := f.Fetch(context.Background(), testVideoURL, 0, 0); err != nil {
		t.Fatalf("first Fetch: %v", err)
	}

	now = base.Add(DefaultTTL + time.Minute)
	f2 := New(mustClient(t, srv.URL), WithCache(cache))
	if _, err := f2.Fetch(context.Background(), testVideoURL, 0, 0); err != nil {
		t.Fatalf("post-TTL Fetch: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("server hits = %d, want 2 (expired entry must refetch)", hits.Load())
	}
}

// TestKey_MirrorsCanonicalFormula pins the cache-key parity with
// internal/kernel/transcript::CacheKey: trimmed segments, ":" join, ASCII-only
// lowercasing (provider codes are ASCII; non-ASCII passes through).
func TestKey_MirrorsCanonicalFormula(t *testing.T) {
	cases := []struct {
		vid, lang, src, want string
	}{
		{"abc123", "EN", "YouTube_Subtitle", "abc123:en:youtube_subtitle"},
		{"  abc  ", " it ", " Whisper ", "abc:it:whisper"},
		{"ABC", "", "", "abc::"},
		{"vid-1_x", "pt-BR", "Manual", "vid-1_x:pt-br:manual"},
	}
	for _, tc := range cases {
		if got := Key(tc.vid, tc.lang, tc.src); got != tc.want {
			t.Errorf("Key(%q,%q,%q) = %q, want %q", tc.vid, tc.lang, tc.src, got, tc.want)
		}
	}
}

// TestCacheKeyParticipatesInFileName pins that language AND source are part
// of the persisted key: an ASR transcript can never silently answer a manual
// request (the fidelity rule the whole source tag exists for).
func TestCacheKeyParticipatesInFileName(t *testing.T) {
	dir := t.TempDir()
	cache := NewCache(dir)
	trManual := &veloxclient.Transcript{VideoID: testVideoID, Language: "en", SourceType: "manual", Text: "M"}
	trASR := &veloxclient.Transcript{VideoID: testVideoID, Language: "en", SourceType: "whisper", Text: "A"}

	if err := cache.Put(testVideoID, trManual, time.Now()); err != nil {
		t.Fatalf("Put manual: %v", err)
	}
	if err := cache.Put(testVideoID, trASR, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("Put asr: %v", err)
	}
	if entry, _ := cache.Get(testVideoID); entry == nil || entry.Transcript.Text != "A" {
		t.Errorf("Get returns freshest = %+v, want the ASR entry", entry)
	}
	if _, ok := cache.Get("otherVideo"); ok {
		t.Error("cache must not answer a different video")
	}
}

// ── evidence honesty ────────────────────────────────────────────────────────

// TestEvidenceClass pins the manual/subtitle/asr classification agents use to
// rank evidence honestly.
func TestEvidenceClass(t *testing.T) {
	cases := map[string]string{
		"manual":           "manual",
		"provided":         "manual",
		"youtube_subtitle": "subtitle",
		"whisper":          "asr",
		"visual_analysis":  "asr",
		"qdrant-recovery":  "asr",
		"":                 "unknown",
		"somethin-new":     "unknown",
	}
	for sourceType, want := range cases {
		if got := EvidenceClass(sourceType); got != want {
			t.Errorf("EvidenceClass(%q) = %q, want %q", sourceType, got, want)
		}
	}
}

func mustClient(t *testing.T, baseURL string) *veloxclient.Client {
	t.Helper()
	return veloxclient.New(baseURL, "")
}
