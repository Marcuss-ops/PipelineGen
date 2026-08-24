// Package completion — complete_job_tx_outbox_test.go.
//
// Pins the cross-path dedup alignment of the job.completed event_key: the
// Sender-side completion services (Service.emitOutboxEvents and
// WithArtifactsService.emitArtifactOutboxEvents) must emit the SAME canonical
// key as the JobFinalizer and SQLiteStore.Complete/Fail —
// outboxevents.JobCompletedEventKey(jobID) = "job.completed:<jobID>".
//
// The key is the job id alone (no attempt segment) because a job reaches a
// terminal state at most once: a retry or a cross-path re-completion of the
// same job must collapse to ONE outbox row via ON CONFLICT(event_key) DO
// NOTHING. The previous attempt-scoped SHA-256
// (remote.CompleteJobIdempotencyKey(jobID, attempt, "JOB_COMPLETED")) would
// produce a DIFFERENT key per attempt and per producer, silently enqueuing
// duplicate job.completed events for the same job.
package completion

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// recordingTx is a minimal TxContext that only records InsertOutboxEnvelope
// (the single method the emit* helpers exercise). The other six methods are
// no-op stubs — the emit helpers never touch them.
type recordingTx struct {
	envelopes []OutboxEnvelope
}

func (r *recordingTx) GetJob(context.Context, string) (*JobRow, error) { return nil, nil }
func (r *recordingTx) UpdateJobToSucceededCAS(context.Context, string, string, int) (int64, error) {
	return 0, nil
}
func (r *recordingTx) InsertResultOnConflict(context.Context, string, int, string, []byte, string) (int64, bool, error) {
	return 0, false, nil
}
func (r *recordingTx) GetPriorArtifactHashes(context.Context, string) (map[string]PriorArtifactHash, error) {
	return nil, nil
}
func (r *recordingTx) PersistArtifactMap(context.Context, string, int, []ArtifactMapEntry) error {
	return nil
}
func (r *recordingTx) InsertOutboxEnvelope(_ context.Context, e OutboxEnvelope) error {
	r.envelopes = append(r.envelopes, e)
	return nil
}
func (r *recordingTx) InsertAssetLocations(context.Context, []AssetLocationEntry) error { return nil }

// findJobCompletedEnvelope returns the job.completed envelope among the
// emitted set (there is exactly one per completion).
func findJobCompletedEnvelope(t *testing.T, envelopes []OutboxEnvelope) OutboxEnvelope {
	t.Helper()
	for _, e := range envelopes {
		if e.EventKind == outboxevents.EventJobCompleted {
			return e
		}
	}
	t.Fatalf("no job.completed envelope emitted (got %d envelopes)", len(envelopes))
	return OutboxEnvelope{}
}

func TestEmitOutboxEvents_JobCompletedUsesCanonicalKey(t *testing.T) {
	tx := &recordingTx{}
	req := &remote.CompleteJobRequest{
		JobID:   "job-e2e",
		Attempt: 7,
		Result:  []byte(`{"ok":true}`),
		Artifacts: job.RemoteArtifactManifest{
			Artifacts: []job.RemoteArtifact{{ID: "job-e2e:vo", Kind: "voiceover"}},
		},
	}
	if err := (&Service{}).emitOutboxEvents(context.Background(), tx, req, nil); err != nil {
		t.Fatal(err)
	}

	jc := findJobCompletedEnvelope(t, tx.envelopes)
	if jc.IdempotencyKey != outboxevents.JobCompletedEventKey("job-e2e") {
		t.Fatalf("job.completed idempotency key = %q, want %q (attempt %d must not be part of the key — one job completes once)", jc.IdempotencyKey, outboxevents.JobCompletedEventKey("job-e2e"), req.Attempt)
	}
	if jc.EventKind != outboxevents.EventJobCompleted {
		t.Fatalf("job.completed event kind = %q, want %q", jc.EventKind, outboxevents.EventJobCompleted)
	}
}

func TestEmitArtifactOutboxEvents_JobCompletedUsesCanonicalKey(t *testing.T) {
	tx := &recordingTx{}
	req := &remote.CompleteWithArtifactsRequest{
		JobID:   "job-art",
		Attempt: 4,
		Result:  []byte(`{"ok":true}`),
	}
	published := []*finalization.PublishedArtifact{
		{ArtifactID: "job-art:vo", Kind: finalization.KindVoiceover},
	}
	if err := (&WithArtifactsService{}).emitArtifactOutboxEvents(context.Background(), tx, req, published); err != nil {
		t.Fatal(err)
	}

	jc := findJobCompletedEnvelope(t, tx.envelopes)
	if jc.IdempotencyKey != outboxevents.JobCompletedEventKey("job-art") {
		t.Fatalf("job.completed idempotency key = %q, want %q (must match the finalizer + SQLiteStore dedup key)", jc.IdempotencyKey, outboxevents.JobCompletedEventKey("job-art"))
	}
	if jc.EventKind != outboxevents.EventJobCompleted {
		t.Fatalf("job.completed event kind = %q, want %q", jc.EventKind, outboxevents.EventJobCompleted)
	}
}
