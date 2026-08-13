// Package jobs (api/jobs) — handler_get_full_test.go.
//
// PR-ERROR-SURFACING regression test (2026-07-04).
//
// Pins the canonical contract that GET /api/jobs/{id}/full surfaces the
// canonical job.Error field at TOP-LEVEL, matching parity with the
// /api/jobs LIST endpoint (which already exposes each slice element's
// `error` JSON tag via internal/kernel/job/job.go::Job.Error).
//
// Pre-PR contract violation: the gin.H literal in handler.GetFull
// omitted the `error` field at top-level. Even when the DB column
// held a 123-char typed error string (e.g. "generation: postprocess
// failed: generation: entity extractor unavailable"), operators
// polling /full saw error=None (top-level) and had to fall back to
// the nested job.error path. Post-PR contract: top-level error is
// populated, mirror contract holds (top-level == nested job.error),
// and the canonical typed sentinel `scriptpkg.ErrScriptGenerationFailed`
// surfaces verbatim.
//
// Stub pattern mirrors the canonical fakeJobService pattern from
// internal/api/assets/storage/handler_test.go (8-method job.Service
// stub: Enqueue / Get / Cancel / List / IsTerminal / RegisterHandler /
// ListEvents / Retry).
package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stubServiceForGetFull is the minimal job.Service stub that
// satisfies the 8-method signature. Mirrors internal/api/assets/storage/
// handler_test.go::fakeJobService verbatim (no Comments added); the
// difference is the Parameters on Get + ListEvents which feed the
// regression test scenarios below.
type stubServiceForGetFull struct {
	// outErr is the canonical job.Error value returned from Get().
	// Tests set this to drive the typed-sentinel / mismatch scenarios.
	outErr string

	// outStatus is the canonical job.Status value returned from Get().
	// Tests set this to drive the FAILED / SUCCEEDED / RUNNING scenarios.
	outStatus job.Status

	// outID / outType are returned from Get().
	outID   string
	outType string

	// eventsList / eventsErr feed the events slice via ListEvents().
	eventsList []job.Event
	eventsErr  error

	// outResult is the canonical job.Result payload returned from Get()
	// (surfaced verbatim as the /full `result` field).
	outResult json.RawMessage
}

func (s *stubServiceForGetFull) Enqueue(_ context.Context, _ *job.EnqueueRequest) (*job.Job, error) {
	return nil, nil
}
func (s *stubServiceForGetFull) Get(_ context.Context, _ string) (*job.Job, error) {
	return &job.Job{
		ID:     s.outID,
		Type:   s.outType,
		Status: s.outStatus,
		Error:  s.outErr,
		Result: s.outResult,
	}, nil
}
func (s *stubServiceForGetFull) Cancel(_ context.Context, _ string) error { return nil }
func (s *stubServiceForGetFull) List(_ context.Context, _ job.Filter) ([]job.Job, error) {
	return nil, nil
}
func (s *stubServiceForGetFull) IsTerminal(_ job.Status) bool          { return false }
func (s *stubServiceForGetFull) RegisterHandler(_ string, _ any) error { return nil }
func (s *stubServiceForGetFull) ListEvents(_ context.Context, _ string) ([]job.Event, error) {
	return s.eventsList, s.eventsErr
}
func (s *stubServiceForGetFull) Retry(_ context.Context, _ string) (*job.Job, error) {
	return nil, nil
}

// Compile-time pin: stub MUST satisfy job.Service to be wired
// into JobsHandler.service (typed as job.Service via the
// domain/job alias). Surfaces signature drift at build time, not
// runtime panic.
var _ job.Service = (*stubServiceForGetFull)(nil)

// pushedType is the canonical job-type discriminator for script.generate
// jobs (lives canonical SSOT at internal/domain/script/job_types.go,
// re-exported at internal/domain/job/job.go). Tests reference this
// constant directly instead of literal "script.generate" to maintain
// godlike/06 SSOT (one canonical owner per fact).
const pushedType = scriptpkg.TypeGenerate

// ── Regression tests for GetFull top-level error contract ─────────

// fullResponseEnvelope is the canonical wire-shape decoded in the
// regression tests below. Pre-PR contract (after Blocco C1-Step 13):
// gin.H literal enumerated id/type/status/progress/current_step/
// events/result/retryable/job — `error` was absent. Post-PR contract:
// adds top-level `error`.
type fullResponseEnvelope struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Status      job.Status      `json:"status"`
	Progress    int             `json:"progress"`
	Error       string          `json:"error"`
	CurrentStep job.Status      `json:"current_step"`
	Events      []job.Event     `json:"events"`
	Result      json.RawMessage `json:"result"`
	Retryable   bool            `json:"retryable"`
	Job         *job.Job        `json:"job"`
}

// runGetFull wires the canonical handler to a stub service, mounts it on
// a fresh gin router, fires an httptest request, and decodes the JSON
// response into the envelope. Returns (env, raw response bytes).
func runGetFull(t *testing.T, stub *stubServiceForGetFull) (*fullResponseEnvelope, []byte, int) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	h := NewJobsHandler(stub, nil, zap.NewNop())
	router := gin.New()
	rg := router.Group("/jobs")
	h.RegisterRoutes(rg)

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/jobs/"+stub.outID+"/full", nil)
	router.ServeHTTP(rec, req)

	env := &fullResponseEnvelope{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), env), "response should be valid JSON")
	return env, rec.Body.Bytes(), rec.Code
}

// TestGetFull_TopLevelErrorFieldPopulated is the load-bearing regression
// test. Pre-PR, the response body did NOT contain a top-level "error"
// key — even when the DB column held a 123-char error string, the
// response was empty at that path. Post-PR, this test fails loudly
// if any future change reverts the gin.H literal to omit `error`.
func TestGetFull_TopLevelErrorFieldPopulated(t *testing.T) {
	// var (not const) because scriptpkg.ErrScriptGenerationFailed.Error()
	// is a method call — not a compile-time constant. Computed once at
	// package init; the SSOT pin in TestGetFull_TypedSentinelVerbatimPresence
	// keeps the literal value in lockstep.
	var wantErr = scriptpkg.ErrScriptGenerationFailed.Error() +
		": generation: postprocess failed: generation: entity extractor unavailable"

	stub := &stubServiceForGetFull{
		outID:     "job-regression-1",
		outType:   pushedType,
		outStatus: job.StatusFailed,
		outErr:    wantErr,
	}
	env, body, code := runGetFull(t, stub)

	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, wantErr, env.Error,
		"top-level `error` MUST equal job.Error verbatim (PR-ERROR-SURFACING contract)")
	assert.Contains(t, string(body), `"error":"`+wantErr+`"`,
		"raw response body MUST serialize top-level `error` JSON")

	t.Logf("top-level error propagated: %.80s...", env.Error)
}

// TestGetFull_TypedSentinelVerbatimPresence asserts that the canonical
// typed-error sentinel reaches the response payload as a substring.
// The probe text is derived from scriptpkg.ErrScriptGenerationFailed.Error()
// so the test stays in lockstep with the canonical sentinel at
// internal/domain/script/generation_errors.go.
func TestGetFull_TypedSentinelVerbatimPresence(t *testing.T) {
	const sentinelPrefix = "generation: script generation failed" // matches ErrScriptGenerationFailed.Error()
	const phaseSentinelPrefix = "generation: postprocess failed"  // matches ErrPostprocessFailed.Error()
	const fullErr = sentinelPrefix + ": " + phaseSentinelPrefix + ": any-inner-error-message"

	stub := &stubServiceForGetFull{
		outID:     "job-regression-2",
		outType:   pushedType,
		outStatus: job.StatusFailed,
		outErr:    fullErr,
	}
	env, _, code := runGetFull(t, stub)
	require.Equal(t, http.StatusOK, code)

	// 1. Canonical umbrella-envelope sentinel prefix IS present verbatim
	//    in the top-level response (errors.Is walk prerequisite for
	//    operators/clients).
	assert.Contains(t, env.Error, sentinelPrefix,
		"top-level error MUST surface the canonical umbrella prelude (ErrScriptGenerationFailed)")

	// 2. Phase-level sentinel is ALSO present verbatim (granular walk).
	assert.Contains(t, env.Error, phaseSentinelPrefix,
		"top-level error MUST also preserve the phase-level sentinel for granular errors.Is")

	// 3. SSOT pin: scriptpkg.ErrScriptGenerationFailed string MUST NOT drift.
	assert.Equal(t, sentinelPrefix, scriptpkg.ErrScriptGenerationFailed.Error(),
		"scriptpkg.ErrScriptGenerationFailed string MUST NOT drift (SSOT lockstep)")
}

// TestGetFull_MirrorContract_TopLevelEqualsNested locks the contract
// that top-level .error and nested .job.error render the same JSON
// string — operators can paste either path into their tooling and
// get the canonical message. Pre-PR, .error was missing entirely;
// post-PR, both surfaces match.
func TestGetFull_MirrorContract_TopLevelEqualsNested(t *testing.T) {
	const wantErr = "script.generate: postprocess: typed-error-sentinel-propagation-test"

	stub := &stubServiceForGetFull{
		outID:     "job-regression-3",
		outType:   pushedType,
		outStatus: job.StatusFailed,
		outErr:    wantErr,
	}
	env, _, code := runGetFull(t, stub)
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, env.Job, "nested `job` envelope MUST remain (backward compat)")

	assert.Equal(t, wantErr, env.Error,
		"top-level .error MUST equal job.Error verbatim")
	assert.Equal(t, wantErr, env.Job.Error,
		"nested .job.error MUST equal job.Error verbatim (canonical kernel/job.Job.Error JSON tag)")

	assert.Equal(t, env.Error, env.Job.Error,
		"mirror contract: top-level .error MUST equal nested .job.error")
}

// TestGetFull_EmptyErrorOnSuccess confirms that an SUCCEEDED job with
// no error produces an empty top-level error string. The test fails if
// a future refactor accidentally defaults to "null" at the top-level
// when no error exists.
func TestGetFull_EmptyErrorOnSuccess(t *testing.T) {
	stub := &stubServiceForGetFull{
		outID:     "job-regression-4",
		outType:   pushedType,
		outStatus: job.StatusSucceeded,
		outErr:    "", // empty
	}
	env, body, code := runGetFull(t, stub)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "", env.Error, "empty job.Error MUST round-trip as empty top-level")
	assert.Contains(t, string(body), `"error":""`,
		"empty error MUST serialize as `\"error\":\"\"` (not absent — contract is field-present)")
}

// TestGetFull_ExposesTimingReferences pins the timing-bundle surface on
// the /full payload: a script.generate result whose scene binding carries
// a per-language VoiceoverTimingBinding must surface verbatim in the
// `result` field (the handler passes j.Result through untouched), while
// the word-level timing array stays in the published timing.json (never
// inlined in the binding).
func TestGetFull_ExposesTimingReferences(t *testing.T) {
	envelope := scriptpkg.GenerationEnvelopeResult{
		Version: scriptpkg.EnvelopeVersion,
		OK:      true,
		Items: []scriptpkg.GenerationEnvelopeItem{{
			ItemID: "item-1",
			Result: &scriptpkg.GenerationResult{
				Output: scriptpkg.ScriptOutput{SpecScene: scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
					ID:   "scene-1",
					Text: "Il celebre incontro di Teano",
					Bindings: scriptpkg.SceneBindings{Voiceover: &scriptpkg.VoiceoverBinding{
						Status: "completed",
						Link:   "https://drive.google.com/file/d/audio-it/view",
						Timing: map[string]scriptpkg.VoiceoverTimingBinding{
							"it": {
								Status:       "completed",
								JSONLink:     "https://drive.google.com/file/d/timing-it/view",
								SRTLink:      "https://drive.google.com/file/d/subtitles-it-srt/view",
								VTTLink:      "https://drive.google.com/file/d/subtitles-it-vtt/view",
								BoundaryMode: "word",
								WordCount:    184,
								DurationUS:   18_342_000,
								TextSHA256:   "text-en",
								AudioSHA256:  "audio-en",
							},
						},
					}},
				}}},
				},
			}}},
		Summary: scriptpkg.GenerationEnvelopeSummary{Total: 1, Succeeded: 1},
	}
	raw, err := json.Marshal(envelope)
	require.NoError(t, err)

	stub := &stubServiceForGetFull{
		outID:     "job-timing-1",
		outType:   pushedType,
		outStatus: job.StatusSucceeded,
		outResult: raw,
	}
	env, body, code := runGetFull(t, stub)
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, env.Result, "/full must surface the job result payload")
	result := string(env.Result)
	assert.Contains(t, result, `"timing"`, "the result payload must expose the per-language timing map")
	assert.Contains(t, result, `"json_link"`, "the timing bundle must expose the timing.json link")
	assert.Contains(t, result, `"vtt_link"`, "the timing bundle must expose the VTT link")
	assert.Contains(t, result, `"duration_us"`, "the timing bundle must expose the duration")
	assert.NotContains(t, result, `"words"`, "the /full payload must NOT inline the word-level timing array")
	_ = body
}

// TestGetFull_BackwardCompat_ExistingFieldsPreserved locks the
// pre-existing fields stay untouched by the contract pin. If a future
// refactor drops current_step / events / result / retryable / job,
// this test surfaces the regression.
func TestGetFull_BackwardCompat_ExistingFieldsPreserved(t *testing.T) {
	const wantErr = "regression-shape-test: error message"
	stub := &stubServiceForGetFull{
		outID:     "job-regression-5",
		outType:   pushedType,
		outStatus: job.StatusFailed,
		outErr:    wantErr,
		eventsList: []job.Event{
			{ID: "evt-1", JobID: "job-regression-5", Type: "started", Message: "begin"},
		},
	}
	env, body, code := runGetFull(t, stub)
	require.Equal(t, http.StatusOK, code)

	// Pre-existing fields preserved.
	assert.Equal(t, "job-regression-5", env.ID, "id preserved")
	assert.Equal(t, pushedType, env.Type, "type preserved")
	assert.Equal(t, job.StatusFailed, env.Status, "status preserved")
	assert.Equal(t, job.StatusFailed, env.CurrentStep,
		"current_step preserved (must mirror j.Status pre-PR contract)")
	require.Len(t, env.Events, 1, "events slice preserved")
	assert.Equal(t, "evt-1", env.Events[0].ID, "events content preserved")
	require.NotNil(t, env.Job, "embedded `job` envelope preserved")
	assert.Equal(t, "job-regression-5", env.Job.ID, "embedded job.id preserved")

	// AND: top-level error is now also present (PR-ERROR-SURFACING).
	assert.Equal(t, wantErr, env.Error, "top-level error ALSO present (PR-ERROR-SURFACING)")
	assert.Contains(t, string(body), "regression-shape-test",
		"raw body MUST contain the canonical regression marker for reader-skim verification")
}
