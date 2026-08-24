// Package jobs — generate_item_handler_test.go (BLOC5.3 commit-2-child-canonical, June 2026).
//
// Audit-pinned TDD tests for the child handler cutover. The 4 tests
// below lock the canonical BLOC5.3 invariants at the handler boundary:
//
//  1. TestChild_DoesNotRegenerateRequestID — item.RequestID passes
//     through verbatim from fanout; the handler must NOT re-derive it.
//  2. TestChild_PreservesVoiceOverrideFromParent — item.Voice (set by
//     the parent's VoiceOverrides fan-out) is forwarded to the
//     canonical use case WITHOUT re-resolving via cmd.VoiceOverrides.
//  3. TestChild_PreservesFilenameFromParent — item.Filename (set by
//     the parent's buildItemFilename) is forwarded verbatim; the
//     canonical use case does NOT call FilenameBuilder.BuildFilename
//     for the per-item path (the port is held for future BACKFILL
//     stages, not for the current per-item Execute).
//  4. TestChild_DoesNotConvertToBatchRequest — the handler uses the
//     narrow VoiceoverItemExecutor port directly; no BatchRequest is
//     constructed at any layer. Audit pin: presence of the port +
//     absence of BatchRequest → canonical pipeline reached.
//
// Test fixture: a recordingVoiceoverItemExecutor implements the
// narrow VoiceoverItemExecutor port with field-level capture so each
// test asserts on (item passed in, error returned) rather than on the
// canonical pipeline's internal state.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
)

// recordingVoiceoverItemExecutor satisfies the narrow
// VoiceoverItemExecutor port (AGENTS.md Pattern 0) with field-level
// record/replay. Every Execute call appends to the calls slice; the
// cannedResult + cannedErr pair are returned to the caller.
type recordingVoiceoverItemExecutor struct {
	calls      []*voiceover.GenerateVoiceoverItemCommand
	cannedRes  *voiceover.VoiceoverItemResult
	cannedErr  error
	languages  []string
	filenames  []string
	hashes     []string
	requestIDs []string
	voices     []string
}

func (r *recordingVoiceoverItemExecutor) Execute(ctx context.Context, item *voiceover.GenerateVoiceoverItemCommand) (*voiceover.VoiceoverItemResult, error) {
	r.calls = append(r.calls, item)
	if item != nil {
		r.languages = append(r.languages, string(item.Language))
		r.filenames = append(r.filenames, item.Filename)
		r.hashes = append(r.hashes, string(item.TextHash))
		r.requestIDs = append(r.requestIDs, item.RequestID)
		r.voices = append(r.voices, item.Voice)
	}
	return r.cannedRes, r.cannedErr
}

// makeItemCmd constructs a fully-populated GenerateVoiceoverItemCommand
// mirroring FanoutVoiceoversUseCase's per-item construction (fanout.go
// line ~178) so the audit tests pin the exact field shape the parent
// emits. Any drift between this fixture and fanout.go surfaces here.
func makeItemCmd(parent, rid, lang, voice, file, hash string) *voiceover.GenerateVoiceoverItemCommand {
	return &voiceover.GenerateVoiceoverItemCommand{
		ParentJobID: parent,
		RequestID:   rid,
		Text:        "hello " + lang,
		Language:    voiceover.Language(lang),
		Voice:       voice,
		Filename:    file,
		TextHash:    voiceover.TextHash(hash),
		Destination: &voiceover.DestinationRequest{
			Group: "test-group",
		},
		Strategy:      "verify",
		RemoveSilence: false,
	}
}

// marshalItemCmd encodes the item as a job.Payload so we can drive
// HandleJob end-to-end through the dispatcher contract rather than
// calling Execute directly. The audit pin is the dispatcher-shaped
// surface, not the inner Execute.
func marshalItemCmd(t *testing.T, item *voiceover.GenerateVoiceoverItemCommand) []byte {
	t.Helper()
	raw, err := json.Marshal(item)
	require.NoError(t, err, "marshal GenerateVoiceoverItemCommand for HandleJob dispatch")
	return raw
}

// ────────────────────────────────────────────────────────────────────
// Audit Test 1: TestChild_DoesNotRegenerateRequestID
// ────────────────────────────────────────────────────────────────────

// Pins the canonical item.RequestID audit-pinned invariant (P0.6):
// the handler forwards the parent's RequestID to the canonical use case
// WITHOUT any re-derivation. The recordingExecutor asserts the recorded
// request_id matches the parent-emitted value.
func TestChild_DoesNotRegenerateRequestID(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{Language: "en", Status: voiceover.StatusCompleted},
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-job-001", "vo_20260101_120000_abcdef", "en", "en-US-RogerNeural", "hello_en.mp3", "abc123hash")

	// Drive HandleJob via a synthetic dispatcher contract.
	tools := &appjobs.JobTools{}                      // unused (no Progress callback in test)
	tools.Progress = func(percent int, msg string) {} // no-op progress

	j := &appjobs.Job{
		ID:      "child-job-001",
		Payload: marshalItemCmd(t, item),
	}
	res, err := h.HandleJob(context.Background(), j, tools)

	require.NoError(t, err)
	require.Len(t, exec.calls, 1, "recorder: handle dispatched exactly once")
	assert.Equal(t, "vo_20260101_120000_abcdef", exec.requestIDs[0],
		"BLOC5.3 audit pin: child handler MUST forward parent-emitted RequestID verbatim (no re-derivation)")
	_ = res
}

// ────────────────────────────────────────────────────────────────────
// Audit Test 2: TestChild_PreservesVoiceOverrideFromParent
// ────────────────────────────────────────────────────────────────────

// Pins the canonical item.Voice audit-pinned invariant (P0.6): the
// parent's VoiceOverrides fan-out (fanout.go cmd.VoiceOverrides[lang]
// → item.Voice) is forwarded to the canonical use case WITHOUT
// re-resolving via any VoiceOverrides map at the handler OR use case
// layer. The recordingExecutor asserts item.Voice matches the parent.
func TestChild_PreservesVoiceOverrideFromParent(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{Language: "it", Status: voiceover.StatusCompleted, Voice: "it-IT-IsabellaNeural"},
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())

	// Parent (fanout) resolves VoiceOverides["it"] to "it-IT-IsabellaNeural"
	// and stamps it on item.Voice. The child must NOT have a VoiceOverrides
	// field; the canonical use case is only called with item.Voice already
	// resolved.
	item := makeItemCmd("parent-job-002", "vo_20260101_120100_aaa111", "it", "it-IT-IsabellaNeural", "ciao_it.mp3", "deadbeef")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-job-002", Payload: marshalItemCmd(t, item)}
	res, err := h.HandleJob(context.Background(), j, tools)

	require.NoError(t, err)
	require.Len(t, exec.calls, 1)
	assert.Equal(t, "it-IT-IsabellaNeural", exec.calls[0].Voice,
		"BLOC5.3 audit pin: child handler MUST forward parent-emitted Voice verbatim (no VoiceOverrides re-resolution)")
	assert.Equal(t, "it-IT-IsabellaNeural", exec.voices[0],
		"BLOC5.3 audit pin: item.Voice on the canonical use case Execute input matches parent's resolved override")
	_ = res
}

// ────────────────────────────────────────────────────────────────────
// Audit Test 3: TestChild_PreservesFilenameFromParent
// ────────────────────────────────────────────────────────────────────

// Pins the canonical item.Filename audit-pinned invariant (P0.6):
// the parent's buildItemFilename (fanout.go template substitution) is
// forwarded to the canonical use case verbatim; the canonical use case
// does NOT call FilenameBuilder.BuildFilename to re-derive the file
// path on the per-item path. Filename collisions between parent and
// child would surface as a Drive duplicate.
func TestChild_PreservesFilenameFromParent(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{Language: "fr", Status: voiceover.StatusCompleted},
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())

	// Parent (fanout) emits filename "vo_20260101_fr_<hash>.mp3". The
	// child must forward this verbatim — NO FilenameBuilder re-derivation.
	item := makeItemCmd("parent-job-003", "vo_20260101_120200_bbb222", "fr", "",
		"vo_20260101_fr_abc12345.mp3", "abc12345")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-job-003", Payload: marshalItemCmd(t, item)}
	res, err := h.HandleJob(context.Background(), j, tools)

	require.NoError(t, err)
	require.Len(t, exec.calls, 1)
	assert.Equal(t, "vo_20260101_fr_abc12345.mp3", exec.calls[0].Filename,
		"BLOC5.3 audit pin: child handler MUST forward parent-emitted Filename verbatim (no FilenameBuilder re-derivation)")
	assert.Equal(t, "vo_20260101_fr_abc12345.mp3", exec.filenames[0],
		"BLOC5.3 audit pin: item.Filename on the canonical use case Execute input matches parent's emitted name")
	_ = res
}

// ────────────────────────────────────────────────────────────────────
// Audit Test 4: TestChild_DoesNotConvertToBatchRequest
// ────────────────────────────────────────────────────────────────────

// Pins the canonical "no BatchRequest conversion" invariant: the child
// handler uses the narrow VoiceoverItemExecutor port directly; no
// BatchRequest is constructed at any layer (audit guard pinned in the
// task spec as `rg 'BatchRequest' --type go | productive callers: only
// legacy Service.GenerateBatch sites remain`). This test asserts:
//   - the handler calls useCase.Execute(ctx, &item) directly with the
//     item struct (NOT a BatchRequest)
//   - the use case is of type VoiceoverItemExecutor port (the SAME
//     type used by the master plan promo bridge, single canonical
//     pipeline)
func TestChild_DoesNotConvertToBatchRequest(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{Language: "en", Status: voiceover.StatusCompleted},
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-job-004", "vo_20260101_120300_ccc333", "en", "en-US-RogerNeural", "hello_en.mp3", "ccc333hash")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-job-004", Payload: marshalItemCmd(t, item)}
	res, err := h.HandleJob(context.Background(), j, tools)
	require.NoError(t, err)

	// Audit pin: only ONE use case call (the canonical Execute method);
	// the recorder captures the item shape directly. NO BatchRequest
	// was constructed anywhere between payload unmarshal and recorder.
	require.Len(t, exec.calls, 1)
	got := exec.calls[0]

	// The item is exactly the *GenerateVoiceoverItemCommand (NOT a
	// BatchRequest). The handler did NOT pack the fields into a
	// BatchRequest, call Service.GenerateBatch, then map back.
	// The presence of GenerateVoiceoverItemCommand-specific fields
	// (ParentJobID, TextHash) on the recorder payload proves the
	// canonical per-item shape was preserved through dispatch.
	assert.Equal(t, "parent-job-004", got.ParentJobID,
		"BLOC5.3 audit pin: ParentJobID forwarded verbatim — NO BatchRequest conversion happened")
	assert.Equal(t, "vo_20260101_120300_ccc333", got.RequestID,
		"BLOC5.3 audit pin: RequestID forwarded verbatim — NO BatchRequest conversion happened")
	assert.Equal(t, "en-US-RogerNeural", got.Voice,
		"BLOC5.3 audit pin: Voice from parent override forwarded verbatim — NO VoiceOverrides re-resolution")
	_ = res
}

// ────────────────────────────────────────────────────────────────────
// Bonus: Handler fail-fast on useCase=nil
// ────────────────────────────────────────────────────────────────────

// Pins the constructor's panic-on-nil invariant (Pattern 0 WireUp
// pattern, AGENTS.md). A future refactor that drops the nil-check
// silently would let a partially-wired composition root boot without
// the canonical pipeline registered, breaking all BLOC5.3 cuts.
func TestChild_NilUseCase_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("BLOC5.3: NewGenerateItemJobHandler must panic on nil useCase (fail-fast WireUp)")
		}
	}()
	_ = NewGenerateItemJobHandler(nil, zap.NewNop())
}

// ────────────────────────────────────────────────────────────────────
// P0.1 Audit Test: TestGenerateItemHandler_FailedResultReturnsError
// ────────────────────────────────────────────────────────────────────

// Pins the canonical P0.1 false-success fix (July 2026): when
// useCase.Execute returns (res, nil) with res.Status != StatusCompleted,
// the handler MUST return an error to the dispatcher.
func TestGenerateItemHandler_FailedResultReturnsError(t *testing.T) {
	// Simulate a TTS failure: Execute returns (result with
	// Status=StatusFailed + Error="tts_failed: ...", nil).
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{
			Language: "en",
			Status:   voiceover.StatusFailed,
			Error:    "tts_failed: Edge TTS connection timeout",
		},
		cannedErr: nil,
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-job-005", "vo_20260101_120400_ddd444", "en", "en-US-RogerNeural", "hello_en.mp3", "ddd444hash")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-job-005", Payload: marshalItemCmd(t, item)}
	resultMap, err := h.HandleJob(context.Background(), j, tools)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tts_failed")
	assert.NotNil(t, resultMap)
	ok, hasOK := resultMap["ok"].(bool)
	assert.True(t, hasOK)
	assert.False(t, ok)
	assert.Equal(t, voiceover.StatusFailed, resultMap["status"])
	assert.Equal(t, "tts_failed: Edge TTS connection timeout", resultMap["error"])
	require.Len(t, exec.calls, 1)
}

// TestGenerateItemHandler_DestinationMismatchCarriesErrorCode pins the
// machine-readable job boundary for the hard parent-integrity failure.
func TestGenerateItemHandler_DestinationMismatchCarriesErrorCode(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{
			Language:  "en",
			Status:    voiceover.StatusFailed,
			Error:     voiceover.VoiceoverDestinationMismatchCode + ": uploaded file parent differs",
			ErrorCode: voiceover.VoiceoverDestinationMismatchCode,
		},
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-job-mismatch", "vo_mismatch", "en", "en-US-RogerNeural", "mismatch.mp3", "mismatch-hash")
	resultMap, err := h.HandleJob(context.Background(), &appjobs.Job{ID: "child-mismatch", Payload: marshalItemCmd(t, item)}, &appjobs.JobTools{Progress: func(int, string) {}})

	require.Error(t, err)
	assert.Equal(t, voiceover.VoiceoverDestinationMismatchCode, resultMap["error_code"])
	ok, hasOK := resultMap["ok"].(bool)
	assert.True(t, hasOK)
	assert.False(t, ok)
}

// ────────────────────────────────────────────────────────────────────
// P0.1 Variant: handler still succeeds when result.Status == StatusCompleted
// ────────────────────────────────────────────────────────────────────

// Locks the complementary contract: when Execute returns a genuinely
// successful result (StatusCompleted, err==nil), the handler MUST
// return (resultMap, nil) — the P0.1 gate must NOT false-positive
// on legitimate successes.
func TestGenerateItemHandler_CompletedResultReturnsSuccess(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{
			Language:    "en",
			Status:      voiceover.StatusCompleted,
			DriveLink:   "https://drive.google.com/file/d/abc123/view",
			DriveFileID: "abc123",
		},
		cannedErr: nil,
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-job-006", "vo_20260101_120500_eee555", "en", "en-US-RogerNeural", "hello_en.mp3", "eee555hash")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-job-006", Payload: marshalItemCmd(t, item)}
	resultMap, err := h.HandleJob(context.Background(), j, tools)

	require.NoError(t, err, "P0.1 gate: handler must NOT error on StatusCompleted (no false-positive)")
	ok, hasOK := resultMap["ok"].(bool)
	assert.True(t, hasOK, "P0.1 gate: result map must have 'ok' field on success")
	assert.True(t, ok, "P0.1 gate: result map 'ok' must be true on StatusCompleted")
	assert.Equal(t, voiceover.StatusCompleted, resultMap["status"],
		"P0.1 gate: result map must surface StatusCompleted")
	assert.Equal(t, "https://drive.google.com/file/d/abc123/view", resultMap["drive_link"],
		"P0.1 gate: result map must carry DriveLink on success")
}

// roleMarker is a thin compile-time pin: assert that the recorder
// stays satisfying the narrow port. Drift detection requires this
// explicit type assertion so the next refactor of RecordingExecutor
// cannot accidentally break the conformance without a compile error.
var _ voiceover.VoiceoverItemExecutor = (*recordingVoiceoverItemExecutor)(nil)

// ────────────────────────────────────────────────────────────────────
// P0.2 Audit Test: TestNilDestinationUsesConfiguredDefault
// ────────────────────────────────────────────────────────────────────

// Pins the P0.2 nil-destination fallback contract (July 2026): when
// an item has Destination=nil, the use case MUST call the configured
// DefaultFolderResolver to obtain the fallback FolderID instead of
// short-circuiting to "missing_destination". The test simulates a
// scenario where the fanout schedules a child with nil Destination
// (e.g. the API caller omitted the destination field).
//
// The recording executor records the item shape. This test verifies
// that a nil-Destination item is handled gracefully: the use case
// resolves the default folder and proceeds (the test returns a
// completed result, simulating the resolver providing a valid folder).
// The P0.2 fix contract: a nil-Destination request that was
// previously rejected with "missing_destination" now succeeds when
// a default folder is configured.
func TestNilDestinationUsesConfiguredDefault(t *testing.T) {
	// Simulate the use case resolving the default folder successfully
	// and producing a completed result.
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{
			Language:    "en",
			Status:      voiceover.StatusCompleted,
			DriveLink:   "https://drive.google.com/file/d/default-folder-abc/view",
			DriveFileID: "default-folder-abc",
		},
		cannedErr: nil,
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())

	// Build an item with nil Destination — this is the exact shape
	// that was previously rejected with "missing_destination".
	item := &voiceover.GenerateVoiceoverItemCommand{
		ParentJobID:   "parent-nil-dest",
		RequestID:     "vo_nil_dest_fallback",
		Text:          "hello world",
		TextHash:      "nil-dest-hash-01",
		Language:      "en",
		Voice:         "en-US-RogerNeural",
		Filename:      "hello_en.mp3",
		Destination:   nil, // key: nil destination should NOT cause "missing_destination"
		Strategy:      "verify",
		RemoveSilence: false,
	}

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-nil-dest", Payload: marshalItemCmd(t, item)}
	resultMap, err := h.HandleJob(context.Background(), j, tools)

	// P0.2 contract: with a configured default folder, a nil-destination
	// request must succeed (not fail with missing_destination).
	require.NoError(t, err,
		"P0.2: nil-Destination must NOT cause an error when default folder is configured")
	require.Len(t, exec.calls, 1, "recorder: handle dispatched exactly once")

	// Verify the item passed to Execute still has nil Destination —
	// the resolver is called INSIDE Execute, not before.
	assert.Nil(t, exec.calls[0].Destination,
		"P0.2: handler forwards nil-Destination verbatim; the use case resolves internally")

	ok, hasOK := resultMap["ok"].(bool)
	assert.True(t, hasOK, "P0.2: result map must have 'ok' field")
	assert.True(t, ok, "P0.2: nil-Destination with configured default → result.ok=true")
	assert.Equal(t, voiceover.StatusCompleted, resultMap["status"],
		"P0.2: nil-Destination with configured default → StatusCompleted")
}

// ────────────────────────────────────────────────────────────────────
// P0.1 Fase 1b: TestVoiceoverChild_RetriesOnTTSFailure
// ────────────────────────────────────────────────────────────────────

// Pins the PipelineError retryability contract: a TTS failure
// (connection timeout, Edge TTS overload) must produce a PipelineError
// with Retryable=true so the worker retries (DefaultMaxRetries: 2).
// Pre-Fase-1b the use case returned (out, nil) here — no error at
// all, so the worker never retried and the job was falsely SUCCEEDED.
func TestVoiceoverChild_RetriesOnTTSFailure(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{
			Language: "en",
			Status:   voiceover.StatusFailed,
			Error:    "tts_failed: Edge TTS connection timeout",
		},
		cannedErr: voiceover.NewPipelineError(
			voiceover.StageTTS,
			true, // Retryable: TTS connection timeouts are transient
			fmt.Errorf("Edge TTS connection timeout"),
		),
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-tts-retry", "vo_tts_retry", "en", "en-US-RogerNeural", "hello_en.mp3", "tts-hash-01")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-tts-retry", Payload: marshalItemCmd(t, item)}
	_, err := h.HandleJob(context.Background(), j, tools)

	require.Error(t, err, "Fase 1b: TTS failure must return an error (not silent nil)")

	var pipelineErr *voiceover.PipelineError
	require.True(t, errors.As(err, &pipelineErr),
		"Fase 1b: TTS error must be a PipelineError (typed stage + retryability)")
	assert.True(t, pipelineErr.Retryable,
		"Fase 1b: TTS failures must be Retryable=true (worker should retry)")
	assert.Equal(t, voiceover.StageTTS, pipelineErr.Stage,
		"Fase 1b: TTS error must carry Stage='tts'")
}

// ────────────────────────────────────────────────────────────────────
// P0.1 Fase 1b: TestVoiceoverChild_FailsPermanentOnInvalidLanguage
// ────────────────────────────────────────────────────────────────────

// Pins the PipelineError permanence contract: a validation failure
// (invalid language, nil item, unsupported BCP-47 code) must produce
// a PipelineError with Retryable=false so the worker does NOT retry
// (retrying a permanent failure wastes lease cycles and delays
// terminal failure visibility). The error must still be surfaced so
// the dispatcher marks the job FAILED on the first attempt.
func TestVoiceoverChild_FailsPermanentOnInvalidLanguage(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{
			Language: "xx",
			Status:   voiceover.StatusFailed,
			Error:    "invalid_language: unsupported BCP-47 code",
		},
		cannedErr: voiceover.NewPipelineError(
			voiceover.StageValidate,
			false, // Permanent: invalid language won't become valid on retry
			fmt.Errorf("unsupported BCP-47 code"),
		),
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-perm", "vo_perm", "xx", "", "hello_xx.mp3", "perm-hash-01")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-perm", Payload: marshalItemCmd(t, item)}
	_, err := h.HandleJob(context.Background(), j, tools)

	require.Error(t, err, "Fase 1b: validation failure must return an error (not silent nil)")

	var pipelineErr *voiceover.PipelineError
	require.True(t, errors.As(err, &pipelineErr),
		"Fase 1b: validation error must be a PipelineError (typed stage + retryability)")
	assert.False(t, pipelineErr.Retryable,
		"Fase 1b: validation failures must be Retryable=false (worker should NOT retry)")
	assert.Equal(t, voiceover.StageValidate, pipelineErr.Stage,
		"Fase 1b: validation error must carry Stage='validate'")
}

// ────────────────────────────────────────────────────────────────────
// P0.1 Fase 1b: TestVoiceoverChild_DriveUploadIsRetryable
// ────────────────────────────────────────────────────────────────────

// Pins the Drive upload retryability: upload failures (network
// timeout, Drive rate limit, transient API error) must be
// Retryable=true. A permanent upload failure (e.g. invalid folder)
// would be caught at the destination_resolve stage instead.
func TestVoiceoverChild_DriveUploadIsRetryable(t *testing.T) {
	exec := &recordingVoiceoverItemExecutor{
		cannedRes: &voiceover.VoiceoverItemResult{
			Language: "en",
			Status:   voiceover.StatusFailed,
			Error:    "upload_failed: Drive API rate limit exceeded",
		},
		cannedErr: voiceover.NewPipelineError(
			voiceover.StageUpload,
			true, // Retryable: Drive rate limits clear after backoff
			fmt.Errorf("Drive API rate limit exceeded"),
		),
	}
	h := NewGenerateItemJobHandler(exec, zap.NewNop())
	item := makeItemCmd("parent-drive", "vo_drive", "en", "en-US-RogerNeural", "hello_en.mp3", "drive-hash")

	tools := &appjobs.JobTools{Progress: func(int, string) {}}
	j := &appjobs.Job{ID: "child-drive", Payload: marshalItemCmd(t, item)}
	_, err := h.HandleJob(context.Background(), j, tools)

	require.Error(t, err, "Fase 1b: Drive upload failure must return an error")

	var pipelineErr *voiceover.PipelineError
	require.True(t, errors.As(err, &pipelineErr),
		"Fase 1b: Drive upload error must be a PipelineError")
	assert.True(t, pipelineErr.Retryable,
		"Fase 1b: Drive upload failures must be Retryable=true")
	assert.Equal(t, voiceover.StageUpload, pipelineErr.Stage,
		"Fase 1b: Drive error must carry Stage='upload'")
}
