// Package scriptgeneration — slim runner_test.go.
//
// Fase 5 of the Largest-Files plan split the original monolithic
// runner_test.go (1229 LOC, 21 top-level test functions covering
// happy path + retry + stage-skip + helper-units + lifecycle +
// service) into a per-scenario sibling-file set. This file remains
// only as an index-of-tests entry-point so a developer can grep
// for a scenario keyword and find the corresponding test file.
//
// Per-scenario test files (each same-package `scriptgeneration`):
//
//   - runner_happy_path_test.go    ← TestRunner_HappyPath_AllStagesComplete
//   - runner_retry_test.go         ← 2 retry-from-checkpoint scenarios
//     (TextGenerator fails, Translator fails at scene N)
//   - runner_stage_skip_test.go    ← 3 stage-skip scenarios
//     (VoiceoverGenerator nil, Docs disabled, Docs enabled)
//   - runner_unit_test.go          ← TestDeriveErrorCode +
//     TestBuildDocumentContent + TestContainsAny
//   - runner_lifecycle_test.go     ← IsRunCompletable + ResumeFrom +
//     StageIndex + StageIsTerminal + RetryDelay + ShouldRetry +
//     ResolveDocsConfig (7 lifecycle tests)
//   - runner_service_test.go       ← ServiceStart_Validation +
//     NewService_PanicsOnNilRequiredPorts +
//     NewRunner_PanicsOnNilRepo + InMemRepo_GetByJobID
//
// Shared test fixture (stubs + in-memory repo + factory helpers):
//
//   - runner_helpers_test.go       ← stub{TextGenerator,Translator,
//     VoiceoverGenerator,DocumentPublisher} +
//     inMemRunRepository + newStub...() factories +
//     defaultTestRequest + defaultTestScenes + newTestRunner +
//     awaitCompletion
//
// Independence from Fase 4 precedent: Fase 4 split lived in the
// `internal/platform/sqlite` package; this split lives
// in `internal/scriptgeneration`. The `migrations_092_093_test.go`
// + `migrations_helpers_test.go` precedent illustrates the
// "shared fixture + per-scenario test files" pattern; this
// runner.go split mirrors it with package-local helpers.
//
// godlike/06 SSOT invariants covered by the suite (behavioral
// matrix — every invariant is asserted verbatim by at least
// one per-scenario test above):
//
//   - RunStatus + CurrentStage + FailedStage propagation
//   - Stage-by-stage checkpoint + retry from failed stage
//   - Scene-level translation idempotency
//   - Stage-skip for nil ports and disabled feature flags
//   - Document upsert identity (run_id + language) and idempotency
//   - Stable error codes (PROVIDER_TIMEOUT, PROVIDER_UNAVAILABLE,
//     EMPTY_RESULT, TEXT_GENERATION_FAILED, TRANSLATION_FAILED,
//     VOICEOVER_FAILED, DOCUMENT_FAILED, ENQUEUE_FAILED)
//   - RetryDelay exponential capped at 120s
//   - Service.Start ingress validation gates
//   - NewService / NewRunner nil-port panic contract
//
// The slim file intentionally does NOT import `testing` —
// `go test ./internal/scriptgeneration/...` discovers the 21
// top-level TestXxx functions from the sibling files, not from
// this anchor file. Any new top-level test here MUST live in one
// of the per-scenario files above (or in a new sibling) — not
// in this anchor.
package scriptgeneration

// Intentionally empty. All 21 prior test bodies now live across:
//   - runner_happy_path_test.go   (1 test)
//   - runner_retry_test.go        (2 tests)
//   - runner_stage_skip_test.go   (3 tests)
//   - runner_unit_test.go         (3 tests)
//   - runner_lifecycle_test.go    (7 tests)
//   - runner_service_test.go      (4 tests)
//   - runner_helpers_test.go      (shared fixture: 5 stubs +
//     1 in-memory repo + 4 factories)
