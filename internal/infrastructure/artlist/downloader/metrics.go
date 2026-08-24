// Package downloader — Metrics struct for Artlist download-path
// Prometheus counters (ART-002 P1.1, July 2026).
//
// Per AGENTS.md Pattern 0, the struct is composition-injected
// (passed as the 3rd arg to New) rather than package-globally
// imported. Production wiring (internal/app/build_bundles_artlist.go
// ::WireArtlist) cables NewMetrics() against the promauto global
// observability.ArtlistDownloadPathTotal; tests construct a fresh
// Metrics from a private prometheus.NewCounterVec so they do not
// pollute the production collector registry (see
// downloader_metrics_test.go for the canonical TDD surface).
//
// nil-safety: the struct pointer may be nil. incDownloadPath
// short-circuits when called on a nil receiver AND when the
// DownloadPath field is nil — composition roots that pre-date
// the metrics surface (or want to run the downloader in a
// stats-disabled environment) can pass nil without guard
// boilerplate at the call site. This mirrors the
// DriveValidatorMetrics nil-safety contract in
// internal/platform/delivery/drive_validator_metrics.go
// (P1.4 canonical precedent).
package downloader

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// Metrics groups the Prometheus collectors that the downloader
// emits. The pointer is injected via New as the 3rd arg; passing
// nil disables metrics emission entirely (no-op observers).
//
// Field visibility is intentional: tests may construct a struct
// literal with a locally-registered CounterVec (NOT promauto) and
// then assert increments via testutil.ToFloat64 (see
// downloader_metrics_test.go).
//
// Production construction: NewMetrics() backs the field with the
// promauto global from
// internal/platform/observability/metrics_artlist.go —
// that global auto-registers with prometheus.DefaultRegisterer
// and is surfaced via api/routes.go::/metrics (promhttp.Handler()).
type Metrics struct {
	// DownloadPath is the counter incremented per download
	// attempt. Labelled by "path" (browser | yt-dlp | http | hls;
	// see the 4 Path* consts below). Cardinality bounded at 4
	// series max.
	DownloadPath *prometheus.CounterVec
}

// NewMetrics constructs the production metrics struct backed by
// the package-level promauto global. The returned pointer is
// stable across calls — the field points at the same promauto
// collector every caller sees, so increments accumulate on the
// default registry as expected.
func NewMetrics() *Metrics {
	return &Metrics{
		DownloadPath: observability.ArtlistDownloadPathTotal,
	}
}

// incDownloadPath records a single download-path observation.
// Safe to call on a nil receiver (m == nil short-circuits the
// whole call); per-field nil guard runs independently so a
// partial struct (only DownloadPath wired) still increments the
// wired collector while a fully-empty struct is a no-op.
//
// path MUST be one of the 4 Path* consts declared below —
// caller-side enforcement only via the constants; mis-spelled
// values create new time-series silently (a textbook Prometheus
// footgun, so the convention is documented near the call site
// in downloader.go::Download).
func (m *Metrics) incDownloadPath(path string) {
	if m == nil || m.DownloadPath == nil {
		return
	}
	m.DownloadPath.WithLabelValues(path).Inc()
}

// Canonical path-label constants. These are the 4 documented
// time-series labels for artlist_download_path_total{path=...}.
// Mirrored in observability/metrics_artlist.go package doc
// (which carries the per-label semantics). Always use these
// consts at call sites — never raw strings.
const (
	// PathBrowser is the Node Puppeteer path. Fired by
	// downloader.Resolver.Download when resolvePath picks the
	// scraper transport (Rule 1: ClipPageURL set OR Artlist-shaped
	// URL) or as the first-resort in the controlled fallback ladder.
	PathBrowser = "browser"

	// PathYTDLP is the HLS-via-yt-dlp path. Fired by
	// downloader.Resolver.Download when resolvePath picks the
	// yt-dlp transport (Rule 3: .m3u8 URL, non-Artlist). This
	// is the dominant path for HLS-CDN assets where yt-dlp's
	// cookie impersonation succeeds (non-Artlist sources).
	PathYTDLP = "yt-dlp"

	// PathHTTP is the progressive-HTTP path. Fired by
	// downloader.Resolver.Download when resolvePath picks the
	// HTTP transport (Rule 2: direct .mp4/.mov/.avi URLs) or
	// as the last-resort fallback in the controlled ladder.
	// This is the dominant path for direct progressive assets.
	PathHTTP = "http"

	// PathHLS is the direct-HLS-without-yt-dlp path. Reserved
	// — no production caller fires this label today. The
	// constant exists for forward-compat with
	// PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY's HLS-direct handler
	// which would short-circuit the .m3u8→yt-dlp escalation
	// in favour of a purpose-built HLS client (potentially
	// reusing the cookie-impersonation path but bypassing
	// yt-dlp's HLS-merge overhead).
	PathHLS = "hls"
)
