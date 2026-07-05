// Package script -- handler_idempotency_test.go pins Issue 5 / P1's
// handler-side Idempotency-Key contract.
//
// What this test exercises:
//
//  1. POST /api/script/generate with a GenerationEnvelopeV2 body AND
//     an Idempotency-Key HTTP header.
//  2. The handler reads the header (Stripe / AWS-SQS convention),
//     whitespace-trims it, and stamps it onto the
//     GenerateEnqueueRequest.ActiveKey field (which is already wired
//     through to broker via EnqueueGenerationJob).
//  3. fakeJobsService (defined in handler_test.go in the same
//     package) captures the resulting *job.EnqueueRequest; the test
//     asserts capturedActiveKey equals the original header value.
//
// Non-trivial interior: handler_generate.go used to drop the header.
// The test is the regression guard for the Issue 5 fix.
//
// Header-wins precedence is the canonical rule: even if a future PR
// adds an idempotency_key JSON body field, the header must take
// priority. This test pins the header-only path today; a sibling
// body-field test can land in lockstep when / if the body field
// ships.
package script

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureActiveKeyFromHandler runs against an in-process httptest server
// the same way the Issue-1 canned-job handler test routes the smoke
// path. Returns the captured EnqueueRequest's ActiveKey verbatim so
// the caller can assert on the real routing outcome (instead of
// mocking the helper).
func captureActiveKeyFromHandler(t *testing.T, idempotencyKey string) string {
	t.Helper()

	parentSvc, fake := newTestJobsService(t)
	handler := NewScriptFlowHandler(newMinimalScriptFlowDepsForTest(parentSvc))
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	server := httptest.NewServer(router)
	defer server.Close()

	// Minimal valid envelope body -- the handler runs env.Validate(),
	// which accepts any non-empty Title + Source.Text-shaped item.
	body := map[string]any{
		"version": 2,
		"preset":  "custom",
		"items": []map[string]any{
			{
				"id":    "idempotency-test",
				"title": "Idempotency Test Item",
				"source": map[string]any{
					"type":        "text",
					"topic":       "idempotency",
					"source_text": "idempotency fixture",
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err, "test envelope must marshal cleanly")

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/script/generate", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The handler returns 200 with { ok, job_id, status, status_url };
	// validation/auth failures would return 4xx with { ok:false, error }.
	// Both are valid here -- the load-bearing invariant is that the
	// fake's captured EnqueueRequest reflects whatever the handler
	// passed through.
	if resp.StatusCode != http.StatusOK {
		// bodies other than 200 may still have flowed through the
		// helper if the body validated; assert on the captured
		// request regardless and let the test consumer decide.
		t.Logf("unexpected status %d; checking captured request anyway", resp.StatusCode)
	}

	require.NotNil(t, fake.lastReq,
		"handler must have called jobsSvc.Enqueue at least once; got nil lastReq")
	return fake.lastReq.ActiveKey
}

// TestHandler_AcceptsIdempotencyKey is the canonical Issue 5 / P1
// handler-side contract pin. Footer contract:
//
//   - handler reads header `Idempotency-Key`;
//   - whitespace-trims it;
//   - sets it on GenerateEnqueueRequest.ActiveKey;
//   - EnqueueGenerationJob forwards it into the broker's
//     EnqueueRequest.ActiveKey.
//
// The test asserts against fakeJobsService.lastReq.ActiveKey after
// the helper round-trip -- the value the broker would receive --
// so the contract is pinned at the closest observable seam.
func TestHandler_AcceptsIdempotencyKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string // header value as the client sent it
		want string // expected ActiveKey the broker sees
		setH bool   // whether to send the Idempotency-Key header at all
	}{
		{
			name: "header canonical value",
			raw:  "test-key-12345",
			want: "test-key-12345",
			setH: true,
		},
		{
			name: "header whitespace trimmed",
			raw:  "  test-key-with-padding  ",
			want: "test-key-with-padding",
			setH: true,
		},
		{
			name: "absent header leaves ActiveKey empty",
			raw:  "",
			want: "",
			setH: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var header string
			if tc.setH {
				header = tc.raw
			}

			got := captureActiveKeyFromHandler(t, header)
			assert.Equal(t, tc.want, got,
				"Issue 5 / P1: handler must map Idempotency-Key header to broker EnqueueRequest.ActiveKey (verbatim after trim)")
		})
	}
}
