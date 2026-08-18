// Package scriptgeneration — runner_idempotency_restart_test.go certifies the
// total-idempotency contract: after a crash and restart the resume must write
// 0 duplicate Drive files (voiceover synthesis), 0 duplicate DB rows (script
// persistence), and 0 duplicate Docs (document upsert). Reuse is decided from
// the durable per-unit result — never from re-invoking the providers.
package scriptgeneration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// idempotentDocPublisher is a DocumentPublisher whose identity is deterministic
// per language (doc-<lang>). It fails the first upsert for any language in
// failFirst, simulating a crash after earlier languages were already
// published. It records every successful upsert so a test can count
// duplicates.
type idempotentDocPublisher struct {
	mu        sync.Mutex
	failFirst map[Language]bool
	upserts   map[Language]int
	created   map[Language]string
}

func newIdempotentDocPublisher() *idempotentDocPublisher {
	return &idempotentDocPublisher{
		failFirst: map[Language]bool{},
		upserts:   map[Language]int{},
		created:   map[Language]string{},
	}
}

func (p *idempotentDocPublisher) UpsertDocument(_ context.Context, in DocumentInput) (DocumentReference, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.upserts[in.Language]++
	if p.failFirst[in.Language] && p.upserts[in.Language] == 1 {
		return DocumentReference{}, fmt.Errorf("docs provider unavailable (simulated crash)")
	}
	id := "doc-" + string(in.Language)
	p.created[in.Language] = id
	return DocumentReference{ID: id, Link: "https://docs.google.com/document/d/" + id}, nil
}

func (p *idempotentDocPublisher) upsertsFor(lang Language) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upserts[lang]
}

func (p *idempotentDocPublisher) createdCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.created)
}

// TestRunner_IdempotentRestart_NoDuplicateWrites crashes the run during
// document publication (after voiceover synthesis and script persistence have
// completed, and after the EN document was published) and then restarts it.
// The resume must:
//
//	0 duplicate Drive files — every scene×language voiceover synthesized once
//	0 duplicate DB rows   — script persisted once, same canonical ScriptID
//	0 duplicate Docs      — EN reused (never re-uploaded), ES published once
func TestRunner_IdempotentRestart_NoDuplicateWrites(t *testing.T) {
	runner, repo, _, _, voiceoverGen, _, _ := newTestRunner()
	persistence := &recordingScriptPersistence{}
	docs := newIdempotentDocPublisher()
	runner.SetScriptPersistence(persistence)
	runner.docPublisher = docs

	req := defaultTestRequest() // en + es, 3 scenes, CHUNKED_VOICEOVER + docs
	req.SaveToDB = true

	runID := "run-idempotent-restart"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	// Crash on the ES document: EN is published first and checkpointed, ES
	// fails, so the run dies mid-publication.
	docs.mu.Lock()
	docs.failFirst["es"] = true
	docs.mu.Unlock()

	runner.Execute(context.Background(), runID, req)
	failed := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusFailed, failed.Status)
	require.Equal(t, StagePublishingDocuments, failed.FailedStage)

	// After the crash: 6 voiceovers synthesized (3 scenes × en+es), script
	// persisted once, EN published once (durable per-language checkpoint).
	require.Equal(t, 6, voCallCount(voiceoverGen), "all 6 voiceovers synthesized before the crash")
	require.Equal(t, 1, persistence.calls, "script persisted once before the crash")
	require.NotNil(t, failed.Result, "partial result must survive the crash")
	require.Equal(t, int64(77), failed.Result.ScriptID, "ScriptID must be durably checkpointed")
	require.Equal(t, "doc-en", failed.Result.Documents["en"].ID, "EN document must be checkpointed before the crash")

	// "Restart": clear the fault and re-execute the same run.
	docs.mu.Lock()
	docs.failFirst = map[Language]bool{}
	docs.mu.Unlock()

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, final.Status)

	// 0 duplicate Drive files: no voiceover is re-synthesized on resume.
	require.Equal(t, 6, voCallCount(voiceoverGen), "resume must not re-synthesize any voiceover")

	// 0 duplicate DB rows: the canonical script row is reused, never re-written.
	require.Equal(t, 1, persistence.calls, "resume must not persist a second script row")
	require.Equal(t, int64(77), final.Result.ScriptID, "the canonical ScriptID is preserved")

	// 0 duplicate Docs: EN was published once and never re-uploaded on resume
	// (still a single upsert attempt); ES succeeded once after its crash
	// retry. Exactly two distinct documents exist — one per language.
	require.Equal(t, 1, docs.upsertsFor("en"), "resume must not re-upload the already-published EN document")
	require.Equal(t, 2, docs.createdCount(), "exactly two distinct documents must be created (0 duplicates)")
	require.Equal(t, "doc-en", final.Result.Documents["en"].ID, "EN document identity is stable across restart")
	require.Equal(t, "doc-es", final.Result.Documents["es"].ID, "ES document published once on resume")
}
