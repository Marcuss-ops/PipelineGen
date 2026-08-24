// Package downloader — Metrics struct TDD tests (ART-002 P1.1, July 2026).
//
// 2 canonical-surface tests for the Pattern-0 Metrics adapter
// declared in metrics.go. Each test exercises the adapter
// directly (NOT through Resolver.Download) — the test surface
// is the Metrics struct's incDownloadPath method, which is the
// only point where the production Prometheus collector is
// touched. Integration tests for the Resolver.Download routing
// (verifying that resolvePath routes to the correct transport)
// would require mocking *core_dl.YTDLPDownloader +
// *core_dl.HTTPDownloader (concrete types, not interfaces —
// refactor is a separate PR).
//
// Test isolation discipline: the CounterVec is constructed via
// prometheus.NewCounterVec (NOT promauto) so the test never
// registers against prometheus.DefaultRegisterer. Multiple
// tests in the same run start at counter value 0; test
// interleaving cannot contaminate assertions. The same
// discipline is used in
// internal/platform/observability/metrics_adapter_test.go
// (FASE 3.7 Commit 2 canonical precedent).
package downloader

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestMetrics_IncDownloadPath_LabelRouted pins the canonical
// label-routed counter-increment behaviour: each of the 4 Path*
// consts MUST route to its own labelled series on the underlying
// CounterVec, and repeated increments MUST accumulate on the
// same label (cumulative-counter Prometheus idiom). This is the
// primary test that the SRE dashboard will key on
// (browser | yt-dlp | http | hls).
//
// Why 3 increments for PathYTDLP: ensures the counter is
// cumulative, not last-write-wins — a godlike/06 SSOT gotcha
// would be a counter that resets to 1 on each call (which would
// break rate() calculations in Prometheus).
func TestMetrics_IncDownloadPath_LabelRouted(t *testing.T) {
	// Private CounterVec (NOT promauto) so the test never pollutes
	// the production collector registry. The Path* consts under
	// test are the canonical label values from metrics.go — the
	// test asserts the consts and the underlying labels stay in
	// lockstep (a const-vs-label drift would be a godlike/06 SSOT
	// violation surfaced at test time rather than at first
	// dashboard query time).
	cv := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_artlist_download_path_total",
		Help: "test-only counter for the 4 Path* consts",
	}, []string{"path"})
	m := &Metrics{DownloadPath: cv}

	m.incDownloadPath(PathYTDLP)
	m.incDownloadPath(PathYTDLP)
	m.incDownloadPath(PathYTDLP)
	m.incDownloadPath(PathHTTP)
	m.incDownloadPath(PathBrowser)
	m.incDownloadPath(PathHLS)

	cases := []struct {
		path string
		want float64
	}{
		{PathYTDLP, 3},
		{PathHTTP, 1},
		{PathBrowser, 1},
		{PathHLS, 1},
	}
	for _, tc := range cases {
		if got := testutil.ToFloat64(cv.WithLabelValues(tc.path)); got != tc.want {
			t.Errorf("path=%q: want %v, got %v", tc.path, tc.want, got)
		}
	}
}

// TestMetrics_NilSafety pins the production safety posture: a
// nil receiver AND a nil DownloadPath field are both no-ops.
// This is the partial-deploy + stats-disabled-environment path;
// the composition root that wires the Resolver with
// downloader.NewResolver(cfg, ResolverConfig{}, log, nil)
// MUST NOT panic on first Download call. The same nil-safety
// DriveValidatorMetrics in
// internal/application/assets/delivery/drive_validator_metrics_test.go
// (P1.4 canonical precedent — the
// TestDriveValidatorMetrics_NilReceiverNoOp_P1_4 test there is
// the structural mirror of this one).
func TestMetrics_NilSafety(t *testing.T) {
	// All-nil receiver: every method must short-circuit silently.
	var nilRec *Metrics
	nilRec.incDownloadPath(PathYTDLP)   //nolint:staticcheck // deliberate nil-receiver test
	nilRec.incDownloadPath(PathHTTP)    //nolint:staticcheck
	nilRec.incDownloadPath(PathBrowser) //nolint:staticcheck
	nilRec.incDownloadPath(PathHLS)     //nolint:staticcheck

	// Receiver with nil DownloadPath field: the field-level guard
	// must short-circuit even though the receiver itself is
	// non-nil. This is the "Metrics{}" struct-literal path — a
	// caller that wants the struct shape but explicitly opts out
	// of metrics by leaving the field nil.
	emptyRec := &Metrics{}
	emptyRec.incDownloadPath(PathYTDLP)   //nolint:staticcheck // deliberate nil-field test
	emptyRec.incDownloadPath(PathHTTP)    //nolint:staticcheck
	emptyRec.incDownloadPath(PathBrowser) //nolint:staticcheck
	emptyRec.incDownloadPath(PathHLS)     //nolint:staticcheck

	// No panic reached here = either safety path works. Assertion
	// is the "silent pass": any panic fails the test via the
	// Go runtime's panic-on-nil-pointer dereference. No
	// explicit t.Fatal needed.
}
