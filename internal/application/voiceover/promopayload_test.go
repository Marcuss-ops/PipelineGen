// PR-VO-A6 payload round-trip test. The PromoRequestPayloadMap
// function must serialise AllowUntranslated so async /promo jobs
// (handler enqueues a voiceover.promo job; worker re-runs Generator
// via job_handler.go) preserve the original strict / lenient intent
// across the job boundary.
package voiceover

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
)

// TestPromoRequestPayloadMap_AllowUntranslated_Propagates pins the
// "lenient mode survives async" contract. The worker side rebuilds a
// promo.Request from the JSON payload map; if the map drops
// allow_untranslated, the lenient intent is lost — a caller that
// explicitly opted in to skip-on-fail would silently flip to strict
// mode at the worker boundary. Fail-closed default (Request{
// AllowUntranslated: false }) means omitempty is OK for the false
// branch — but true MUST round-trip.
func TestPromoRequestPayloadMap_AllowUntranslated_Propagates(t *testing.T) {
	cases := []struct {
		name string
		req  *promo.Request
		want map[string]any
	}{
		{
			name: "strict-default",
			req: &promo.Request{
				Text: "Hello",
			},
			want: map[string]any{
				"text":    "Hello",
				"dry_run": false,
			},
		},
		{
			name: "explicit-allow-untranslated",
			req: &promo.Request{
				Text:              "Hello",
				AllowUntranslated: true,
			},
			want: map[string]any{
				"text":               "Hello",
				"dry_run":            false,
				"allow_untranslated": true,
			},
		},
		{
			name: "all-fields-with-lenient",
			req: &promo.Request{
				Text:              "Hello",
				DriveFolderID:     "folder-id",
				DryRun:            true,
				Languages:         []string{"en-US", "es-ES"},
				AllowUntranslated: true,
			},
			want: map[string]any{
				"text":               "Hello",
				"drive_folder_id":    "folder-id",
				"dry_run":            true,
				"languages":          []string{"en-US", "es-ES"},
				"allow_untranslated": true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := PromoRequestPayloadMap(tc.req)
			// Strict-default: AllowUntranslated MUST NOT appear (omitempty).
			if tc.req.AllowUntranslated {
				assert.Equal(t, true, payload["allow_untranslated"], "allow_untranslated must round-trip when true")
			} else {
				_, present := payload["allow_untranslated"]
				assert.False(t, present, "allow_untranslated must NOT appear under strict default (omitempty)")
			}
			for k, v := range tc.want {
				assert.Equal(t, v, payload[k], "payload[%q]", k)
			}
		})
	}
}
