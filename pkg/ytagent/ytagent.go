// Package ytagent is the autonomous YouTube transcript capability for remote
// agents: readable transcript text for a YouTube URL, obtained WITHOUT
// downloading the video and WITHOUT running Whisper.
//
// Division of responsibility mirrors pkg/materialagent's ("no endpoint is
// re-implemented here"): the acquisition chain lives server-side behind
// GET /api/clips/transcript (yt-dlp --write-subs/--write-auto-subs
// --skip-download + the canonical rolling-cue VTT parser + the 5-priority
// text-track resolver). This package owns the AGENT side of the contract:
//
//   - honest provenance (manual subtitle vs ASR) so callers never treat a
//     Whisper transcript as equal to a VTT one;
//   - a 24-hour on-disk cache keyed with the canonical transcript key
//     formula, so re-reads cost zero network (see cache.go);
//   - jittered exponential backoff on HTTP 429 — the real failure mode
//     observed when harvesting many transcripts (yt-dlp rate limits), paced
//     with bounds mirroring YTDLP_MIN/MAX_SLEEP_SECONDS (2s/5s in prod).
package ytagent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// Backoff + retry defaults. The sleep bounds mirror the production pacing
// knobs YTDLP_MIN_SLEEP_SECONDS=2 / YTDLP_MAX_SLEEP_SECONDS=5 (see
// scripts/systemd/pipelinegen.service.d/youtube-dlp.conf): the agent's
// politeness towards the same upstream must not be stingier than the
// server's own.
const (
	DefaultMinSleep    = 2 * time.Second
	DefaultMaxSleep    = 5 * time.Second
	DefaultMaxAttempts = 3
)

// Fetcher resolves YouTube transcripts through the Master's read-only
// transcript endpoint, with disk cache and rate-limit backoff.
// Zero value is not usable; construct with New.
type Fetcher struct {
	client *veloxclient.Client
	cache  *Cache

	// MinSleep / MaxSleep bound one backoff sleep (jittered, exponential).
	// MaxSleep < MinSleep is clamped to MinSleep.
	MinSleep time.Duration
	MaxSleep time.Duration
	// MaxAttempts is the total tries on persistent 429 (>=1).
	MaxAttempts int

	// sleep and randFloat are injection seams for tests (deterministic
	// durations, zero actual waiting).
	sleep     func(context.Context, time.Duration) error
	randFloat func() float64
}

// Option configures a Fetcher.
type Option func(*Fetcher)

// WithDiskCache enables the 24-hour on-disk transcript cache in dir.
// Without it every Fetch hits the network.
func WithDiskCache(dir string) Option {
	return func(f *Fetcher) { f.cache = NewCache(dir) }
}

// WithCache installs a pre-built cache (tests inject a cache with a
// controllable clock through this seam).
func WithCache(c *Cache) Option {
	return func(f *Fetcher) { f.cache = c }
}

// WithBackoff overrides the jitter bounds (pacing between 429 retries).
func WithBackoff(min, max time.Duration) Option {
	return func(f *Fetcher) {
		f.MinSleep = min
		f.MaxSleep = max
	}
}

// WithMaxAttempts overrides the total tries on persistent 429.
func WithMaxAttempts(n int) Option {
	return func(f *Fetcher) { f.MaxAttempts = n }
}

// WithSleep replaces the sleep implementation (test seam).
func WithSleep(fn func(context.Context, time.Duration) error) Option {
	return func(f *Fetcher) { f.sleep = fn }
}

// WithRandFloat replaces the jitter source (test seam; return [0,1)).
func WithRandFloat(fn func() float64) Option {
	return func(f *Fetcher) { f.randFloat = fn }
}

// New builds a Fetcher over the given client.
func New(client *veloxclient.Client, opts ...Option) *Fetcher {
	f := &Fetcher{
		client:      client,
		MinSleep:    DefaultMinSleep,
		MaxSleep:    DefaultMaxSleep,
		MaxAttempts: DefaultMaxAttempts,
		sleep:       sleepCtx,
		randFloat:   rand.Float64,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

// Result is one transcript answer with its acquisition metadata.
type Result struct {
	// Transcript is the server's canonical payload (text + cues +
	// resolved provenance).
	Transcript veloxclient.Transcript
	// Cached reports the answer came from the on-disk cache (0 network).
	Cached bool
	// FetchedAt is when the transcript was acquired from the server
	// (cache entries carry the original fetch time, so a cached answer
	// reports the ORIGINAL fetch instant, not the read instant).
	FetchedAt time.Time
}

// EvidenceClass is the agent-facing honesty tag derived from the server's
// resolved provenance:
//
//	"manual"   — human-authored tracks (manual / provided)
//	"subtitle" — YouTube caption tracks (auto-generated OR uploaded VTT)
//	"asr"      — machine transcripts (whisper, visual analysis, recovery)
//
// Callers that rank evidence must not treat "asr" as equal to a VTT:
// ASR carries word errors, while a VTT (even auto-generated) is what the
// publisher's pipeline emitted. Empty sourceType classifies as "unknown".
func EvidenceClass(sourceType string) string {
	switch sourceType {
	case "manual", "provided":
		return "manual"
	case "youtube_subtitle":
		return "subtitle"
	case "whisper", "visual_analysis", "qdrant-recovery":
		return "asr"
	default:
		return "unknown"
	}
}

// Fetch returns the transcript for videoURL over [startSec, endSec]
// (0/0 = whole video).
//
// Order: disk cache (if enabled) → server fetch with jittered 429 backoff
// → cache store. Only HTTP 429 is retried: the server owns its own internal
// acquisition chain (including its own yt-dlp pacing and Whisper fallback),
// so a 4xx/5xx here is a request problem, not congestion, and retrying it
// would only multiply load.
func (f *Fetcher) Fetch(ctx context.Context, videoURL string, startSec, endSec int) (*Result, error) {
	if f == nil || f.client == nil {
		return nil, errors.New("ytagent: Fetcher is not initialized (nil client)")
	}
	videoID, err := urlutil.ExtractVideoID(videoURL)
	if err != nil || videoID == "" {
		return nil, fmt.Errorf("ytagent: %q is not a recognizable YouTube URL: %w", videoURL, err)
	}

	if f.cache != nil {
		if entry, ok := f.cache.Get(videoID); ok {
			return &Result{
				Transcript: entry.Transcript,
				Cached:     true,
				FetchedAt:  entry.FetchedAt,
			}, nil
		}
	}

	attempts := f.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		tr, fetchErr := f.client.ClipTranscript(ctx, videoURL, startSec, endSec)
		if fetchErr == nil {
			res := &Result{Transcript: *tr, FetchedAt: time.Now().UTC()}
			if f.cache != nil {
				// Best-effort store: a cache write failure must never
				// lose a successfully fetched transcript.
				_ = f.cache.Put(videoID, tr, res.FetchedAt)
			}
			return res, nil
		}
		lastErr = fetchErr
		if !errors.Is(fetchErr, veloxclient.ErrRateLimited) {
			return nil, fmt.Errorf("ytagent: transcript fetch for %s: %w", videoID, fetchErr)
		}
		if attempt == attempts {
			break
		}
		if sleepErr := f.sleep(ctx, f.backoff(attempt)); sleepErr != nil {
			return nil, fmt.Errorf("ytagent: rate-limit backoff for %s: %w", videoID, sleepErr)
		}
	}
	return nil, fmt.Errorf("ytagent: transcript fetch for %s still rate limited after %d attempt(s): %w", videoID, attempts, lastErr)
}

// backoff computes the jittered sleep before retry `attempt` (1-based,
// the sleep that follows attempt N): exponential growth from MinSleep,
// doubled per attempt, capped at MaxSleep, with uniform jitter inside
// [MinSleep, upper]. Full-jitter-free lower bound keeps the politeness
// floor; the cap keeps a long run from stalling.
func (f *Fetcher) backoff(attempt int) time.Duration {
	min := f.MinSleep
	if min <= 0 {
		min = DefaultMinSleep
	}
	max := f.MaxSleep
	if max < min {
		max = min
	}
	upper := min
	for i := 1; i < attempt; i++ {
		upper *= 2
		if upper >= max || upper <= 0 { // cap (or overflow)
			upper = max
			break
		}
	}
	if upper <= min {
		return min
	}
	span := float64(upper - min)
	r := f.randFloat
	if r == nil {
		r = rand.Float64
	}
	jitter := r()
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	return min + time.Duration(jitter*span)
}

// sleepCtx is the default sleep: cancellable, so a dead run stops at once.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
