package assets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestHTTPSelfLoopProbe_HTTP2xx_ReturnsTrue verifies the canonical happy
// path: server responds 200 within timeout → (true, nil). Mirrors the
// production runtime contract /api/artlist/stats (handler returns stats
// on HTTP 200 per internal/api/assets/artlist/artlist_handlers.go::Stats).
func TestHTTPSelfLoopProbe_HTTP2xx_ReturnsTrue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/artlist/stats", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	probe := NewHTTPSelfLoopProbe(srv.URL, "/api/artlist/stats", time.Second, zap.NewNop())
	ctx := context.Background()

	live, err := probe.Probe(ctx)
	require.NoError(t, err)
	require.True(t, live, "expected live=true on HTTP 2xx")
}

// TestHTTPSelfLoopProbe_HTTP5xx_ReturnsFalseNotLive verifies the
// canonical non-live-but-reachable case: server responds with non-2xx
// within timeout → (false, nil). The Probe MUST NOT surface the 4xx/5xx
// details — caller decides retry policy per godlike/07 no-fake-availability.
func TestHTTPSelfLoopProbe_HTTP5xx_ReturnsFalseNotLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	probe := NewHTTPSelfLoopProbe(srv.URL, "/api/artlist/stats", time.Second, zap.NewNop())
	ctx := context.Background()

	live, err := probe.Probe(ctx)
	require.NoError(t, err, "non-2xx must NOT return error (godlike/07 not-live classification)")
	require.False(t, live, "expected live=false on HTTP 5xx")
}

// TestHTTPSelfLoopProbe_Timeout_ReturnsError verifies the canonical
// transport-failure case: server hangs > timeout → (false, err). The err
// preserves the underlying cause (typically context.DeadlineExceeded
// wrapped by net/http).
func TestHTTPSelfLoopProbe_Timeout_ReturnsError(t *testing.T) {
	blockCh := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blockCh // pend indefinitely until test releases
		w.WriteHeader(http.StatusOK)
	}))
	defer func() { close(blockCh); srv.Close() }()

	probe := NewHTTPSelfLoopProbe(srv.URL, "/api/artlist/stats", 50*time.Millisecond, zap.NewNop())
	ctx := context.Background()

	live, err := probe.Probe(ctx)
	require.Error(t, err, "timeout must surface as transport error")
	require.False(t, live, "timeout must report not-live")
}

// TestHTTPSelfLoopProbe_DefaultTimeout_FallbackApplied verifies the
// constructor's timeout-defaulting per the spec: timeout <= 0 falls back
// to DefaultProbeTimeout (5s). The probe is constructed but we don't
// fire it — verifying the contract via client.Timeout introspection
// (the field is package-private by design; the test lives in the same
// package as the adapter so access is fine per Go's package-private rule).
func TestHTTPSelfLoopProbe_DefaultTimeout_FallbackApplied(t *testing.T) {
	probe := NewHTTPSelfLoopProbe("http://localhost:1", "/api/artlist/stats", 0, zap.NewNop())
	require.Equal(t, DefaultProbeTimeout, probe.client.Timeout,
		"timeout <= 0 must default to DefaultProbeTimeout per constructor contract")
}

// TestHTTPSelfLoopProbe_NilReceiver_FailsClosed verifies the godlike/07
// no-fake-availability contract: a nil *HTTPSelfLoopProbe receiver
// returns a typed error rather than panicking — operators see the error
// in logs (not a nil-deref panic that crashes the goroutine).
func TestHTTPSelfLoopProbe_NilReceiver_FailsClosed(t *testing.T) {
	var probe *HTTPSelfLoopProbe
	live, err := probe.Probe(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil receiver")
	require.False(t, live)
}
