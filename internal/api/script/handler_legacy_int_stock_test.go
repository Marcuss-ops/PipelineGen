// Package script — handler_legacy_int_stock_test.go. RETIRED by
// PR-script-legacy-contract (Jul 2026, P0 ABSOLUTE). The legacy
// /api/script/generate-from-clips endpoint no longer enqueues a
// script.generate job — it returns 410 Gone with the canonical
// deprecation payload pointing to POST /api/script/generate V2. The
// StockAssociationProcessor fallback contract is now exercised by
// the canonical V2 path (handler_generate_handler.go + the new
// StockBinding contract tests). The 410-Gone contract itself is
// pinned by TestLegacyGenerateFromClips_Returns410AndBody_PinContract
// + TestLegacyGenerateWithImages_Returns410AndBody_PinContract (new
// TDD tests in handler_legacy_deprecation_test.go).
//
// Pre-CR shape (preserved as audit-pin only): the test used
// pkg/veloxclient.SubmitAsync on the legacy endpoint with the Jackie
// Chan canned terminal result to verify StockAssociationProcessor
// fallback. Post-CR: the canonical /api/script/generate V2 path
// carries that contract — operator dashboards that alerted on the
// "StockBinding.Fallback" wire shape should pivot their probes to
// the V2 endpoint, not the retired /generate-from-clips. Forward-
// pointer: PR-V2-STOCK-FALLBACK-CONTRACT (2026-08-15) — wire a
// matching V2 integration test for the StockBinding contract.
//
// ── PR-LEGACY-RETIRE-INT-STOCK-MIGRATION (Aug 2026) ──────────────
//
// The file was previously a single t.Skip placeholder. It is now
// MIGRATED to exercise the canonical LegacyDeprecationPayload contract
// from the int_stock perspective, with 3 new tests asserting:
//
//	(a) counter increment even on invalid payload (godlike/07
//	    observability contract — the rate(metric[7d]) == 0
//	    retirement trigger depends on this invariant)
//	(b) wire shape identical header+body to canonical
//	    LegacyDeprecationPayload (weak-typed key set check +
//	    strong-typed round-trip — catches BOTH "extra keys leak
//	    surface" AND "missing keys break wire contract")
//	(c) status 410 always (godlike/07 minimum-blast-radius: the 410
//	    contract is unconditional regardless of payload)
//
// godlike/06 SSOT: the canonical wire shape is declared at
// internal/api/script/handler_legacy_deprecation.go::LegacyDeprecationPayload;
// these tests pin it from the int_stock angle (the
// handler_legacy_deprecation_test.go tests cover the same shape
// from the deprecation angle with strong-typed round-trip only;
// the int_stock tests use weak-typed map inspection to ALSO catch
// the "extra keys leak canonical surface" failure class).
package script

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
)

// TestLegacyIntStock_CounterIncrements_EvenOnInvalidPayload pins the
// godlike/07 observability contract: the legacy_generate_*_total
// counter MUST increment regardless of payload validity. The counter
// is the authoritative signal for the rate(metric[7d]) == 0
// retirement trigger (per the FREEZE-phase observability invariant
// in handler_legacy_deprecation.go); if invalid payloads were
// silently skipped, operators would lose the observability signal
// for callers that send malformed JSON, garbage bytes, empty
// bodies, or no body at all.
//
// 4 invalid-payload variants are exercised across 2 routes (2
// from-clips + 2 with-images = 4 total counter increments, split
// 2/2 per route). Counter delta is asserted per-route against the
// live registry readback via testutil.ToFloat64 (the canonical
// prometheus readback API per the deprecation test discipline —
// reads from the live registry, not a test-side local copy, so
// other tests' contributions are isolated by the delta).
//
// godlike/07 race-protect: this test is NOT marked t.Parallel()
// because it snapshots the global counter state (prometheus
// package-level vars) before + after the 4 requests. The status-410
// test (TestLegacyIntStock_Status410_Always) marks its sub-cases
// t.Parallel() but those sub-cases only assert the status code,
// not the counter — so the race window is one-way (parallel
// sub-cases COULD inflate our delta, but the status test never
// reads the counter). Removing t.Parallel() here is the cheap
// defense — counter test runs sequentially, status-410 test can
// still parallelize internally.
func TestLegacyIntStock_CounterIncrements_EvenOnInvalidPayload(t *testing.T) {
	// No t.Parallel() — see race-protect note in the function
	// godoc above. This test snapshots the global counter state
	// and asserts an exact delta; serialization vs. other
	// package tests is the cleanest correctness invariant.

	router := gin.New()
	rg := router.Group("/api/script")
	h := &ScriptFlowHandler{log: zap.NewNop()}
	h.RegisterLegacyDeprecationRoutes(rg)

	beforeFromClips := testutil.ToFloat64(legacyGenerateFromClipsTotal)
	beforeWithImages := testutil.ToFloat64(legacyGenerateWithImagesTotal)

	// Variant 1: invalid JSON (the most common 400-class caller
	// error — proves the handler does NOT pre-validate the body).
	req1 := httptest.NewRequest("POST", "/api/script/generate-from-clips",
		strings.NewReader(`{not valid json`))
	req1.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req1)

	// Variant 2: empty body.
	req2 := httptest.NewRequest("POST", "/api/script/generate-with-images",
		strings.NewReader(""))
	req2.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req2)

	// Variant 3: no body at all (the most common "client cancelled
	// before request body sent" case — http.NoBody is what
	// httptest.NewRequest passes through).
	req3 := httptest.NewRequest("POST", "/api/script/generate-from-clips", nil)
	router.ServeHTTP(httptest.NewRecorder(), req3)

	// Variant 4: binary garbage (proves the handler does NOT
	// attempt to peek at the body — it just routes to the 410
	// surface unconditionally).
	req4 := httptest.NewRequest("POST", "/api/script/generate-with-images",
		strings.NewReader("\x00\x01\x02\x03"))
	router.ServeHTTP(httptest.NewRecorder(), req4)

	afterFromClips := testutil.ToFloat64(legacyGenerateFromClipsTotal)
	afterWithImages := testutil.ToFloat64(legacyGenerateWithImagesTotal)

	// 2 from-clips requests (req1 + req3) → delta == 2
	if delta := afterFromClips - beforeFromClips; delta != 2 {
		t.Fatalf("legacy_generate_from_clips_total delta = %v, want 2 (godlike/07 observability: counter must increment on invalid payload)\nbefore=%v after=%v", delta, beforeFromClips, afterFromClips)
	}
	// 2 with-images requests (req2 + req4) → delta == 2
	if delta := afterWithImages - beforeWithImages; delta != 2 {
		t.Fatalf("legacy_generate_with_images_total delta = %v, want 2 (godlike/07 observability: counter must increment on invalid payload)\nbefore=%v after=%v", delta, beforeWithImages, afterWithImages)
	}
}

// TestLegacyIntStock_WireShape_IdenticalToCanonicalPayload pins the
// wire shape from the WEAKLY-TYPED angle: decode the body into a
// generic map[string]any and assert the exact key set + types match
// the canonical LegacyDeprecationPayload struct
// (handler_legacy_deprecation.go).
//
// The complementary TestLegacyGenerateFromClips_Returns410AndBody_PinContract
// test (handler_legacy_deprecation_test.go) uses a strong-typed
// json.Unmarshal which would silently accept extra keys. This test
// catches the "extra keys leak canonical surface" failure class via
// the explicit len() == 5 assertion AND the per-key canonical set
// check (BOTH directions: unexpected keys are flagged, missing
// canonical keys are flagged).
//
// Header + body are checked together (godlike/06 SSOT: the wire
// shape is the union of HTTP body + X-Deprecation headers, not the
// body alone).
func TestLegacyIntStock_WireShape_IdenticalToCanonicalPayload(t *testing.T) {
	t.Parallel()

	router := gin.New()
	rg := router.Group("/api/script")
	h := &ScriptFlowHandler{log: zap.NewNop()}
	h.RegisterLegacyDeprecationRoutes(rg)

	req := httptest.NewRequest("POST", "/api/script/generate-from-clips",
		strings.NewReader(`{"topic":"observability"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// ── Strong-typed round-trip — pins the canonical struct shape
	// (godlike/06 SSOT: the wire is byte-equivalent to the
	// LegacyDeprecationPayload struct fields — adding a struct
	// field silently leaks to the wire; removing one breaks
	// downstream consumers). This complement to the weak-typed
	// check below catches the "struct field removed silently"
	// failure class (which the weak-typed check would also catch
	// via the key set, but the strong-typed check gives a
	// build-time signal on field rename).
	var payload LegacyDeprecationPayload
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("strong-typed body json.Unmarshal into LegacyDeprecationPayload failed: %v\nbody: %s", err, w.Body.String())
	}
	if payload.OK != false {
		t.Errorf("payload.OK = %v, want false (canonical wire literal)", payload.OK)
	}
	if payload.CanonicalEndpoint != "POST /api/script/generate" {
		t.Errorf("payload.CanonicalEndpoint = %q, want %q", payload.CanonicalEndpoint, "POST /api/script/generate")
	}
	if payload.RemovalDate != "2026-12-31" {
		t.Errorf("payload.RemovalDate = %q, want %q", payload.RemovalDate, "2026-12-31")
	}
	if !strings.Contains(payload.Error, "endpoint retired") {
		t.Errorf("payload.Error = %q, want substring %q", payload.Error, "endpoint retired")
	}
	if !strings.Contains(payload.DeprecationNoticeRef, "X-Deprecation-Notice") {
		t.Errorf("payload.DeprecationNoticeRef = %q, want substring %q", payload.DeprecationNoticeRef, "X-Deprecation-Notice")
	}

	// ── Weak-typed decode — surfaces the EXACT key set on the wire.
	// Catches the "extra keys leak canonical surface" failure class
	// that the strong-typed round-trip would silently accept
	// (json.Unmarshal into a struct ignores unknown fields).
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("weak-typed body json.Unmarshal failed: %v\nbody: %s", err, w.Body.String())
	}

	// The canonical 5-key surface (mirrors LegacyDeprecationPayload
	// struct fields exactly — 1:1 field-to-key mapping per the
	// json: struct tags in handler_legacy_deprecation.go).
	canonicalKeys := []string{
		"ok", "error", "canonical_endpoint", "removal_date", "deprecation_notice_ref",
	}
	canonicalSet := make(map[string]bool, len(canonicalKeys))
	for _, k := range canonicalKeys {
		canonicalSet[k] = true
	}

	// Assert: exactly N keys (no extra, no missing).
	if len(raw) != len(canonicalKeys) {
		var extra []string
		for k := range raw {
			if !canonicalSet[k] {
				extra = append(extra, k)
			}
		}
		t.Errorf("body has %d keys, want %d (extra keys leak canonical surface: %v)", len(raw), len(canonicalKeys), extra)
	}

	// Assert: NO unexpected keys (catches leaks even if len matches
	// via a hypothetical swap — defensive depth).
	for k := range raw {
		if !canonicalSet[k] {
			t.Errorf("body has unexpected key %q (canonical LegacyDeprecationPayload has exactly 5 keys: ok, error, canonical_endpoint, removal_date, deprecation_notice_ref)", k)
		}
	}

	// Assert: ALL canonical keys present (defense-in-depth — catches
	// the "len happens to match but a key is renamed" case).
	for _, k := range canonicalKeys {
		if _, ok := raw[k]; !ok {
			t.Errorf("body missing canonical key %q (wire-shape contract break)", k)
		}
	}

	// Assert: types + values match the canonical payload literal
	// (godlike/06 SSOT: the wire is the literal struct — no
	// derived defaults, no placeholder strings).
	if ok, _ := raw["ok"].(bool); ok != false {
		t.Errorf("body.ok = %v (%T), want false (bool)", raw["ok"], raw["ok"])
	}
	if s, _ := raw["error"].(string); !strings.Contains(s, "endpoint retired") {
		t.Errorf("body.error = %q, want substring %q", s, "endpoint retired")
	}
	if s, _ := raw["canonical_endpoint"].(string); !strings.Contains(s, "POST /api/script/generate") {
		t.Errorf("body.canonical_endpoint = %q, want substring %q", s, "POST /api/script/generate")
	}
	if s, _ := raw["removal_date"].(string); s != "2026-12-31" {
		t.Errorf("body.removal_date = %q, want %q", s, "2026-12-31")
	}
	if s, _ := raw["deprecation_notice_ref"].(string); !strings.Contains(s, "X-Deprecation-Notice") {
		t.Errorf("body.deprecation_notice_ref = %q, want substring %q", s, "X-Deprecation-Notice")
	}

	// Assert: X-Deprecation headers match the body's canonical values.
	// (godlike/06 SSOT: the wire shape is body + headers, not body
	// alone — the headers point operators to the same canonical
	// endpoints the body does, with the same removal date.)
	if got := w.Header().Get("X-Deprecated"); got != "true" {
		t.Errorf("X-Deprecated = %q, want true", got)
	}
	if got := w.Header().Get("X-Deprecation-Notice"); !strings.Contains(got, "2026-12-31") {
		t.Errorf("X-Deprecation-Notice missing removal date %q: %q", "2026-12-31", got)
	}
	if got := w.Header().Get("X-Deprecation-Notice"); !strings.Contains(got, "POST /api/script/generate is the canonical endpoint") {
		t.Errorf("X-Deprecation-Notice missing canonical-endpoint pointer: %q", got)
	}
}

// TestLegacyIntStock_Status410_Always pins the 410 status across all
// payload variants (godlike/07 minimum-blast-radius: the 410
// contract is unconditional — even a caller sending valid-looking
// JSON to the legacy endpoint should see 410, not 200). The 6
// sub-cases cover the failure classes the FREEZE-phase
// observability signal needs to be aware of: empty body, invalid
// JSON, legacy-shape payload (would-have-been-valid-pre-CR — the
// "Jackie Chan" shape), deprecation-payload-shape (would trick a
// naive parser into a 200), binary garbage, and no body.
//
// All sub-cases assert 410 — the wire shape is the canonical
// LegacyDeprecationPayload body regardless of input. The 410
// handler does NOT read the request body (the counter increment
// fires at handler entry, BEFORE any body inspection).
func TestLegacyIntStock_Status410_Always(t *testing.T) {
	t.Parallel()

	router := gin.New()
	rg := router.Group("/api/script")
	h := &ScriptFlowHandler{log: zap.NewNop()}
	h.RegisterLegacyDeprecationRoutes(rg)

	cases := []struct {
		name string
		body io.Reader
	}{
		{"empty_body", strings.NewReader("")},
		{"invalid_json", strings.NewReader(`{not valid json`)},
		{"legacy_clip_input_shape", strings.NewReader(`{"topic":"observability","clip_ids":["clip-a"],"language":"it"}`)},
		{"deprecation_payload_shape", strings.NewReader(`{"ok":false,"error":"custom","canonical_endpoint":"custom","removal_date":"custom","deprecation_notice_ref":"custom"}`)},
		{"binary_garbage", strings.NewReader("\x00\x01\x02\x03")},
		{"no_body", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// No t.Parallel() here — the counter test
			// (TestLegacyIntStock_CounterIncrements_EvenOnInvalidPayload)
			// snapshots the global counter state. The
			// 6 sub-cases in this loop would inflate
			// that counter window if they ran in parallel,
			// causing the counter test's delta assertion
			// to flake. Sub-cases are fast (single HTTP
			// roundtrip each), so serialization has
			// negligible cost.
			req := httptest.NewRequest("POST", "/api/script/generate-from-clips", tc.body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusGone {
				t.Errorf("status = %d, want %d (410 Gone unconditional regardless of payload: %s)\nbody: %s", w.Code, http.StatusGone, tc.name, w.Body.String())
			}
		})
	}
}
