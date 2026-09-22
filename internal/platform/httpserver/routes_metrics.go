package httpserver

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.uber.org/zap"
)

// registerMetricsRoute mounts /metrics with the PR-METRICS-FAILCLOSED
// posture, keyed on the BIND ADDRESS rather than on gin mode:
//
//	A) METRICS_AUTH_TOKEN set  → Bearer required (401), in every mode.
//	B) no token, loopback bind, non-release → mounted, with a per-request
//	   loopback check (403 for a non-loopback peer) so local dev keeps working.
//	C) anything else (release mode, or a listener that is not loopback-only)
//	   → NOT mounted, plus a startup WARN: fail-closed.
//
// Rule C is the July 2026 hardening. Gin mode alone was the old key and it left
// a hole with two doors: a deployment that binds 0.0.0.0 under a dev-mode
// binary, and any deployment behind a reverse proxy — there the peer address is
// the proxy's loopback, so an in-handler loopback check reads "local" for a
// request that arrived from the internet. A bind-address decision cannot be
// defeated by a proxy or by a spoofed X-Forwarded-For, which is why it is the
// key. Verified 2026-09-22 against a remote master: GET /metrics answered 200
// with no Authorization header at all, exposing per-worker CPU/disk/network,
// queue depth, quarantine counters and cost models to anyone who could reach
// the port.
func (r *Router) registerMetricsRoute(engine *gin.Engine, log *zap.Logger) {
	// Prometheus metrics endpoint — FAIL-CLOSED (PR-METRICS-FAILCLOSED).
	metricsHandler := gin.WrapH(promhttp.Handler())
	token := os.Getenv("METRICS_AUTH_TOKEN")
	isRelease := r.cfg.ServerGinMode == gin.ReleaseMode

	switch {
	case token != "":
		// Authenticated regardless of mode. The comparison is constant-time:
		// the endpoint is network-reachable and `!=` on a secret leaks its
		// prefix through response timing.
		expected := "Bearer " + token
		engine.GET("/metrics", func(c *gin.Context) {
			if subtle.ConstantTimeCompare([]byte(c.GetHeader("Authorization")), []byte(expected)) != 1 {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			metricsHandler(c)
		})
	case !r.cfg.ServerLoopbackOnly:
		// FAIL-CLOSED: the listener is not loopback-only, so a token is the
		// only safe way to serve this surface on it. A front proxy cannot hide
		// this case from us because the decision is made on the bind address.
		log.Warn("/metrics not mounted: server is not bound to a loopback address and METRICS_AUTH_TOKEN is unset (fail-closed). Set METRICS_AUTH_TOKEN=<64-hex> and restart, or bind 127.0.0.1.")
	case isRelease:
		// FAIL-CLOSED: token MUST be set in release mode.
		log.Warn("/metrics not mounted: METRICS_AUTH_TOKEN is required in release mode (fail-closed). Set METRICS_AUTH_TOKEN=<64-hex> and restart to enable.")
	default:
		// Dev/local (non-release) on a loopback bind: per-request loopback
		// restriction.
		// Uses c.Request.RemoteAddr (NOT c.ClientIP()) because ClientIP
		// respects X-Forwarded-For / X-Real-Ip headers that a non-loopback
		// client could spoof to bypass the restriction. RemoteAddr is the
		// raw TCP peer address and cannot be header-spoofed.
		engine.GET("/metrics", func(c *gin.Context) {
			host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
			var ip net.IP
			if err == nil {
				ip = net.ParseIP(host)
			}
			if ip != nil && ip.IsLoopback() {
				metricsHandler(c)
				return
			}
			c.AbortWithStatus(http.StatusForbidden)
		})
	}
}
