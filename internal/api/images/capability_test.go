package images

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	fullimagesapi "github.com/Marcuss-ops/PipelineGen/internal/api/fullimages"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
)

// TestGenerate_Returns410_AfterPR_AUDIT_3: PR-AUDIT-3 (2026-07-09)
// retired the legacy POST /api/images/generate endpoint to HTTP 410
// Gone, matching the legacy script-route precedent. The handler
// MUST NOT call GenerateSmartImage — every invocation returns 410
// with the canonical deprecation payload pointing to the replacement
// endpoint POST /api/images/generated/generate.
//
// godlike/07 NO-FAKE-AVAILABILITY: a future regression that
// re-activates the handler (calling GenerateSmartImage again) would
// surface as a test failure — the counter increments on every 410
// call (godlike/07 observability) so operators can track the 7-day
// sustained-zero gate via rate(legacy_images_generate_total[7d]).
func TestGenerate_Returns410_AfterPR_AUDIT_3(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := &ImagesHandler{
		service: &imgservice.Service{}, // zero-valued; handler never reaches service
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/images/generate",
		strings.NewReader(`{"prompt":"a black cat","width":512,"height":512}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	if rec.Code != http.StatusGone {
		t.Fatalf("expected HTTP 410 Gone, got %d. body=%s",
			rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	// Canonical deprecation payload fields (per legacyDeprecationPayload SSOT).
	requiredSubstrings := []string{
		`"ok":false`,
		`"canonical_endpoint":"POST /api/images/generated/generate"`,
		`"removal_date":"2026-12-31"`,
		`"deprecation_notice_ref":"PR-AUDIT-3`,
	}
	for _, want := range requiredSubstrings {
		if !strings.Contains(body, want) {
			t.Errorf("410 body must contain %q; got body=%s", want, body)
		}
	}

	// X-Deprecated response headers.
	if rec.Header().Get("X-Deprecated") != "true" {
		t.Errorf("410 response must include X-Deprecated: true header")
	}
	if !strings.Contains(rec.Header().Get("X-Deprecation-Notice"), "POST /api/images/generated/generate") {
		t.Errorf("X-Deprecation-Notice must reference the canonical endpoint; got %q",
			rec.Header().Get("X-Deprecation-Notice"))
	}
}

// TestGenerateFullImages_Returns501_WhenVideoAIRequestedButNotImplemented
// REMOVED (June 2026 PR cleanup): CapVideoAI capability was deleted.
// The video_ai capability gate in the handler has been removed.

// TestGenerateFullImages_GoogleVidsEngine_ReturnsBadRequest — locks
// the PR-IMG-LEGACY-4 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, EXPAND phase,
// deadline 2026-08-15) contract: when a section requests Engine="google-vids"
// (the video_ai capability was RETIRED per FASE_2.1, June 2026), the
// handler MUST fail-closed with HTTP 400 BadRequest + the canonical
// ErrEngineRetired sentinel message naming both valid replacement
// engines (ken-burns + ai-image-N). NO silent fall-through to the
// ken-burns path (pre-PR godlike/07 fake-availability surface).
//
// godlike/06 SSOT: ErrEngineRetired canonical surface lives ONLY in
// internal/api/fullimages/handler.go (the canonical public fullimages
// surface per the PR-IMG-LEGACY-6-FIX closure, ship_sha 246c095f,
// 2026-07-06). This test exercises that surface via the aliased import
// `fullimagesapi.FullImagesHandler` — the canonical typed construct.
// godlike/07 typed-error contract: callers probe via
// errors.Is(err, ErrEngineRetired) — the 400 body message is the
// canonical diagnostic string.
//
// Test surface: 5 sub-cases — engine omitted (positive control),
// engine=ken-burns (positive control, no error), engine="google-vids"
// lowercase (canonical rejected), engine="Google-Vids" mixed-case
// (case-insensitive), engine=" GOOGLE-VIDS " trimmed (whitespace
// tolerance). All sub-cases hit the handler with nil service so the
// gate fires BEFORE the service dispatch — the canonical godlike/07
// fail-closed-at-composition seam.
//
// Note: the handler receives FullImagesRequest with Sections binding
// required,min=1. A request with empty sections would 400 at BindJSON
// BEFORE the engine gate; we exercise the engine gate specifically
// with a syntactically-valid 1-section request.
func TestGenerateFullImages_GoogleVidsEngine_ReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		engine   string
		want400  bool
		wantBody []string // substrings that MUST appear in 400 body
	}{
		{
			name:     "engine_omitted_positive_control",
			engine:   "", // Section.Engine omitempty; absent counts as empty
			want400:  false,
			wantBody: nil,
		},
		{
			name:     "engine_ken_burns_positive_control",
			engine:   "ken-burns",
			want400:  false,
			wantBody: nil,
		},
		{
			name:     "engine_google_vids_lowercase_canonical_reject",
			engine:   "google-vids",
			want400:  true,
			wantBody: []string{"engine=google-vids retired", "ken-burns", "ai-image-N"},
		},
		{
			name:     "engine_GoogleVids_mixed_case_reject",
			engine:   "Google-Vids",
			want400:  true,
			wantBody: []string{"engine=google-vids retired", "ken-burns", "ai-image-N"},
		},
		{
			name:     "engine_google_vids_whitespace_trimmed_reject",
			engine:   "  GOOGLE-VIDS  ",
			want400:  true,
			wantBody: []string{"engine=google-vids retired", "ken-burns", "ai-image-N"},
		},
	}

	for _, tc := range cases {
		tc := tc // capture range var
		t.Run(tc.name, func(t *testing.T) {
			h := fullimagesapi.NewFullImagesHandler(nil)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			body := `{
				"topic": "Medieval Europe",
				"language": "en",
				"sections": [
					{"title": "Section A", "text": "desc A", "engine": "` + tc.engine + `"}
				]
			}`
			c.Request = httptest.NewRequest(http.MethodPost, "/api/images/video/generate",
				strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			// ACT — call handler. For want400=false sub-cases the nil
			// service short-circuits via 503 ("fullimages service not
			// wired") — that's the EXPECTED positive-control behavior
			// (the engine gate passes, then the nil-service check at
			// handler.go returns 503). For want400=true sub-cases the
			// engine gate fires BEFORE the nil-service check, returning
			// 400. GenerateFullImages is the canonical method on the
			// canonical fullimages surface (internal/api/fullimages/handler.go).
			h.GenerateFullImages(c)

			if tc.want400 {
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("engine=%q: want 400 BadRequest, got %d (body=%q)",
						tc.engine, rec.Code, rec.Body.String())
				}
				for _, want := range tc.wantBody {
					if !strings.Contains(rec.Body.String(), want) {
						t.Errorf("engine=%q: 400 body must contain %q; got body=%q",
							tc.engine, want, rec.Body.String())
					}
				}
			} else {
				// Positive control: the engine gate passes; nil service
				// short-circuits at handler.go with HTTP 503
				// "fullimages service not wired" (canonical typed
				// nil-tolerance per godlike/07 no-fake-availability).
				// Asserting the exact 503 locks the canonical nil-service
				// contract — if a future refactor changes the nil
				// response (e.g. silent-success to 200), this test fails
				// immediately rather than silently passing on != 400.
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("engine=%q: positive control should return 503 (nil service); got %d body=%q",
						tc.engine, rec.Code, rec.Body.String())
				}
				wantSub := "fullimages service not wired"
				if !strings.Contains(rec.Body.String(), wantSub) {
					t.Errorf("engine=%q: 503 body must contain %q; got body=%q",
						tc.engine, wantSub, rec.Body.String())
				}
			}
		})
	}
}
