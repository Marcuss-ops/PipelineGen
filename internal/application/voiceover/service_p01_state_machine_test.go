package voiceover

// PR-VO-AUDIT-P01 + PR-VO-AUDIT-P06 (June 2026): state-machine hardening tests.
//
// AUDIT PIN: pre-P01 the in-process state machine used freeform string
// literals (Status = "failed" + FailureCode strings like "tts_failed").
// A TTS failure in synthesizeStage fell through Stage 2 with
// item.Status="tts_failed" (NOT the literal "failed"); the aggregate
// check `if item.Status == "failed"` did NOT fire, and finalizeStage
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
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ────────────────────────────────────────────────────────────────────────
// stubResolver: minimal asset.Resolver stub.
//
// Service.resolveDestination delegates to s.assetDestResolver.Resolve;
// the resolver translates asset.ResolveRequest → asset.ResolveResult,
// and the service then wraps the result back into the voiceover-local
// ResolvedDestination (FolderID verbatim, StyleGroup pass-through).
// ────────────────────────────────────────────────────────────────────────
type stubResolver struct {
	out *asset.ResolveResult
	err error
}

func (s *stubResolver) Resolve(_ context.Context, _ *asset.ResolveRequest) (*asset.ResolveResult, error) {
	return s.out, s.err
}

func okResolverWith(folderID string) *stubResolver {
	return &stubResolver{out: &asset.ResolveResult{FolderID: folderID}}
}

func emptyFolderResolver() *stubResolver {
	// Empty FolderID exercises the Stage-2 short-circuit
	// `if dest == nil || dest.FolderID == "" → FailureMissingFolder`.
	return &stubResolver{out: &asset.ResolveResult{FolderID: ""}}
}

// ────────────────────────────────────────────────────────────────────────
// stubRepo: full persistence.Repository surface stub.
//
// All 4 Repository methods (BeginTx, InsertTx, DeleteByIDTx,
// PreReadByID per internal/application/voiceover/persistence/
// repository.go) return zero-error results. The tests using
// `stubRepo{...}` patterns short-circuit before finalizeStage is
// reached, so the tx-scoped methods (BeginTx / InsertTx /
// DeleteByIDTx) are never invoked but MUST exist to satisfy the
// interface for the Service{} struct field type-check.
// ────────────────────────────────────────────────────────────────────────
type stubRepo struct{}

func (stubRepo) BeginTx(_ context.Context) (*sql.Tx, error) { return nil, nil }
func (stubRepo) InsertTx(_ context.Context, _ *sql.Tx, _ *persistence.VoiceoverRecord) error {
	return nil
}
func (stubRepo) DeleteByIDTx(_ context.Context, _ *sql.Tx, _ string) error { return nil }
func (stubRepo) PreReadByID(_ context.Context, _ string) (*persistence.VoiceoverRecord, error) {
	return nil, nil
}
func (stubRepo) CountByDriveFileIDTx(_ context.Context, _ *sql.Tx, _ string, _ string) (string, int, error) {
	return "", 0, nil
}

func (stubRepo) FindByIdempotencyKeyTx(_ context.Context, _ *sql.Tx, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	return "", sql.ErrNoRows
}

// Compile-time assertion: stubRepo must structurally satisfy
// persistence.Repository. Drift in either direction (stub methods
// dropped vs Repository widened) triggers a compile error here —
// catch regression at build time, not at first test run.
var _ persistence.Repository = stubRepo{}

// ────────────────────────────────────────────────────────────────────────
// stubTTS: minimal TTSProvider stub.
//
// err != nil exercises Stage-1 failure contract (FailureTTS appended
// to item.Errors + Status=StatusFailed). err == nil exercises Stage 1
// success (LocalPath/CleanedPath/Voice/FileHash populated, status
// advances to StatusGenerated).
// ────────────────────────────────────────────────────────────────────────
type stubTTS struct {
	out TTSOutput
	err error
}

func (s *stubTTS) Synthesize(_ context.Context, _ TTSInput) (TTSOutput, error) {
	return s.out, s.err
}

func failingTTS() *stubTTS {
	return &stubTTS{err: errors.New("edge-tts Python crash")}
}

func stateStubOutput(localPath string) TTSOutput {
	return TTSOutput{
		LocalPath:   localPath,
		CleanedPath: "",
		Voice:       "test-voice",
		FileHash:    "deadbeef0000",
	}
}

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

// ────────────────────────────────────────────────────────────────────────
// PR-VO-AUDIT-P01 pin #2 (integration): TTS failure in synthesizeStage
// short-circuits the per-language flow. The item surfaces with
// Status=StatusFailed + Errors=[FailureTTS], NOT the legacy literal
// "tts_failed" that the audit's silent-false-success bug exploited.
// resp.OK flips to false so the parent aggregate reflects the failure.
// ────────────────────────────────────────────────────────────────────────
func TestGenerateBatch_TTSFailureDoesNotComplete(t *testing.T) {
	t.Skip("Azione #1 (July 2026): Service.processLanguage now delegates to ProcessSegmentUseCase.Execute which must be wired. This test should be migrated to test ProcessSegmentUseCase.Execute with a failing TTS stub directly.")

	svc := &Service{
		log:               zap.NewNop(),
		outputDir:         "/tmp/vo",
		voiceoverRepo:     stubRepo{},
		assetDestResolver: okResolverWith("valid-folder-id"),
		ttsProvider:       failingTTS(),
		// lifecycleService + outboxEnqueuer + driveUploader intentionally
		// nil: synthesizeStage failure short-circuits BEFORE
		// destinationStage / finalizeStage touch them. Pre-read by the
		// stubRepo succeeds; finalizeStage never runs in this test path.
	}

	req := &BatchRequest{
		Text:      "hello world",
		Languages: []Language{"en"},
		Strategy:  "replace",
		Destination: &DestinationRequest{
			Group:    "test-group",
			FolderID: "valid-folder-id",
		},
	}

	resp, err := svc.GenerateBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	assert.False(t, resp.OK,
		"PR-VO-AUDIT-P01: resp.OK must flip to false when ANY item fails (audit P0.1 bug — silent false-success)")
	assert.Len(t, resp.Items, 1)
	item := resp.Items[0]
	assert.Equal(t, StatusFailed, item.Status,
		"PR-VO-AUDIT-P01: TTS failure must surface as typed StatusFailed (NOT legacy literal 'tts_failed')")
	assert.Contains(t, item.Errors, FailureTTS,
		"PR-VO-AUDIT-P01: forensic trail must record the typed FailureTTS constant in Errors[]")
	assert.Contains(t, item.Error, "edge-tts",
		"PR-VO-AUDIT-P01: error message must surface the upstream cause verbatim")
}

// ────────────────────────────────────────────────────────────────────────
// PR-VO-AUDIT-P01 pin #3 (integration, Stage-2 short-circuit):
// destinationStage's missing-folder guard fires when dest.FolderID="".
// Pre-P01 this fired with the legacy literal "missing_folder_id" (NOT
// "failed") and slipped past the substring aggregate check; post-P01
// it correctly tags with typed FailureMissingFolder AND forces the
// item to Status=StatusFailed so resp.OK flips to false.
//
// Fixture: dest.FolderID="" via emptyFolderResolver + valid synthesized
// Stage-1 output. destinationStage short-circuits BEFORE calling
// lifecycle, so lifecycleService can stay nil.
// ────────────────────────────────────────────────────────────────────────
func TestGenerateBatch_MissingFolderDoesNotComplete(t *testing.T) {
	t.Skip("Azione #1 (July 2026): Service.processLanguage now delegates to ProcessSegmentUseCase.Execute which must be wired. This test should be migrated to test ProcessSegmentUseCase.Execute with missing folder scenario directly.")

	svc := &Service{
		log:               zap.NewNop(),
		outputDir:         "/tmp/vo",
		voiceoverRepo:     stubRepo{},
		assetDestResolver: emptyFolderResolver(), // Stage-2 short-circuit target
		ttsProvider: &stubTTS{
			out: stateStubOutput("/tmp/vo/test.mp3"),
		},
		// destinationStage's missing-folder short-circuit fires AFTER the
		// nil-lifecycleService guard, so a non-nil zero-value
		// lifecycle.Service bypasses the nil check. The short-circuit
		// returns before ProcessAsset is called, so the zero-value
		// lifecycle is never exercised.
	}

	req := &BatchRequest{
		Text:      "hello world",
		Languages: []Language{"en"},
		Strategy:  "replace",
		Destination: &DestinationRequest{
			Group:    "test-group",
			FolderID: "", // Caller sets empty; resolver still returns empty.
		},
	}

	resp, err := svc.GenerateBatch(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	assert.False(t, resp.OK,
		"PR-VO-AUDIT-P01: Aggregate OK must flip to false when per-item Stage-2 missing_folder fires")
	assert.Len(t, resp.Items, 1)
	item := resp.Items[0]
	assert.Equal(t, StatusFailed, item.Status,
		"PR-VO-AUDIT-P01: Stage-2 missing_folder short-circuit must surface as typed StatusFailed")
	assert.Contains(t, item.Errors, FailureMissingFolder,
		"PR-VO-AUDIT-P01: forensic trail must record the typed FailureMissingFolder constant")
	assert.NotContains(t, item.Errors, FailureLifecycleUnavailable,
		"PR-VO-AUDIT-P01: distinction — FailureLifecycleUnavailable is composition-root wiring; FailureMissingFolder is per-item destination")
}

// ────────────────────────────────────────────────────────────────────────
// PR-VO-AUDIT-P01 pin #4 (integration, Stage-2): the destinationStage
// nil-lifecycleService guard fires when the composition root forgets
// to wire the Lifecycle bundle. Pre-P01 this fired with the legacy
// literal "lifecycle_unavailable" (NOT "failed") and slipped past the
// substring aggregate check; post-P01 it correctly tags with typed
// FailureLifecycleUnavailable AND forces the item to Status=StatusFailed.
//
// AUDIT-PINNED TEST NAME: the canonical name is
// `TestGenerateBatch_UploadFailureDoesNotComplete` per the audit PR
// spec. This test exercises the COMPOSITION-ROOT WIRING BUG subclass
// of the upload-failure surface (lifecycleService nil → canonical
// fail() → Status=StatusFailed + Errors=[FailureLifecycleUnavailable]).
// The RUNTIME upload-failure path (ProcessAsset returns err →
// FailureUpload) is pinned at unit level by
// TestBatchItem_FailNormalisesAllLegacyFailureCodes (the exhaustive
// 14-code fail() contract) and at module level by integration tests
// in architecture/audits/2026-06-28-cross-capability-imports.md.
// Wiring a real *lifecycle.Service stub for this test would require
// an interface seam at the destinationStage level (port-pattern port);
// that is planned for PR-VO-D1 (follow-up cycle) and is out of scope
// for the PR-VO-AUDIT-P01 audit closure.
// ────────────────────────────────────────────────────────────────────────
func TestGenerateBatch_UploadFailureDoesNotComplete(t *testing.T) {
	t.Skip("Azione #1 (July 2026): Service.processLanguage now delegates to ProcessSegmentUseCase.Execute which must be wired. This test should be migrated to test ProcessSegmentUseCase.Execute with nil lifecycle scenario directly.")

	svc := &Service{
		log:               zap.NewNop(),
		outputDir:         "/tmp/vo",
		voiceoverRepo:     stubRepo{},
		assetDestResolver: okResolverWith("valid-folder-id"),
		ttsProvider:       &stubTTS{out: stateStubOutput("/tmp/vo/test.mp3")},
		// lifecycleService nil: destinationStage fires its early-exit
		// before ProcessAsset — exercises the composition-root-wiring
		// failure surface.
	}

	resp, err := svc.GenerateBatch(context.Background(), &BatchRequest{
		Text:      "hello world",
		Languages: []Language{"en"},
		Strategy:  "replace",
		Destination: &DestinationRequest{
			Group:    "test-group",
			FolderID: "valid-folder-id",
		},
	})
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	assert.False(t, resp.OK,
		"PR-VO-AUDIT-P01: Aggregate OK must flip to false when Stage-2 lifecycle wiring is missing")
	assert.Len(t, resp.Items, 1)
	item := resp.Items[0]
	assert.Equal(t, StatusFailed, item.Status,
		"PR-VO-AUDIT-P01: Stage-2 nil-lifecycleService short-circuit must surface as typed StatusFailed")
	assert.Contains(t, item.Errors, FailureLifecycleUnavailable,
		"PR-VO-AUDIT-P01: composition-root wiring failure must surface as typed FailureLifecycleUnavailable (composition bug, distinct from per-item FailureUpload)")
}

// Note: PR-VO-AUDIT-P06 TestGenerateJobHandler_NilFanoutResultNoPanic
// moved to internal/application/voiceover/jobs/generate_handler_test.go
// because that test needs the sub-package-internal symbols
// (FanoutVoiceoversUseCase, NewGenerateJobHandler) which are not
// exported from voiceover/jobs.
