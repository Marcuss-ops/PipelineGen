// Package observability — Artlist download-path metrics (ART-002 P1.1, July 2026).
//
// SRE surface for the Artlist download path distribution. The
// artlist_download_path_total counter partitions every Artlist
// download attempt by which path/transport actually carried the
// bytes, so operators can dashboard the live split between the
// Go-primary surface (yt-dlp + HTTP) and the Node-fallback
// surface (browser Puppeteer).
//
// Cardinality (bounded, no risk):
//   - path: 4 canonical values (browser | yt-dlp | http | hls).
//   - Total series: ≤ 4 (one per path label, populated lazily
//     on first WithLabelValues().Inc() call per the Prometheus
//     CounterVec convention).
//
// Forward-pointer (PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY, deadline
// 2026-08-15): the 4-label cardinality is stable across the
// planned unification — whichever surface becomes SSOT will
// continue to emit one of the same 4 labels, so dashboards
// built against this metric today survive the unification
// (label set is intentionally future-proofed for the
// HLS-direct handler that the unification PR may add).
//
// Cross-ref: downloader/metrics.go (path-label consts
// PathBrowser / PathYTDLP / PathHTTP / PathHLS); the canonical
// wire-up is in internal/app/build_bundles_artlist.go::WireArtlist
// (composition root injects downloader.NewMetrics() into
// downloader.New).
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ArtlistDownloadPathTotal counts Artlist download attempts
	// partitioned by the path/transport that actually carried the
	// bytes. Labels (canonical 4-value set, mirrored as consts in
	// downloader/metrics.go):
	//   - "yt-dlp": HLS via yt-dlp (this Go package's HLS path —
	//               the .m3u8 detection gate in
	//               downloader.go::Download routes here).
	//   - "http":   progressive HTTP download (this Go package's
	//               non-HLS path; the else branch in
	//               downloader.go::Download).
	//   - "hls":    direct HLS without yt-dlp (reserved — no
	//               production caller fires this label today; it
	//               exists for forward-compat with
	//               PR-ARTLIST-DOWNLOAD-SURFACE-UNIFY's HLS-direct
	//               handler which would short-circuit the
	//               .m3u8→yt-dlp escalation in favour of a
	//               purpose-built HLS client).
	//   - "browser": Node Puppeteer fallback (the downloadViaScraper
	//                path in
	//                internal/platform/media/processor/processor_download.go;
	//                this Go package doesn't dispatch to it directly
	//                today, but the label exists for cross-surface
	//                observability — see PR-ART-002 P0.2 godoc
	//                "Divider" paragraph for the per-invocation
	//                sequential gate that prevents double-fire).
	//
	// Mis-spelled label values create new time-series silently
	// (a textbook Prometheus footgun). The 4 consts in
	// downloader/metrics.go are the canonical call-site surface
	// — always use the consts, never raw strings.
	ArtlistDownloadPathTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "artlist_download_path_total",
		Help: "Total number of Artlist download attempts partitioned by the path/transport that carried the bytes (browser | yt-dlp | http | hls). Increments PER ATTEMPT (not per completion) — a single Download that retries 3 times on a flaky HLS CDN adds 3 to the counter; rate() over this metric is the attempt rate, not the success rate. Dashboards MUST treat rate() as attempt pressure, not completion volume.",
	}, []string{"path"})

	// ArtlistScraperHealthAlertsTotal counts alerts fired by the
	// Artlist Node scraper health probe (ART-002 P1.3, July 2026)
	// when the consecutive-failure counter crosses the threshold
	// (default 3). One increment per alert (not per failed probe) —
	// the alert-once-per-streak semantic is the canonical SRE
	// contract: 3 consecutive failures = 1 alert, 6 consecutive
	// failures = 2 alerts (one per streak of 3).
	//
	// Cardinality: 1 series. Dashboards: rate() this for alert
	// pressure over time; pair with artlist_scraper_probe_total
	// to compute the alert-to-failure ratio.
	ArtlistScraperHealthAlertsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "artlist_scraper_health_alerts_total",
		Help: "Total number of Artlist Node scraper health alerts fired when the consecutive-failure counter crossed the threshold (default 3). One increment per alert event (alert-once-per-streak).",
	})

	// ArtlistScraperProbeResultTotal counts each probe attempt
	// (NOT per alert). Labels:
	//   - "success": HTTP response received (any 2xx/3xx/4xx/5xx
	//                — the probe is a liveness check, not a
	//                functional smoke test; any response means the
	//                server is accepting connections).
	//   - "failure": transport-level error (connection refused,
	//                timeout, DNS failure, TLS error). The
	//                consecutive-failure counter is incremented
	//                ONLY on "failure" outcomes; "success" resets
	//                it to 0.
	//
	// Cardinality: 2 series max. Dashboards: rate() per label to
	// see success vs failure pressure; alert ratio = alerts / failures.
	ArtlistScraperProbeResultTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "artlist_scraper_probe_total",
		Help: "Total number of Artlist Node scraper health probe attempts partitioned by result (success | failure). Success = any HTTP response received (liveness check); failure = transport-level error. Increments PER ATTEMPT (one per 60s tick by default).",
	}, []string{"result"})
)
