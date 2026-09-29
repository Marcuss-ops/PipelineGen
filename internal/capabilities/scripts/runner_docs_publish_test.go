package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// missingRunReturnsNilRepository matches the PRODUCTION contract: the real
// SQLiteRunRepository.scan returns (nil, nil) on sql.ErrNoRows, and the port
// documents that a missing run is reported that way. The in-memory double used
// by the runner tests instead returns an error, so a test of the not-found
// classification has to use the production shape — otherwise it would pin the
// double's behaviour and leave the real path unverified.
type missingRunReturnsNilRepository struct {
	*inMemRunRepository
}

func (r missingRunReturnsNilRepository) Get(ctx context.Context, runID string) (*GenerationRun, error) {
	run, err := r.inMemRunRepository.Get(ctx, runID)
	if err != nil {
		return nil, nil
	}
	return run, nil
}

func docsPublishRunner(t *testing.T) (*Runner, *inMemRunRepository) {
	t.Helper()
	repo := newInMemRunRepository()
	// The document ports are intentionally nil: every case below must be
	// decided BEFORE any renderer or publisher is reached, so a nil port is
	// what makes "no work happened" observable rather than merely asserted.
	return NewRunner(repo, nil, nil, nil, nil), repo
}

// TestPublishRunDocuments_RejectsAnEmptyRunReference pins that an unresolvable
// reference never reaches the repository or the render path.
func TestPublishRunDocuments_RejectsAnEmptyRunReference(t *testing.T) {
	t.Parallel()

	r, _ := docsPublishRunner(t)
	_, err := r.PublishRunDocuments(context.Background(), DocsPublishRequest{
		RunID:      "   ",
		ChildJobID: "job_child",
	})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("PublishRunDocuments() error = %v, want ErrRunNotFound", err)
	}
}

// TestPublishRunDocuments_RequiresTheChildIdentity pins that work is never
// recorded against an empty job id: without the child's identity the execution
// ledger would attribute the publication to nobody.
func TestPublishRunDocuments_RequiresTheChildIdentity(t *testing.T) {
	t.Parallel()

	r, repo := docsPublishRunner(t)
	if err := repo.Create(context.Background(), &GenerationRun{
		ID: "run_1", Status: RunStatusRunning, Result: &GenerateResult{},
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	err := func() error {
		_, err := r.PublishRunDocuments(context.Background(), DocsPublishRequest{RunID: "run_1"})
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "child job id") {
		t.Fatalf("PublishRunDocuments() error = %v, want a missing-child-identity error", err)
	}
}

// TestPublishRunDocuments_ClassifiesAMissingRun pins the deterministic failure
// the handler re-exports: a run that is gone must be classifiable, because the
// broker's retry budget must not be spent against a payload that cannot change.
func TestPublishRunDocuments_ClassifiesAMissingRun(t *testing.T) {
	t.Parallel()

	repo := missingRunReturnsNilRepository{inMemRunRepository: newInMemRunRepository()}
	r := NewRunner(repo, nil, nil, nil, nil)

	err := func() error {
		_, err := r.PublishRunDocuments(context.Background(), DocsPublishRequest{
			RunID:      "run_gone",
			ChildJobID: "job_child",
		})
		return err
	}()
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("PublishRunDocuments() error = %v, want ErrRunNotFound", err)
	}
}

// TestPublishRunDocuments_RefusesARunWithNoSnapshot is the no-fake-availability
// guard: with nothing to render from, publishing "successfully" would let the
// parent aggregator flip the parent to COMPLETED on an empty success.
func TestPublishRunDocuments_RefusesARunWithNoSnapshot(t *testing.T) {
	t.Parallel()

	r, repo := docsPublishRunner(t)
	if err := repo.Create(context.Background(), &GenerationRun{
		ID: "run_2", Status: RunStatusRunning,
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	err := func() error {
		_, err := r.PublishRunDocuments(context.Background(), DocsPublishRequest{
			RunID:      "run_2",
			ChildJobID: "job_child",
		})
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "no checkpointed result") {
		t.Fatalf("PublishRunDocuments() error = %v, want a missing-snapshot error", err)
	}
}

// TestPublishRunDocuments_IsANoOpForACompletedRun pins the idempotent terminal
// case: a run that already completed publishes nothing and writes no telemetry,
// so a retried child cannot pollute a closed run's stages.
func TestPublishRunDocuments_IsANoOpForACompletedRun(t *testing.T) {
	t.Parallel()

	r, repo := docsPublishRunner(t)
	if err := repo.Create(context.Background(), &GenerationRun{
		ID: "run_3", Status: RunStatusCompleted, Result: &GenerateResult{},
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	if _, err := r.PublishRunDocuments(context.Background(), DocsPublishRequest{
		RunID:      "run_3",
		ChildJobID: "job_child",
	}); err != nil {
		t.Fatalf("PublishRunDocuments() on a completed run = %v, want nil", err)
	}
}

// TestPublishRunDocuments_NoDuplicateDocOnRetry is acceptance gate
// "No duplicate Doc on retry" (TICKET-CORE-READY-DURABLE-DAG section 5) made
// executable.
//
// The gate is the reason the broker may retry this child at all. A retried
// child re-enters the SAME publish path against the SAME durable run, so the
// property that must hold is not "a retry is safe because it is rare" but "a
// language that already has a document is not published again".
//
// The run under test is built from a REAL completed run's result rather than a
// hand-written fixture: the document map, the per-language text and the audio
// references are exactly what the pipeline produces, so the test cannot pass
// against a shape the pipeline never emits.
func TestPublishRunDocuments_NoDuplicateDocOnRetry(t *testing.T) {
	ctx := context.Background()

	// ── 1. Produce a real run whose documents are durable ────────────────
	source := newInMemRunRepository()
	sourceDocs := newStubDocumentPublisher()
	sourceReq := defaultTestRequest()
	sourceReq.Source.Type = SourceText
	sourceReq.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}

	sourceRunner := NewRunner(source, newStubTextGenerator(defaultTestScenes()), newStubTranslator(),
		newStubVoiceoverGenerator(), sourceDocs, &capturingDocumentRenderer{})
	sourceRunner.SetLogger(zap.NewNop())
	sourceRunner.SetScriptDocsFolderID("test-docs-folder")
	sourceRunner.SetCombinedAudioRenderer(&stubCombinedAudioRenderer{})

	const sourceRunID = "run-docs-gate-source"
	require.NoError(t, source.Create(ctx, &GenerationRun{
		ID: sourceRunID, Request: sourceReq, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))
	sourceRunner.Execute(ctx, sourceRunID, sourceReq)
	sourceFinal := awaitCompletion(t, source, sourceRunID, 5*time.Second)
	require.Equal(t, RunStatusCompleted, sourceFinal.Status, "source run must complete: %s", sourceFinal.ErrorMessage)
	require.NotNil(t, sourceFinal.Result)
	published := sourceFinal.Result.Documents
	require.NotEmpty(t, published, "the source run must have published at least one document")
	require.Len(t, sourceDocs.records, 1, "the inline pass publishes exactly one document")

	// ── 2. The child re-enters against a run that already has them ──────
	// Status RUNNING, not COMPLETED: the terminal case is a different
	// (no-op) branch. This is the branch a broker RETRY takes — the run is
	// still post-core, its documents already durable.
	repo := newInMemRunRepository()
	docPub := newStubDocumentPublisher()
	runner := NewRunner(repo, nil, nil, nil, docPub, &capturingDocumentRenderer{})
	runner.SetLogger(zap.NewNop())
	runner.SetScriptDocsFolderID("test-docs-folder")

	const runID = "run-docs-gate-retry"
	require.NoError(t, repo.Create(ctx, &GenerationRun{
		ID: runID, Request: sourceReq, Status: RunStatusRunning,
		CurrentStage: StageCoreReady, Result: sourceFinal.Result,
	}))

	// ── 3. The first child attempt and its retry ───────────────────────
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := runner.PublishRunDocuments(ctx, DocsPublishRequest{
			RunID: runID, ChildJobID: "job_docs_child", Attempt: attempt,
		}); err != nil {
			t.Fatalf("attempt %d: PublishRunDocuments() error = %v, want nil", attempt, err)
		}
	}

	// The gate: zero publications, on both attempts, because every requested
	// language was already durable. A single call here is a duplicate Doc.
	require.Zero(t, docPub.callCount,
		"a retry must not publish a second document for a language that already has one (records: %+v)", docPub.records)

	// And the outcome still reports the COMPLETE set — a retry that published
	// nothing must not report an empty document set, or the parent result
	// would lose the links the first attempt won.
	outcome, err := runner.PublishRunDocuments(ctx, DocsPublishRequest{
		RunID: runID, ChildJobID: "job_docs_child", Attempt: 3,
	})
	require.NoError(t, err)
	require.Len(t, outcome.Documents, len(published), "the outcome must describe the run's whole document set, not this call's delta")
	for _, doc := range outcome.Documents {
		require.NotEmpty(t, doc.DocID, "every reported document must carry the id it was published under")
		require.NotEmpty(t, doc.Link, "every reported document must carry its link")
	}

	// ── 4. Positive control ────────────────────────────────────────────
	// "Zero publications" is only evidence of the per-language gate if the
	// same call DOES publish when the language is missing. Without this
	// control the assertion above would also pass if the document phase never
	// ran at all, which is the failure mode a vacuous gate hides.
	unpublished := *sourceFinal.Result
	unpublished.Documents = nil

	const controlRunID = "run-docs-gate-control"
	require.NoError(t, repo.Create(ctx, &GenerationRun{
		ID: controlRunID, Request: sourceReq, Status: RunStatusRunning,
		CurrentStage: StageCoreReady, Result: &unpublished,
	}))

	if _, err := runner.PublishRunDocuments(ctx, DocsPublishRequest{
		RunID: controlRunID, ChildJobID: "job_docs_child", Attempt: 1,
	}); err != nil {
		t.Fatalf("positive control: PublishRunDocuments() error = %v, want nil", err)
	}
	require.Equal(t, 1, docPub.callCount,
		"the same call MUST publish when the language has no document yet — otherwise the zero above proved nothing")
	require.Equal(t, Language("en"), docPub.records[0].Language,
		"the control must publish the language the retry skipped")
}

// TestSetDocsPublishEnqueuer_IsNilSafe pins the wiring contract: the deferred
// leg is opt-in, and setting it on a nil Runner must not panic.
func TestSetDocsPublishEnqueuer_IsNilSafe(t *testing.T) {
	t.Parallel()

	var nilRunner *Runner
	nilRunner.SetDocsPublishEnqueuer(nil)

	r, _ := docsPublishRunner(t)
	r.SetDocsPublishEnqueuer(nil)
	if r.docsPublishEnqueuer != nil {
		t.Fatal("docsPublishEnqueuer should stay nil when nil is wired (the deferred leg stays off)")
	}
}
