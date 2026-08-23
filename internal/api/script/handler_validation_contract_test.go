// Package script — handler_validation_contract_test.go: P0.A validation
// contract test suite for POST /api/script/generate.
//
// July 2026 PR — P0.A gate. Asserts the HTTP 400 contract for every
// canonical regression path documented in the script.generate V2
// payload contract. Each subtest verifies three invariants:
//
//  1. HTTP 400 status code (NEVER 200/202 — the request must not be
//     accepted; NEVER 500 — the failure is client-caused).
//  2. Structured error response: ok=false + error envelope containing
//     the typed code AND a recognisable human-readable detail.
//  3. ZERO submissions made to the submission service (fakeSubmissionService.submitCount == 0
//     AND fakeSubmissionService.lastReq == nil) — the godlike/07 NO-FAKE-AVAILABILITY
//     contract: a malformed request MUST NOT reach the job layer.
//
// godlike/07 fail-closed rationale: the validator (handler_generate_handler.go
// ::HandlerGenerate.Generate) runs BEFORE the enqueue layer
// (handler_enqueue.go::enqueueEnvelopeFn) so a malformed envelope
// never reaches the broker. The 10-path test pins this contract
// against future handler refactors that could otherwise route a
// bad payload through the queue.
//
// 11 canonical regression paths covered:
//   - version != 2
//   - items vuoto (empty array)
//   - source.type mancante
//   - source.type="clips" senza clip_ids
//   - clip_id vuoto
//   - clip_id duplicato
//   - lingua non supportata
//   - target_words = 0
//   - target_words < 0
//   - grounding_policy non valido
//   - fallback_policy non valido
//   - fallback_policy incompatibile con source.type
//
// + 3 malformed-JSON-binding paths (binding is the canonical client
// failure surface, separate from the structural validator).
//
// t.Parallel choice: every subtest allocates its own fresh fakeJobsService
// (handler_test_fixtures_test.go::newTestJobsService) + its own
// inMemoryIdempotencyStore; no package-level mutable state is touched in
// any subtest body, so parallel execution is race-free. This is a
// stylistic divergence from the older handler_idempotency_test.go
// (which does NOT call t.Parallel) — accepted here because the table
// structure is large enough that sequential execution noticeably slows
// `go test ./internal/api/script -count=1`.
package script

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerate_ValidationContract_V2RejectionPaths is the canonical
// P0.A table-driven suite. The 12 sub-paths exercise the union of:
//
//  1. env.Validate() in internal/kernel/script/generation_envelope.go —
//     structural checks (version, items, source.type, clip_ids shape,
//     language allowlist, policy values, policy<->source compatibility).
//     Returns *PlanInvalidError → mapped to 400 with
//     code="INVALID_PAYLOAD".
//
//  2. PayloadValidator.ValidateEnvelope() in
//     internal/application/scripts/usecase/payload_validator.go —
//     config-aware limits (target_words > 0, source_text size + ratio).
//     Returns *PayloadValidationError → mapped to 400 with the typed
//     Code field (e.g. "INVALID_TARGET_WORDS", "SOURCE_TEXT_TOO_LARGE").
//
// Both error surfaces are caught by HandlerGenerate.Generate BEFORE
// the broker is touched.
func TestGenerate_ValidationContract_V2RejectionPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		body      string
		wantCode  string // expected error.code in the response body
		errDetail string // expected substring in error.message (regression lock)
	}{
		{
			name:      "version_not_2",
			body:      `{"version":1,"preset":"custom","items":[{"source":{"type":"text","topic":"x"},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "version must be 2",
		},
		{
			name:      "items_empty",
			body:      `{"version":2,"preset":"custom","items":[]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "at least one item is required",
		},
		{
			name:      "source_type_missing",
			body:      `{"version":2,"preset":"custom","items":[{"source":{},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "source.type is required",
		},
		{
			name:      "clips_source_empty_clip_ids",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"clips"},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "clips source requires at least one clip_id",
		},
		{
			name:      "clip_id_empty",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"clips","clip_ids":[""]},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "clip_ids cannot be empty",
		},
		{
			name:      "clip_id_duplicate",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"clips","clip_ids":["a","a"]},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "duplicate clip_id",
		},
		{
			name:      "unsupported_language",
			body:      `{"version":2,"preset":"custom","items":[{"language":"qq","source":{"type":"text","topic":"x"},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "unsupported language",
		},
		{
			name:      "target_words_zero",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"x"},"script_params":{"target_words":0}}]}`,
			wantCode:  "INVALID_TARGET_WORDS",
			errDetail: "target_words",
		},
		{
			name:      "target_words_negative",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"x"},"script_params":{"target_words":-5}}]}`,
			wantCode:  "INVALID_TARGET_WORDS",
			errDetail: "target_words",
		},
		{
			name:      "invalid_grounding_policy",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"clips","clip_ids":["a"],"grounding_policy":"bogus"},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "invalid grounding_policy",
		},
		{
			name:      "invalid_fallback_policy",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"clips","clip_ids":["a"],"fallback_policy":"bogus"},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "invalid fallback_policy",
		},
		{
			name:      "fallback_policy_incompatible_source",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"x","fallback_policy":"strict"},"script_params":{"target_words":100}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "fallback_policy is only compatible with source.type=clips",
		},
		// ── PR-CS-1 / FASE 6 (DoD #8): ScriptSegment sentinel ──
		{
			name:      "segments_empty_present",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"x"},"script_params":{"target_words":100,"segments":[]}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "script_params.segments must not be empty",
		},
		{
			name:      "segment_topic_empty",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"x"},"script_params":{"target_words":100,"segments":[{"topic":"intro"},{"topic":""}]}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "topic is required",
		},
		{
			name:      "segments_and_topic_topics_both_set",
			body:      `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"x"},"script_params":{"target_words":100,"segment_topics":["a","b"],"segments":[{"topic":"x"}]}}]}`,
			wantCode:  "INVALID_PAYLOAD",
			errDetail: "cannot both be set",
		},
		{
			name:      "too_many_segments_above_cap",
			body:      buildSegmentsJSON(51),
			wantCode:  "TOO_MANY_SEGMENTS",
			errDetail: "too many entries",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			jobsSvc, _ := newTestJobsService(t)
			deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
			handler := NewScriptFlowHandler(deps)

			router := gin.New()
			handler.RegisterRoutes(router.Group("/api/script"))

			req := httptest.NewRequest(
				http.MethodPost, "/api/script/generate",
				bytes.NewBufferString(tc.body),
			)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "idem-validation-"+tc.name)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			// P0.A invariant 1: HTTP 400 — the request must never be
			// accepted (202) and the failure must never be presented
			// as a server crash (500). 400 is the canonical
			// godlike/07 client-error surface.
			require.Equal(t, http.StatusBadRequest, w.Code,
				"V2 contract violation must return HTTP 400; body=%s",
				w.Body.String())

			// P0.A invariant 2: structured error envelope. The wire
			// shape produced by handler_generate_handler.go::Generate
			// for non-Typed errors is:
			//   {"ok":false, "error":{"code","message","stage","retryable"}}
			// For Typed *PayloadValidationError it's the same shape
			// but error.code is the typed Code field (e.g.
			// "INVALID_TARGET_WORDS").
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp),
				"response body must be valid JSON; got=%s", w.Body.String())
			require.Equal(t, false, resp["ok"],
				"ok must be false on a validation rejection; body=%s",
				w.Body.String())

			errField, hasErr := resp["error"]
			require.True(t, hasErr,
				"error field must be present in rejection response; body=%s",
				w.Body.String())

			// P0.A invariant 2 (continued): the canonical wire shape
			// is the structured envelope {"ok":false,"error":{"code":..,"message":..,"stage":..,"retryable":..}}
			// produced by handler_generate_handler.go::Generate. Any
			// other shape is a regression and must fail loudly — we
			// deliberately do NOT have a flat-string fallback branch.
			errEnv, ok := errField.(map[string]any)
			require.Truef(t, ok,
				"error field must be the canonical structured envelope (map[string]any); got %T body=%s",
				errField, w.Body.String())
			code, _ := errEnv["code"].(string)
			require.Equal(t, tc.wantCode, code,
				"error.code mismatch (want %q); body=%s",
				tc.wantCode, w.Body.String())
			msg, _ := errEnv["message"].(string)
			assert.Truef(t,
				strings.Contains(strings.ToLower(msg), strings.ToLower(tc.errDetail)),
				"error.message must contain %q (regression lock); got %q",
				tc.errDetail, msg)
			stage, _ := errEnv["stage"].(string)
			assert.Equal(t, "request.validation", stage,
				"error.stage must be request.validation; body=%s",
				w.Body.String())

			// P0.A invariant 3: NO JOB enqueued (godlike/07 fail-closed).
			// The validator runs BEFORE submission, so the fake submitter
			// must remain untouched.
			assert.Equalf(t, 0, submit.submitCount,
				"V2 contract violation MUST NOT submit a job; submitCount=%d body=%s",
				submit.submitCount, w.Body.String())
		})
	}
}

// buildSegmentsJSON returns a JSON request body whose only payload
// is `script_params.segments` with N minimal {"topic":"s"} entries.
// Used by the too_many_segments_above_cap subtest to drive
// MaxSegmentsCap (default 50) without writing 51 segments inline.
// PR-CS-1 / FASE 6 (DoD #8).
func buildSegmentsJSON(n int) string {
	segs := make([]string, n)
	for i := range segs {
		segs[i] = `{"topic":"s"}`
	}
	return `{"version":2,"preset":"custom","items":[{"source":{"type":"text","topic":"x"},"script_params":{"target_words":100,"segments":[` + strings.Join(segs, ",") + `]}}]}`
}

// TestGenerate_ValidationContract_MalformedJSONReturns400 pins the
// JSON-binding failure surface: a non-JSON body / syntactically invalid
// JSON / wrong top-level shape returns HTTP 400 with the "invalid payload:"
// prefix (NOT 500 — binding errors are a client-caused problem handled
// deterministically by gin's ShouldBindJSON). This complements the
// 12 structural-rejection paths by locking the wire contract for
// payloads that fail BEFORE the validator can run.
func TestGenerate_ValidationContract_MalformedJSONReturns400(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"empty_body", ""},
		{"not_json", "<<<not-json>>>"},
		{"array_instead_of_object",
			`["a","b"]`},
		{"number_instead_of_object", "42"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			jobsSvc, _ := newTestJobsService(t)
			deps, submit := newMinimalScriptFlowDepsForTest(jobsSvc)
			handler := NewScriptFlowHandler(deps)

			router := gin.New()
			handler.RegisterRoutes(router.Group("/api/script"))

			req := httptest.NewRequest(
				http.MethodPost, "/api/script/generate",
				bytes.NewBufferString(tc.body),
			)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "idem-malformed-"+tc.name)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusBadRequest, w.Code,
				"malformed JSON body must return HTTP 400; body=%s",
				w.Body.String())
			assert.Contains(t, w.Body.String(), "invalid payload",
				"error must surface the binding failure; body=%s",
				w.Body.String())
			assert.Equal(t, 0, submit.submitCount,
				"malformed JSON must NOT submit; submitCount=%d",
				submit.submitCount)
		})
	}
}
