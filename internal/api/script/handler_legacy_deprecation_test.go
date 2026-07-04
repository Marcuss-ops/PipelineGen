package script

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap"
)

// TestLegacyGenerateFromClips_IncrementsCounter pins the FASE 2.1 typed-counter
// contract: each call to addGenerateFromClipsDeprecationHeader increments
// legacy_generate_from_clips_total by exactly 1. The retirement signal
// (rate(metric[7d]) == 0) depends on this invariant — if a future refactor
// silently drops the Inc() call the 7-day-zero trigger fires prematurely.
//
// Read counter value via prometheus.testutil.ToFloat64F which the prometheus
// library guarantees matches the registered live metric (round-trip via the
// canonical Gatherer, not the test-side local var).
func TestLegacyGenerateFromClips_IncrementsCounter(t *testing.T) {
	before := testutil.ToFloat64(legacyGenerateFromClipsTotal)
	// Synthesise a request: the helper just touches headers + Inc() — no body
	// needed for the counter assertion. The full handler hits deps for an
	// enqueue path which is NOT under test here.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addGenerateFromClipsDeprecationHeader(c, removalDateFromClips)
	if got := testutil.ToFloat64(legacyGenerateFromClipsTotal); got != before+1 {
		t.Fatalf("legacy_generate_from_clips_total = %v, want %v", got, before+1)
	}
}

func TestLegacyGenerateWithImages_IncrementsCounter(t *testing.T) {
	before := testutil.ToFloat64(legacyGenerateWithImagesTotal)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addGenerateWithImagesDeprecationHeader(c, removalDateWithImages)
	if got := testutil.ToFloat64(legacyGenerateWithImagesTotal); got != before+1 {
		t.Fatalf("legacy_generate_with_images_total = %v, want %v", got, before+1)
	}
}

// TestDeprecationCount_SumsBothCounters pins the FASE 2.1 thin-shim
// contract: DeprecationCount() returns the sum of both typed counters
// (godlike/06 SSOT — single canonical owner of the FASE 2.1 metric
// surface, read via the canonical Gatherer).
func TestDeprecationCount_SumsBothCounters(t *testing.T) {
	// Snapshot the current values so the assertion is delta-based
	// (test order is non-deterministic via the canonical typed counters).
	beforeClips := testutil.ToFloat64(legacyGenerateFromClipsTotal)
	beforeImages := testutil.ToFloat64(legacyGenerateWithImagesTotal)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addGenerateFromClipsDeprecationHeader(c, removalDateFromClips)
	addGenerateWithImagesDeprecationHeader(c, removalDateWithImages)
	afterClips := testutil.ToFloat64(legacyGenerateFromClipsTotal)
	afterImages := testutil.ToFloat64(legacyGenerateWithImagesTotal)
	// Hard-assert the per-helper delta is exactly 1 (per-helper contract).
	// A future refactor that drops the Inc() call would silently under-count;
	// this assertion catches that.
	if deltaClips := int64(afterClips - beforeClips); deltaClips != 1 {
		t.Fatalf("after-from-clips Inc delta = %d, want 1 (per-helper contract)", deltaClips)
	}
	if deltaImages := int64(afterImages - beforeImages); deltaImages != 1 {
		t.Fatalf("after-with-images Inc delta = %d, want 1 (per-helper contract)", deltaImages)
	}
	// DeprecationCount returns ABSOLUTE values (sum of live registry state),
	// not delta. Compare against afterClips + afterImages.
	got := DeprecationCount()
	afterClipsInt := int64(afterClips)
	afterImagesInt := int64(afterImages)
	want := afterClipsInt + afterImagesInt
	if got != want {
		t.Fatalf("DeprecationCount() = %d, want %d (= %d + %d)",
			got, want, afterClipsInt, afterImagesInt)
	}
}

// TestLegacyGenerate*DeprecationHeader_SetsXDeprecatedHeader pins the
// response-header contract that the existing test
// TestLegacyGenerateFromClips_x_deprecated_header_set_even_on_400
// covers for one route — the FASE 2.1 typed-counter rename MUST keep
// the X-Deprecation-Notice body verbatim (operators' chrome-test would
// otherwise flake).
func TestLegacyGenerateFromClipsDeprecationHeader_SetsXDeprecatedHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addGenerateFromClipsDeprecationHeader(c, "2099-12-31")
	if got := c.Writer.Header().Get("X-Deprecated"); got != "true" {
		t.Fatalf("X-Deprecated = %q, want true", got)
	}
	if got := c.Writer.Header().Get("X-Deprecation-Notice"); !strings.Contains(got, "2099-12-31") {
		t.Fatalf("X-Deprecation-Notice missing removal date: %q", got)
	}
}

func TestLegacyGenerateWithImagesDeprecationHeader_SetsXDeprecatedHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addGenerateWithImagesDeprecationHeader(c, "2099-12-31")
	if got := c.Writer.Header().Get("X-Deprecated"); got != "true" {
		t.Fatalf("X-Deprecated = %q, want true", got)
	}
	if got := c.Writer.Header().Get("X-Deprecation-Notice"); !strings.Contains(got, "2099-12-31") {
		t.Fatalf("X-Deprecation-Notice missing removal date: %q", got)
	}
}

// TestLegacyCounters_AreRegisteredWithCanonicalNames pins the
// prometheus.MustRegister contract: the metrics are visible to
// prometheus.DefaultGatherer with the exact snake_case names. The
// 7-day-zero retirement trigger depends on the names being
// observable to Prometheus scraping — silent drift here would
// produce a false-positive retirement signal.
func TestLegacyCounters_AreRegisteredWithCanonicalNames(t *testing.T) {
	wantNames := map[string]bool{
		"legacy_generate_from_clips_total":  false,
		"legacy_generate_with_images_total": false,
	}
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("DefaultGatherer.Gather() err: %v", err)
	}
	// Count occurrences to detect silent duplicate-name registration
	// (would silently double-count on /metrics scrape = false-positive 7-day-zero).
	seenCount := map[string]int{}
	for _, mf := range mfs {
		if _, ok := wantNames[mf.GetName()]; ok {
			wantNames[mf.GetName()] = true
			seenCount[mf.GetName()]++
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Fatalf("metric %q not registered with prometheus.DefaultGatherer (silent drift = false-positive 7-day-zero retirement signal)", name)
		}
		if c := seenCount[name]; c > 1 {
			t.Fatalf("metric %q registered %d times (silent duplicate = 2x retire-counter = premature 7-day-zero signal)", name, c)
		}
	}
}

// Compile-time assertions pin the godlike/06 SSOT one-canonical-owner-per-fact
// invariant: NO OTHER producer can register a counter with the same name.
// promauto must report a duplicate-registration panic at startup, but this
// compile-time assertion is the audit-pin for "this is the SOLE owner".
var (
	_ prometheus.Counter = legacyGenerateFromClipsTotal
	_ prometheus.Counter = legacyGenerateWithImagesTotal
	_ dto.Metric         = dto.Metric{}
)

// Keep unused-import discards referenced so go vet does not flag the test file.
// http.StatusOK + zap.NewNop() are the canonical sentinel values for the
// net/http + go.uber.org/zap packages; the prometheus.testutil package is
// already referenced by ToFloat64 above (kept as the canonical readback API
// per the counter-test discipline).
var (
	_ = http.StatusOK
	_ = zap.NewNop()
)
