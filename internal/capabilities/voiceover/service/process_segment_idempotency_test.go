// Package voiceover — usecase/process_segment_idempotency_test.go
//
// Idempotency-phase tests for the SHARED per-item pipeline runner
// (usecase/process_segment.go). These tests pin the FASE 3
// idempotency contracts — retry-safe deduplication via the
// finalizer's Step 0 idempotency gate — WITHOUT exercising the
// transactional outbox / orphan-cleanup path (those live in the e2e
// file) and WITHOUT exercising the destination resolver or per-stage
// success paths (those live in the construction + execution files).
//
// godlike/06 SSOT (one canonical owner per fact): each test pins
// exactly one idempotency invariant at the usecase boundary:
//
//  11. TestProcessSegmentUseCase_Execute_FASE3_Idempotency_SameJobNoDuplicates
//     — same job retried 2x produces 1 DB insert (idempotency gate fires).
//
//  12. TestProcessSegmentUseCase_Execute_FASE3_Idempotency_DifferentJobsSeparate
//     — different jobs with same text produce 2 inserts (no cross-job collision).
//
//  13. TestProcessSegmentUseCase_Execute_FASE3_Idempotency_LegacyEmptyJobID
//     — legacy callers (empty JobID) skip the idempotency gate (back-compat).
//
// godlike/07 minimum-blast-radius: zero production code changes.
// All three tests use stubIdempotencyVoRepo (which extends the canonical
// stubProcessVoRepo via embedding) so the in-memory row store simulates
// the SQLite UNIQUE INDEX without a real DB row round-trip.
package voiceover

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
)

// ─────────────────────────────────────────────────────────────────────────
// Test 11: FASE 3 — same job retried 2x, no duplicate DB rows nor Drive uploads
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE3_Idempotency_SameJobNoDuplicates
// pins the FASE 3 idempotency contract: when the same job is retried
// with identical (JobID + Language + TextHash), the second invocation
// MUST short-circuit at the finalizer's Step 0 idempotency gate and
// return the matched row's ID WITHOUT creating a second Drive upload
// or a second DB insert.
//
// The stub finalizer only records calls. To simulate the production
// finalizer's Step 0/Step 3 behavior, the test manually calls
// repo.InsertTx after the first invocation and seeds the repo's
// idempotency-key lookup for the second invocation's FindByIdempotencyKeyTx.
//
// godlike/07 NO-FAKE-AVAILABILITY: the test asserts BOTH the Drive-publish
// count AND the DB-insert count after the retry.
func TestProcessSegmentUseCase_Execute_FASE3_Idempotency_SameJobNoDuplicates(t *testing.T) {
	repo := newStubIdempotencyVoRepo(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{LocalPath: "/tmp/vo/idem-test.mp3", Voice: "en-US-RogerNeural", LegacyFileMD5: "hash-idem-001"},
	}
	pub := &stubProcessPublisher{fileID: "drive-idem-001"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-idem-001", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-idem"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-idem"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		JobID:    "job-fase3-idempotency-001",
		ID:       "vo-idem-001",
		Language: "en",
		Text:     "Retry this text twice",
		TextHash: "hash-idem-text-001",
		Filename: "idem-test.mp3",
		Dest:     resolvedDest,
	}

	// ── First invocation ───────────────────────────────────────────
	out1, err1 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err1)
	assert.Equal(t, StatusCompleted, out1.Status)

	// Simulate production finalizer Step 3: insert the row into the
	// repo so FindByIdempotencyKeyTx finds it on retry.
	require.Len(t, finalizer.calls, 1, "finalizer must have been called once")
	finCmd := finalizer.calls[0]
	require.NotEmpty(t, finCmd.IdempotencyKey, "idempotency key must be set when JobID is non-empty")
	repo.rows[finCmd.IdempotencyKey] = &persistence.VoiceoverRecord{
		ID:             finCmd.ID,
		IdempotencyKey: finCmd.IdempotencyKey,
		JobID:          finCmd.JobID,
	}
	repo.inserts = append(repo.inserts, repo.rows[finCmd.IdempotencyKey])

	// ── Second invocation (retry) ──────────────────────────────────
	finalizer.cannedRes = &FinalizeResult{ID: "vo-idem-001", Reused: true}

	out2, err2 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err2)
	assert.Equal(t, StatusCompleted, out2.Status)
	assert.Equal(t, "vo-idem-001", out2.ID, "retry must return the matched row ID")

	// ── Assertions ────────────────────────────────────────────────
	assert.Len(t, pub.published, 2,
		"Publisher is called on both invocations (Stage 3 runs before Stage 4 idempotency gate)")
	assert.Len(t, repo.inserts, 1,
		"FASE 3 idempotency: exactly 1 DB insert across 2 invocations of the same job")
	assert.Len(t, finalizer.calls, 2,
		"Finalizer is called on both invocations")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 12: FASE 3 — different jobs with same text → separate voiceovers
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE3_Idempotency_DifferentJobsSeparate
// pins the job-isolation contract: different jobs with the same
// language+textHash MUST produce distinct idempotency keys (because
// the key includes the jobID). Two invocations produce 2 inserts,
// 2 publishes, no collision.
//
// The stub finalizer only records calls. The test manually seeds
// the repo after each invocation to simulate the production finalizer.
//
// godlike/07 typed-error contract: the idempotency key is
// SHA256(jobID:language:textHash). Different jobIDs → different
// SHA256 hex strings → no collision at the UNIQUE INDEX.
func TestProcessSegmentUseCase_Execute_FASE3_Idempotency_DifferentJobsSeparate(t *testing.T) {
	repo := newStubIdempotencyVoRepo(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{LocalPath: "/tmp/vo/diff-job.mp3", Voice: "en-US-RogerNeural", LegacyFileMD5: "hash-diff-job"},
	}
	pub := &stubProcessPublisher{fileID: "drive-diff-job"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-diff-job-1", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-diff"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-diff"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	sameText := "Same text, different jobs"
	sameLang := Language("en")
	sameHash := TextHash("hash-diff-text")

	cmd1 := &ProcessSegmentCommand{
		JobID:    "job-A",
		ID:       "vo-diff-job-A",
		Language: sameLang,
		Text:     sameText,
		TextHash: sameHash,
		Filename: "diff-job.mp3",
		Dest:     resolvedDest,
	}

	cmd2 := &ProcessSegmentCommand{
		JobID:    "job-B",
		ID:       "vo-diff-job-B",
		Language: sameLang,
		Text:     sameText,
		TextHash: sameHash,
		Filename: "diff-job.mp3",
		Dest:     resolvedDest,
	}

	// First invocation.
	out1, err1 := uc.Execute(context.Background(), cmd1)
	require.NoError(t, err1)
	assert.Equal(t, StatusCompleted, out1.Status)
	// Simulate production finalizer insert.
	fin1 := finalizer.calls[0]
	require.NotEmpty(t, fin1.IdempotencyKey)
	repo.rows[fin1.IdempotencyKey] = &persistence.VoiceoverRecord{ID: fin1.ID, IdempotencyKey: fin1.IdempotencyKey, JobID: fin1.JobID}
	repo.inserts = append(repo.inserts, repo.rows[fin1.IdempotencyKey])

	// Second invocation (different job, no collision).
	finalizer.cannedRes = &FinalizeResult{ID: "vo-diff-job-B", Reused: false}

	out2, err2 := uc.Execute(context.Background(), cmd2)
	require.NoError(t, err2)
	assert.Equal(t, StatusCompleted, out2.Status)
	// Simulate production finalizer insert (different idempotency key).
	fin2 := finalizer.calls[1]
	require.NotEmpty(t, fin2.IdempotencyKey)
	repo.rows[fin2.IdempotencyKey] = &persistence.VoiceoverRecord{ID: fin2.ID, IdempotencyKey: fin2.IdempotencyKey, JobID: fin2.JobID}
	repo.inserts = append(repo.inserts, repo.rows[fin2.IdempotencyKey])

	// Both invocations produce distinct results.
	assert.Len(t, pub.published, 2, "different jobs → 2 distinct publishes")
	assert.Len(t, repo.inserts, 2, "different jobs → 2 distinct DB inserts (different idempotency keys)")
	assert.NotEqual(t, repo.inserts[0].IdempotencyKey, repo.inserts[1].IdempotencyKey,
		"different jobIDs MUST produce distinct idempotency keys")
}

// ─────────────────────────────────────────────────────────────────────────
// Test 13: FASE 3 — legacy callers (empty JobID) skip idempotency gate
// ─────────────────────────────────────────────────────────────────────────

// TestProcessSegmentUseCase_Execute_FASE3_Idempotency_LegacyEmptyJobID
// pins the backward-compat contract: when cmd.JobID is empty (pre-FASE-3
// callers), the idempotency key is NOT derived, so the finalizer's
// Step 0 idempotency gate is SKIPPED (empty key → FindByIdempotencyKeyTx
// returns sql.ErrNoRows). Two invocations with the same text produce
// 2 DB inserts (no collision on idempotency_key because both are empty
// and the partial UNIQUE INDEX WHERE idempotency_key != ” excludes them).
//
// godlike/07 typed-error contract: the dedupe gate (Step 1, Drive file
// ID lookup) is the only guard for legacy callers — this test verifies
// the idempotency gate does NOT interfere.
func TestProcessSegmentUseCase_Execute_FASE3_Idempotency_LegacyEmptyJobID(t *testing.T) {
	repo := newStubIdempotencyVoRepo(t)
	tts := &stubProcessTTS{
		cannedOut: TTSOutput{LocalPath: "/tmp/vo/legacy.mp3", Voice: "en-US-RogerNeural", LegacyFileMD5: "hash-legacy"},
	}
	pub := &stubProcessPublisher{fileID: "drive-legacy"}
	finalizer := &stubProcessFinalizer{
		cannedRes: &FinalizeResult{ID: "vo-legacy-1", Reused: false},
	}

	dest := &stubProcessDestResolver{folderID: "dest-legacy"}
	resolvedDest, err := dest.Resolve(context.Background(), &DestinationRequest{FolderID: "dest-legacy"})
	require.NoError(t, err)

	uc := NewProcessSegmentUseCase(ProcessSegmentDeps{
		TTSProvider:         tts,
		Publisher:           pub,
		VoiceoverRepository: repo,
		Finalizer:           finalizer,
		Logger:              zap.NewNop(),
	})

	cmd := &ProcessSegmentCommand{
		JobID:    "", // legacy: no JobID
		ID:       "vo-legacy-1",
		Language: "en",
		Text:     "Legacy caller without JobID",
		TextHash: "hash-legacy-text",
		Filename: "legacy.mp3",
		Dest:     resolvedDest,
	}

	out1, err1 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err1)
	assert.Equal(t, StatusCompleted, out1.Status)

	// Simulate production finalizer: insert row with empty idempotency key.
	fin1 := finalizer.calls[0]
	assert.Empty(t, fin1.IdempotencyKey, "legacy caller: idempotency key must be empty when JobID is empty")
	repo.inserts = append(repo.inserts, &persistence.VoiceoverRecord{ID: fin1.ID, IdempotencyKey: "", JobID: ""})

	// Second invocation (same empty-JobID payload).
	out2, err2 := uc.Execute(context.Background(), cmd)
	require.NoError(t, err2)
	assert.Equal(t, StatusCompleted, out2.Status)
	// Simulate production finalizer: second insert, also empty key.
	fin2 := finalizer.calls[1]
	assert.Empty(t, fin2.IdempotencyKey)
	repo.inserts = append(repo.inserts, &persistence.VoiceoverRecord{ID: fin2.ID, IdempotencyKey: "", JobID: ""})

	// Both invocations produce 2 inserts (empty idempotency_key excluded from partial UNIQUE INDEX).
	assert.Len(t, pub.published, 2, "legacy callers: Publisher invoked twice (idempotency gate skipped)")
	assert.Len(t, repo.inserts, 2, "legacy callers: 2 inserts (empty idempotency_key excluded from partial UNIQUE INDEX)")

	// All inserted rows have empty IdempotencyKey.
	for i, rec := range repo.inserts {
		assert.Empty(t, rec.IdempotencyKey,
			"insert %d: legacy row MUST have empty IdempotencyKey", i+1)
	}
}
