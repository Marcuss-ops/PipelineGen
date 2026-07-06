package fullimages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

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
// this package (the public fullimages surface). godlike/07
// typed-error contract: callers probe via errors.Is(err, ErrEngineRetired)
// — the 400 body message is the canonical diagnostic string.
//
// PR-IMG-LEGACY-6 (IMAGES-LEGACY-CLEANUP-2026-07-06 wave, CUTOVER phase,
// 2026-07-06, deadline 2026-08-22): this test was MOVED from
// internal/api/images/capability_test.go to its canonical dedicated
// package. The 5 sub-cases are preserved byte-stable; the package
// change is the only delta.
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
			h := &FullImagesHandler{service: nil}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			body := `{
				"topic": "Medieval Europe",
				"language": "en",
				"sections": [
					{"title": "Section A", "text": "desc A", "engine": "` + tc.engine + `"}
				]
			}`
			c.Request = httptest.NewRequest(http.MethodPost, "/api/fullimages/video/generate",
				strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			// ACT — call handler. For want400=false sub-cases the nil
			// service short-circuits via 503 (fullimages.Service not
			// wired) — that's the EXPECTED positive-control behavior
			// (the engine gate passes, then the service fails or returns
			// a generation error). For want400=true sub-cases the engine
			// gate fires BEFORE the service dispatch, returning 400.
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
				// Positive control: the gate passes; downstream behavior
				// depends on the service (nil → 503 per the new handler).
				// Either non-400 status confirms the engine gate did NOT fire.
				if rec.Code == http.StatusBadRequest {
					t.Fatalf("engine=%q: positive control should NOT trigger the engine gate (got 400); body=%q",
						tc.engine, rec.Body.String())
				}
			}
		})
	}
}
