// Package scriptgeneration — runner_service_test.go covers the
// ingress surface tests for Service.Start validation, the
// NewService / NewRunner nil-port panic behavior, and the
// repository's GetByJobID lookup. These isolates the validation
// + factory contract from the runner pipeline contract so a
// regression in the ingress layer is caught without spinning up
// a full Execute loop.
//
// godlike/06 SSOT invariants asserted:
//
//   - Service.Start rejects requests with empty idempotency_key,
//     empty source.type, and docs.enabled=true with no languages
//     configured.
//   - Service.Start happy path creates a run with id, status
//     PENDING, stage NORMALIZING, and a status URL containing
//     the run id; the background runner eventually drives the
//     run to COMPLETED.
//   - NewService panics on nil repo, nil textGen, nil translator,
//     nil docPublisher (Required ports).
//   - NewRunner panics on nil repo (RunRepository is required).
//   - inMemRunRepository.GetByJobID returns the matched run for
//     a known job id and (nil, nil) for unknown job ids.
package scriptgeneration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceStart_Validation(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	voiceoverGen := newStubVoiceoverGenerator()
	docPub := newStubDocumentPublisher()

	svc := NewService(repo, textGen, translator, voiceoverGen, docPub, canonicalTestDocumentRenderer{})
	svc.SetScriptDocsFolderID("test-docs-folder")

	t.Run("missing idempotency key", func(t *testing.T) {
		req := defaultTestRequest()
		req.IdempotencyKey = ""
		_, err := svc.Start(context.Background(), req)
		assert.ErrorContains(t, err, "idempotency_key")
	})

	t.Run("missing source type", func(t *testing.T) {
		req := defaultTestRequest()
		req.Source.Type = ""
		_, err := svc.Start(context.Background(), req)
		assert.ErrorContains(t, err, "source.type")
	})

	t.Run("docs enabled without languages", func(t *testing.T) {
		req := defaultTestRequest()
		req.Docs = DocumentsConfig{Enabled: true, Languages: nil}
		req.Languages = nil // no fallback available
		_, err := svc.Start(context.Background(), req)
		assert.ErrorContains(t, err, "docs.enabled requires at least one language")
	})

	t.Run("happy path start", func(t *testing.T) {
		req := defaultTestRequest()
		// The fixture publisher is intentionally a stub. Documents are
		// opt-in, and docs.enabled=true now requires the real-provider
		// preflight, so this service smoke test exercises the non-Docs path.
		req.Docs.Enabled = false
		req.Docs.Languages = nil
		result, err := svc.Start(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.NotEmpty(t, result.Run.ID, "run ID should be set")
		assert.Equal(t, RunStatusPending, result.Run.Status, "initial status should be PENDING")
		assert.Equal(t, StageNormalizing, result.Run.CurrentStage)
		assert.Contains(t, result.StatusURL, result.Run.ID)

		// Wait briefly for the background runner.
		time.Sleep(200 * time.Millisecond)

		final, err := repo.Get(context.Background(), result.Run.ID)
		require.NoError(t, err)
		assert.Equal(t, RunStatusCompleted, final.Status)
	})
}

func TestNewService_PanicsOnNilRequiredPorts(t *testing.T) {
	repo := newInMemRunRepository()
	textGen := newStubTextGenerator(defaultTestScenes())
	translator := newStubTranslator()
	docPub := newStubDocumentPublisher()

	assert.Panics(t, func() {
		NewService(nil, textGen, translator, nil, docPub)
	}, "nil repo should panic")

	assert.Panics(t, func() {
		NewService(repo, nil, translator, nil, docPub)
	}, "nil textGen should panic")

	assert.Panics(t, func() {
		NewService(repo, textGen, nil, nil, docPub)
	}, "nil translator should panic")

	assert.Panics(t, func() {
		NewService(repo, textGen, translator, nil, nil)
	}, "nil docPublisher should panic")
}

func TestNewRunner_PanicsOnNilRepo(t *testing.T) {
	assert.Panics(t, func() {
		NewRunner(nil, nil, nil, nil, nil)
	}, "nil repo should panic")
}

func TestInMemRepo_GetByJobID(t *testing.T) {
	repo := newInMemRunRepository()

	run1 := &GenerationRun{
		ID:     "run-001",
		JobID:  "job-abc",
		Status: RunStatusRunning,
	}
	run2 := &GenerationRun{
		ID:     "run-002",
		JobID:  "job-xyz",
		Status: RunStatusCompleted,
	}
	require.NoError(t, repo.Create(context.Background(), run1))
	require.NoError(t, repo.Create(context.Background(), run2))

	found, err := repo.GetByJobID(context.Background(), "job-abc")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "run-001", found.ID)
	assert.Equal(t, "job-abc", found.JobID)

	notFound, err := repo.GetByJobID(context.Background(), "job-nonexistent")
	require.NoError(t, err)
	assert.Nil(t, notFound, "non-existent job should return nil, nil")
}
