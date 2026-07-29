package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	mwapp "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/gin-gonic/gin"
)

// stubRateLimitPort enables rate-limit with a tight per-window fill so
// the bypass becomes observable within a single burst. Compile-time
// assertion pin (godlike/06 SSOT one-canonical-owner-per-fact): future
// drift in the RateLimitPort signature surfaces as a build failure
// here, NOT at first panic runtime.
type stubRateLimitPort struct {
	enabled  bool
	requests int
}

func (s stubRateLimitPort) RateLimitEnabled() bool { return s.enabled }
func (s stubRateLimitPort) RateLimitRequests() int { return s.requests }

var _ mwapp.RateLimitPort = stubRateLimitPort{}

// stubEnvReader delegates to os.Getenv so t.Setenv in tests works.
type stubEnvReader struct{}

func (stubEnvReader) Getenv(key string) string { return os.Getenv(key) }

// TestRateLimit_VoiceoverBurstBypass_AllowsBurstWhenEnvSet:
// godlike/07 fix-minimo canonical case — burst of 5 against
// /api/media/voiceover/generate with the env set and limit=1 must
// return 200 on every request (bypass grants all).
func TestRateLimit_VoiceoverBurstBypass_AllowsBurstWhenEnvSet(t *testing.T) {
	t.Setenv(voiceoverBurstBypassEnvKey, "1")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	rl := RateLimit(stubRateLimitPort{enabled: true, requests: 1}, stubEnvReader{})
	r.Use(rl.Handler)
	r.POST(voiceoverBurstBypassRoutePrefix+"/generate", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, voiceoverBurstBypassRoutePrefix+"/generate", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("burst #%d: code=%d want 200 (env=1 → bypass MUST allow)", i, w.Code)
		}
	}
}

// TestRateLimit_VoiceoverBurstBypass_BlocksWhenEnvUnset:
// godlike/07 NO-FAKE-AVAILABILITY — env="" means bypass is off and the
// canonical per-IP limiter applies on the voiceover route just like
// every other route.
func TestRateLimit_VoiceoverBurstBypass_BlocksWhenEnvUnset(t *testing.T) {
	t.Setenv(voiceoverBurstBypassEnvKey, "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	rl := RateLimit(stubRateLimitPort{enabled: true, requests: 1}, noopEnvReader{})
	r.Use(rl.Handler)
	r.POST(voiceoverBurstBypassRoutePrefix+"/generate", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req1 := httptest.NewRequest(http.MethodPost, voiceoverBurstBypassRoutePrefix+"/generate", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: code=%d want 200 (single token consumed)", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, voiceoverBurstBypassRoutePrefix+"/generate", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: code=%d want 429 (env='' → bypass OFF → limiter applies)", w2.Code)
	}
}

// TestRateLimit_VoiceoverBurstBypass_DoesNotAffectOtherRoutes:
// godlike/07 minimum-blast-radius — bypass MUST be scoped to the
// voiceover route prefix; silent widening of production rate-limits
// across other surfaces is forbidden.
func TestRateLimit_VoiceoverBurstBypass_DoesNotAffectOtherRoutes(t *testing.T) {
	t.Setenv(voiceoverBurstBypassEnvKey, "1")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	rl := RateLimit(stubRateLimitPort{enabled: true, requests: 1}, noopEnvReader{})
	r.Use(rl.Handler)
	r.POST("/api/script/generate", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req1 := httptest.NewRequest(http.MethodPost, "/api/script/generate", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("non-voiceover #0: code=%d want 200", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/script/generate", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("non-voiceover #1: code=%d want 429 (bypass MUST NOT affect /api/script/*)", w2.Code)
	}
}

// TestRateLimit_VoiceoverBurstBypass_EmptyFullPathNotBypassed:
// godlike/07 NO-FAKE-AVAILABILITY — c.FullPath()=="" (mounted but
// un-matched route) MUST NOT silently bypass the limiter. The route
// prefix match requires a non-empty FullPath.
func TestRateLimit_VoiceoverBurstBypass_EmptyFullPathNotBypassed(t *testing.T) {
	t.Setenv(voiceoverBurstBypassEnvKey, "1")

	if isVoiceoverBurstBypassRoute(&gin.Context{}) {
		t.Fatalf("empty FullPath returned true (a zero-value gin.Context MUST NOT match)")
	}
}

// TestRateLimit_VoiceoverBurstBypass_DoesNotMatchLookalikeRoute:
// CRITICAL #1 (per code-reviewer) — godlike/07 NO-FAKE-AVAILABILITY
// regression lock. A naive strings.HasPrefix match would silently widen
// the bypass to ANY future route whose path begins with the text
// "/api/media/voiceover" but is NOT a sub-path of it (a lookalike
// hyphen-extension or a digit-suffix variant). The canonical segment-
// aware match (`path == prefix || strings.HasPrefix(path, prefix+"/")`)
// MUST reject lookalikes even when the env-var is set.
func TestRateLimit_VoiceoverBurstBypass_DoesNotMatchLookalikeRoute(t *testing.T) {
	t.Setenv(voiceoverBurstBypassEnvKey, "1")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	rl := RateLimit(stubRateLimitPort{enabled: true, requests: 1}, noopEnvReader{})
	r.Use(rl.Handler)
	r.POST("/api/media/voiceovers-archive/list", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req1 := httptest.NewRequest(http.MethodPost, "/api/media/voiceovers-archive/list", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("lookalike #0: code=%d want 200", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/media/voiceovers-archive/list", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("lookalike #1: code=%d want 429 (segment-aware match MUST reject /api/media/voiceovers-archive)", w2.Code)
	}
}

// TestRateLimit_EmitsRetryAfter_On429: godlike/07 fail-closed +
// RFC 7231 §7.1.3 contract — every 429 from the rate-limit middleware
// MUST emit a delta-seconds `Retry-After` header in [1, window]
// seconds. A 200 response MUST NOT carry the header.
//
// Remote workers (e.g. pkg/veloxclient::Client.SubmitAsync) consume
// this header to throttle their retry budget to the actual refill
// window; see pkg/retry/registry_google.go for the canonical parser
// (client-side) and internal/application/jobs/completion/map_error.go
// for the canonical upstream sibling (server-side, rate_limited kind).
//
// We assert RANGE membership rather than exact value because the time
// between the two httptest.NewRequest calls varies per host; pinning
// the integer second would yield flakes (forward-pointer: an isolated
// clock-stub would let us assert the exact mid-window case, but it is
// not worth the test complexity for this single regression lock).
func TestRateLimit_EmitsRetryAfter_On429(t *testing.T) {
	t.Setenv(voiceoverBurstBypassEnvKey, "")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// limit=1 → first request consumes the only token, second hits it.
	rl := RateLimit(stubRateLimitPort{enabled: true, requests: 1}, noopEnvReader{})
	r.Use(rl.Handler)
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 1) First call: 200, no Retry-After.
	req1 := httptest.NewRequest(http.MethodGet, "/probe", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: code=%d want 200", w1.Code)
	}
	if h := w1.Header().Get("Retry-After"); h != "" {
		t.Fatalf("first request (200): Retry-After MUST be absent; got %q", h)
	}

	// 2) Second call: 429, MUST carry Retry-After.
	req2 := httptest.NewRequest(http.MethodGet, "/probe", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: code=%d want 429", w2.Code)
	}
	h := w2.Header().Get("Retry-After")
	if h == "" {
		t.Fatalf("429 MUST emit Retry-After (godlike/07 honest hint + RFC 7231 §7.1.3); got empty header")
	}
	n, err := strconv.Atoi(h)
	if err != nil {
		t.Fatalf("Retry-After MUST be a delta-seconds INTEGER per RFC 7231 §7.1.3; got %q (parse err: %v)", h, err)
	}
	// Per-token bucket window is `window/limit` = 60s/1 = full window
	// here. The integer-window refill scheme guarantees retryAfter in
	// (0, 60] s for any denied request after exhaustion.
	if n < 1 || n > 60 {
		t.Fatalf("Retry-After MUST be in [1, 60] seconds (one window); got %d", n)
	}
}
