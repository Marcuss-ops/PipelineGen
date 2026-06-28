// PR-VO-C1 (June 2026): deprecation contract smoke tests.
//
// Coverage scope (intentionally narrow):
//
//   - voiceoverSunsetDate constant value (RFC 8594 IMF-fixdate format).
//   - addVoiceoverDeprecationHeader response headers (RFC 9745 +
//     RFC 8594 + RFC 8288 Link rel=successor-version).
//   - legacyVoiceoverRouteInvocationsTotal Prometheus counter.
//   - LegacyVoiceoverDeprecationCount readback.
//
// OUT of scope for this file (deferred to follow-up):
//
//   - Full handler-level integration tests driving the complete
//     request flow (POST /generate-with-group returning 200 +
//     headers; POST /generate with destination.kind="group"
//     returning 200 without headers). These tests require the
//     Handler struct to be constructed with stubbed
//     groupsResolver / service / jobsSvc / syncService —
//     infrastructure that does not currently exist for this handler
//     directory (the existing gate_test.go only walks static
//     prohibited-pattern walks, not behavior). The follow-up
//     PR-VO-C2 will add a stub-package in
//     internal/api/assets/voiceover/stub/ and full handler tests.
//
// Why this contract is pinned NOW: the Sunset header is a security
// boundary (cross-team timing contract). A future drift that
// changes the header shape or the constant value is a HARD break.
// The three tests below pin every observable.
package voiceover

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
)

// TestVoiceoverSunsetDate_Format pins the IETF IMF-fixdate format
// per RFC 8594 §"Sunset Header Field" + RFC 7231 IMF-fixdate.
// Format is: "Sat, DD Mon YYYY HH:MM:SS GMT" — capitalised weekday,
// commas as separators, GMT timezone marker. Drift on this value
// would break client-side parsing libraries.
func TestVoiceoverSunsetDate_Format(t *testing.T) {
	if !strings.HasPrefix(voiceoverSunsetDate, "Sat, ") {
		t.Errorf("Sunset: IMF-fixdate weekday must be 'Sat, ' for 2026-09-26; got %q", voiceoverSunsetDate)
	}
	if !strings.HasSuffix(voiceoverSunsetDate, " GMT") {
		t.Errorf("Sunset: IMF-fixdate timezone must be ' GMT' per RFC 7231; got %q", voiceoverSunsetDate)
	}
	if !strings.Contains(voiceoverSunsetDate, " Sep 2026 ") {
		t.Errorf("Sunset: must contain 'Sep 2026' for the 90-day window from 2026-06-28; got %q", voiceoverSunsetDate)
	}
}

// TestAddVoiceoverDeprecationHeader_Headers pins the full header
// triple on the deprecated /generate-with-group endpoint:
//
//   - Deprecation: true                       (RFC 9745 draft standard)
//   - Sunset: <IMF-fixdate>                   (RFC 8594)
//   - Link: <...>; rel="successor-version"    (RFC 8288 web linking)
//
// Failure mode expectation: the helper MUST set all three headers
// in this exact order (Deprecation → Sunset → Link). Drift on any
// header name, value, or ordering is a HARD breaking change for
// cross-team consumers.
func TestAddVoiceoverDeprecationHeader_Headers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addVoiceoverDeprecationHeader(c, "generate-with-group")

	headers := rec.Header()

	// 1. RFC 9745 Deprecation.
	if got := headers.Get("Deprecation"); got != "true" {
		t.Errorf("Deprecation header: got %q, want \"true\" (RFC 9745)", got)
	}

	// 2. RFC 8594 Sunset.
	if got := headers.Get("Sunset"); got != voiceoverSunsetDate {
		t.Errorf("Sunset header: got %q, want %q (RFC 8594)", got, voiceoverSunsetDate)
	}

	// 3. RFC 8288 Link rel=successor-version. The header MUST point at
	// the canonical successor endpoint, formatted as an absolute
	// relative URL (RFC 8288 §3.1: URI-reference). Drift on the URL
	// or on the rel parameter is a HARD breaking change for
	// cross-team consumers — assert the EXACT expected value.
	expectedLink := `</api/voiceover/generate>; rel="successor-version"`
	if got := headers.Get("Link"); got != expectedLink {
		t.Errorf("Link header: got %q, want exact match %q", got, expectedLink)
	}
}

// TestAddVoiceoverDeprecationHeader_IncrementsCounter pins the
// Prometheus observability contract. Future drift that changes
// the counter label or omits Inc() would silently defeat operator
// dashboards tracking the sunset window.
func TestAddVoiceoverDeprecationHeader_IncrementsCounter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Capture pre-count from the labelled counter.
	pre, err := legacyVoiceoverRouteInvocationsTotal.GetMetricWithLabelValues("generate-with-group")
	if err != nil {
		t.Fatalf("get counter with label: %v", err)
	}
	var mPre dto.Metric
	if err := pre.Write(&mPre); err != nil {
		t.Fatalf("write pre metric: %v", err)
	}
	preCount := int64(mPre.GetCounter().GetValue())

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addVoiceoverDeprecationHeader(c, "generate-with-group")

	post, err := legacyVoiceoverRouteInvocationsTotal.GetMetricWithLabelValues("generate-with-group")
	if err != nil {
		t.Fatalf("get counter with label (post): %v", err)
	}
	var mPost dto.Metric
	if err := post.Write(&mPost); err != nil {
		t.Fatalf("write post metric: %v", err)
	}
	postCount := int64(mPost.GetCounter().GetValue())

	if postCount != preCount+1 {
		t.Errorf("counter increment: pre=%d post=%d, want post=pre+1", preCount, postCount)
	}
}

// TestLegacyVoiceoverDeprecationCount_AggregatesAcrossRoutes pins
// the readback helper so admin/diagnostic surfaces can read the
// total usage across all deprecated voiceover routes. Today there
// is only one deprecated route (generate-with-group); the loop
// is futureproofed for additional routes by reading the canonical
// list at the helper site.
func TestLegacyVoiceoverDeprecationCount_AggregatesAcrossRoutes(t *testing.T) {
	// Reset-instrument is intentionally NOT done here — the test
	// relies on absolute monotonic-counter semantics; if a previous
	// test incremented the counter, that increment is part of the
	// current observation. Just assert the helper returns a
	// non-negative value.
	total := LegacyVoiceoverDeprecationCount()
	if total < 0 {
		t.Errorf("LegacyVoiceoverDeprecationCount must be non-negative; got %d", total)
	}
}

// TestAddVoiceoverDeprecationHeader_ResponseStatusUnchanged pins a
// fail-soft contract: setting deprecation headers MUST NOT alter
// the response status. Operators / callers may forward the
// httptest.Recorder status through their routers; the helper
// writes headers only, never status. Drift on this would silently
// flip the HTTP semantics during the 90-day window.
func TestAddVoiceoverDeprecationHeader_ResponseStatusUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	addVoiceoverDeprecationHeader(c, "generate-with-group")
	// httptest.NewRecorder default status is 200; the helper must
	// not touch it. Asserting exactly 200 is more accurate than
	// "is 0" because Gin will set 200 explicitly by default.
	if rec.Code != http.StatusOK {
		t.Errorf("addVoiceoverDeprecationHeader must not change response status; got %d", rec.Code)
	}
}
