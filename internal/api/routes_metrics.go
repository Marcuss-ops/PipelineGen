package api

import (
	"net"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.uber.org/zap"
)

// registerMetricsRoute mounts /metrics with the PR-METRICS-FAILCLOSED
// posture. In release mode METRICS_AUTH_TOKEN MUST be set; otherwise the
// route is NOT registered and the server emits a startup WARN. In
// dev/local modes the route is mounted as before (with token if set,
// without if not) so local dev workflows don't break.
func (r *Router) registerMetricsRoute(engine *gin.Engine, log *zap.Logger) {
	// Prometheus metrics endpoint — FAIL-CLOSED in release mode (PR-METRICS-FAILCLOSED).
	// In release mode, METRICS_AUTH_TOKEN MUST be set; otherwise the route
	// is NOT registered and the server emits a startup WARN. In dev/local
	// modes the route is mounted as before (with token if set, without
	// if not) so local dev workflows don't break.
	metricsHandler := gin.WrapH(promhttp.Handler())
	token := os.Getenv("METRICS_AUTH_TOKEN")
	isRelease := r.cfg.ServerGinMode == gin.ReleaseMode

	switch {
	case token != "":
		// Authenticated regardless of mode.
		engine.GET("/metrics", func(c *gin.Context) {
			if c.GetHeader("Authorization") != "Bearer "+token {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			metricsHandler(c)
		})
	case isRelease:
		// FAIL-CLOSED: token MUST be set in release mode. Route not mounted.
		log.Warn("/metrics not mounted: METRICS_AUTH_TOKEN is required in release mode (fail-closed). Set METRICS_AUTH_TOKEN=<64-hex> and restart to enable.")
	default:
		// Dev/local (non-release): loopback-only restriction.
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
