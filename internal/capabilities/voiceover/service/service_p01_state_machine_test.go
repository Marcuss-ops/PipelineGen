package voiceover

// PR-VO-AUDIT-P01 + PR-VO-AUDIT-P06 (June 2026): state-machine hardening tests.
//
// AUDIT PIN: pre-P01 the in-process state machine used freeform string
// literals (Status = "failed" + FailureCode strings like "tts_failed").
// A TTS failure in the former batch state machine fell through Stage 2
// with item.Status="tts_failed" (NOT the literal "failed"); the aggregate
// check `if item.Status == "failed"` did NOT fire, and finalization
// would commit the row with Status="completed". Silent false-success
// was the canonical audit P0.1 bug.
//
// After the refactor: every failure code flows through BatchItem.fail()
// which ALWAYS normalises to typed Status=StatusFailed, regardless of
// the specific code; the structured forensic trail is preserved via
// item.Errors[] (typed []FailureCode, omitempty so happy-path JSON
// stays compact). The aggregate check `item.Status == StatusFailed`
// is exhaustive at compile time — no substring matching, no legacy
// literal gap.
//
// PR-VO-AUDIT-P06 PARENT-HANDLER NIL-GUARD: FanoutVoiceoversUseCase.Execute
// can legitimately return (nil, err) on cmd==nil, cmd.Validate() failure,
// or panic-recovered paths. Pre-P06 the parent's HandleJob would panic
// on `res.EnqueuedCount` access. Post-P06 the access is nil-guarded
// via local var extraction; toFanoutResultMap is also nil-safe.
//
// ────────────────────────────────────────────────────────────────────────
// WIRE-SHAPE NOTE (deliberate, audit-pinned):
//
//	Pre-P01:  {"status": "tts_failed"}                  (legacy literal)
//	Post-P01: {"status": "failed", "errors": ["tts_failed"]}
//
// HTTP API consumers reading the per-item .status field will see
// "failed" (always-typed-typed) instead of the specific legacy code;
// they MUST inspect .errors[] for the forensic trail. This is BY-the-
// audit (force-typed normalisation). CHANGELOG entry is added in a
// follow-up housekeeping PR; commit message body carries the same note
// for canonical wave-tracker auditability (architecture/current.yaml
// linked_issues P0.1 entry).

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ────────────────────────────────────────────────────────────────────────
// PR-VO-AUDIT-P01 pin #1 (unit invariant): fail() normalises every
// legacy failure code (typed constant) to Status=StatusFailed + appends
// the FailureCode to Errors[]. Pre-P01 the legacy literal Status
// "tts_failed" slipped past the substring aggregate check; post-P01
// fail() forces the typed-typed normalisation regardless of the code.
// ────────────────────────────────────────────────────────────────────────
func TestBatchItem_FailNormalisesAllLegacyFailureCodes(t *testing.T) {
	cases := []struct {
		name     string
		failCode FailureCode
		errMsg   string
	}{
		{"TTS", FailureTTS, "edge-tts bridge failed"},
		{"TTSProviderUnavailable", FailureTTSProviderUnavailable, "ttsProvider nil"},
		{"Upload", FailureUpload, "lifecycle: drive rejected"},
		{"LifecycleUnavailable", FailureLifecycleUnavailable, "lifecycleService nil"},
		{"MissingFolder", FailureMissingFolder, "dest.FolderID empty"},
		{"NoLocalPayload", FailureNoLocalPayload, "Stage 1 produced no local file"},
		{"DBUnavailable", FailureDBUnavailable, "voiceoverRepo nil"},
		{"TxBegin", FailureTxBegin, "BeginTx: locked"},
		{"DBDelete", FailureDBDelete, "DeleteByIDTx: schema mismatch"},
		{"DBInsert", FailureDBInsert, "InsertTx: row constraint"},
		{"OutboxEnqueue", FailureOutboxEnqueue, "EnqueueIndexEvent: outbox nil"},
		{"TxCommit", FailureTxCommit, "Commit: WAL write failed"},
		{"InvalidSubfolder", FailureInvalidSubfolder, "path traversal: ../etc"},
		{"InvalidFilename", FailureInvalidFilename, "SanitizeFilename: reserved name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := BatchItem{ID: "test-item", Language: "en", Status: StatusProcessing}
			result := item.fail(tc.failCode, errors.New(tc.errMsg))

			assert.Equal(t, StatusFailed, result.Status,
				"PR-VO-AUDIT-P01: fail() must normalise ANY %q code to typed StatusFailed", tc.failCode)
			assert.Equal(t, []FailureCode{tc.failCode}, result.Errors,
				"PR-VO-AUDIT-P01: fail() must append the typed FailureCode to Errors[] for forensic trail")
			assert.Equal(t, tc.errMsg, result.Error,
				"PR-VO-AUDIT-P01: fail() must surface the err.Error() message verbatim")
		})
	}
}

// Note: PR-VO-AUDIT-P06 TestGenerateJobHandler_NilFanoutResultNoPanic
// moved to internal/application/voiceover/jobs/generate_handler_test.go
// because that test needs the sub-package-internal symbols
// (FanoutVoiceoversUseCase, NewGenerateJobHandler) which are not
// exported from voiceover/jobs.
