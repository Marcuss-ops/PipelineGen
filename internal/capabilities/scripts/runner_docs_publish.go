// Package scriptgeneration — runner_docs_publish.go owns the DEFERRED Docs leg
// as a callable entry point: the work the `script.docs_publish` child job
// performs (TICKET-CORE-READY-DURABLE-DAG steps 2-3).
//
// # WHY IT IS A RE-ENTRY AND NOT A SECOND IMPLEMENTATION
//
// The document phase already exists, already checkpoints per language, already
// resolves the Drive folder per (job, language) and already fails a run typed.
// The child does not need any of that re-written: what it needs is a way to run
// THAT phase against the durable snapshot instead of against a live
// in-process GenerateResult.
//
// `executionRun` is exactly that seam. It already has a resume mode — a run
// that crashed after CORE_READY re-enters at PUBLISHING_DOCUMENTS and adopts
// the checkpointed Result, which is the prerequisite landed with the boundary
// itself. This entry point builds the same wrapper the resume path builds and
// runs ONLY the documents phase. So the Docs leg keeps exactly ONE
// implementation and ONE owner, and the child job cannot drift from the inline
// path it replaced.
//
// WHAT IS DELIBERATELY NOT DONE HERE
//
//   - No terminal flip. The run is not marked COMPLETED here; the parent
//     aggregator owns the parent job's terminal state and the run is completed
//     by the post-core completion path once every required artifact —
//     including the documents this call publishes — is durable.
//   - No copy of the result. The request carries a run REFERENCE; the snapshot
//     is read from the repository.
//   - No retry policy. Failing is this function's whole error contract; the
//     broker's retry budget and the child's DeferredAfter/typed-error surface
//     decide what happens next.
package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// DocsPublishRequest is the deferred Docs leg's input: the durable reference
// plus the identity of the job performing it.
//
// It carries NO result data by construction. A copy in the payload would be a
// second version of the plan, and the two would diverge the first time a
// document is published on an earlier attempt — which is precisely the
// duplicate-Doc failure the per-language checkpoint against the SAME durable
// run exists to prevent.
type DocsPublishRequest struct {
	// RunID is the durable generation run whose CORE_READY snapshot holds the
	// plan the documents are rendered from.
	RunID string
	// ChildJobID is the `script.docs_publish` job. Execution steps and output
	// assets are recorded against it, so the work is attributed to the child
	// that performed it rather than to the parent, whose own wall clock is
	// already closed.
	ChildJobID string
	// Attempt is the child's attempt number, carried into the execution ledger
	// so a retried child never looks like a first attempt.
	Attempt int
}

// PublishedDocument is one document this leg made durable, in the shape the
// parent aggregation reads.
type PublishedDocument struct {
	// Language is the document's language. It is the key the per-language
	// checkpoint is taken under, so it is also the key an operator needs to
	// reconcile a document with the run that owns it.
	Language string `json:"language"`
	// DocID is the provider document id (Google Docs document id).
	DocID string `json:"doc_id,omitempty"`
	// Link is the operator-facing URL.
	Link string `json:"link,omitempty"`
}

// DocsPublishOutcome reports what the run's document set looks like AFTER the
// leg ran — which is not the same as what this call published, because a
// retried child legitimately finds earlier languages already durable and skips
// them. It is read from the run's checkpointed documents, so it is the complete
// set, not a delta.
type DocsPublishOutcome struct {
	// Documents is every document the run has, ordered by language so the
	// value is stable across calls (map iteration is not).
	Documents []PublishedDocument
}

// Primary reports the document the scalar aggregation fields should carry.
//
// The parent aggregator's child contract is scalar (`doc_id` / `doc_link`),
// while one run can legitimately own several language documents; this returns
// the first by language order so the field is deterministic rather than
// dependent on map iteration. The complete set is carried in Documents, which
// the child result also surfaces — the scalar is a headline, never the only
// record.
func (o DocsPublishOutcome) Primary() (PublishedDocument, bool) {
	if len(o.Documents) == 0 {
		return PublishedDocument{}, false
	}
	return o.Documents[0], true
}

// PublishRunDocuments runs the document phase for a run from its durable
// snapshot.
//
// Idempotency is NOT re-implemented here. It is already the publish path's
// property: each language is filtered against the document reference
// checkpointed on the run, and the upsert is keyed by run + language, so a
// second call after a partial success publishes only what is still missing and
// never creates a second document for a language that already has one. That is
// what makes the broker's retry of this child safe.
func (r *Runner) PublishRunDocuments(ctx context.Context, req DocsPublishRequest) (DocsPublishOutcome, error) {
	if strings.TrimSpace(req.RunID) == "" {
		return DocsPublishOutcome{}, fmt.Errorf("scriptgeneration: docs publish requires a run id: %w", ErrRunNotFound)
	}
	if strings.TrimSpace(req.ChildJobID) == "" {
		// Without an identity the execution ledger cannot attribute the work,
		// and a step recorded against an empty job id is worse than no step.
		return DocsPublishOutcome{}, fmt.Errorf("scriptgeneration: docs publish requires the child job id (the ledger attributes the work to it)")
	}

	run, err := r.repo.Get(ctx, req.RunID)
	if err != nil {
		return DocsPublishOutcome{}, fmt.Errorf("scriptgeneration: load run %s for docs publication: %w", req.RunID, err)
	}
	if run == nil {
		// Deterministic, NOT transient: the same id will resolve the same way
		// on every retry, so the caller must be able to classify it.
		return DocsPublishOutcome{}, fmt.Errorf("scriptgeneration: load run %s for docs publication: %w", req.RunID, ErrRunNotFound)
	}

	// A terminal run has already published everything it was going to
	// publish. Re-entering would not duplicate a document (the per-language
	// filter prevents that) but it WOULD write execution steps and stage
	// telemetry onto a closed run, which is noise an operator cannot
	// distinguish from a real second attempt. Its documents are still reported:
	// the outcome describes the run's document set, and a retry after the run
	// completed must be able to reconcile exactly what exists.
	if run.Status == RunStatusCompleted {
		return docsPublishOutcomeFrom(run), nil
	}

	if run.Result == nil {
		// The run exists but carries no snapshot. There is nothing to render
		// documents FROM, and inventing an empty result would publish nothing
		// while reporting success — the parent aggregator would then flip the
		// parent to COMPLETED on that empty success.
		return DocsPublishOutcome{}, fmt.Errorf("scriptgeneration: run %s has no checkpointed result to publish documents from (core snapshot missing)", req.RunID)
	}

	// Same resolution the live run performs once at start: a docs.enabled run
	// with no resolvable folder must fail closed rather than publish a
	// document into an unrelated Drive tree.
	routing, resolveErr := run.Request.resolveArtifactRoutingContext(r.scriptDocsFolderID)
	if resolveErr != nil {
		return DocsPublishOutcome{}, fmt.Errorf("scriptgeneration: resolve artifact routing for run %s: %w", req.RunID, resolveErr)
	}

	exec := NewExecutionContext(req.ChildJobID, run.JobID)
	exec.Attempt = req.Attempt
	if exec.Attempt <= 0 {
		exec.Attempt = 1
	}

	e := &executionRun{
		r:     r,
		ctx:   ctx,
		runID: req.RunID,
		// The request and the result BOTH come from the durable run: the child
		// has no in-process state to continue from, by design.
		req:       run.Request,
		run:       run,
		result:    run.Result,
		routing:   routing,
		exec:      exec,
		resumeIdx: StageIndex(StagePublishingDocuments),
	}

	if !e.documents() {
		// e.documents() already classified and persisted the failure through
		// failRunWithRetry (failed_stage, error_code, next_retry_at), so the
		// message here is for the CHILD's row, not a second classification.
		return DocsPublishOutcome{}, fmt.Errorf("scriptgeneration: document publication failed for run %s", req.RunID)
	}

	// The outcome is read from the RESULT the phase just checkpointed, not from
	// what this call happened to publish: a retried child skips languages that
	// were already durable, and reporting only its own work would silently
	// shrink the document set an operator sees.
	return docsPublishOutcomeFromRunResult(e.result), nil
}

// docsPublishOutcomeFrom reads the document set off a loaded run. Returns an
// empty outcome when the run carries no snapshot.
func docsPublishOutcomeFrom(run *GenerationRun) DocsPublishOutcome {
	if run == nil {
		return DocsPublishOutcome{}
	}
	return docsPublishOutcomeFromRunResult(run.Result)
}

// docsPublishOutcomeFromRunResult projects the checkpointed document map into a
// language-ordered slice. Only references with an ID are reported: an empty
// reference is a document that was never published, and counting it would make
// the outcome claim a document that does not exist.
func docsPublishOutcomeFromRunResult(result *GenerateResult) DocsPublishOutcome {
	if result == nil || len(result.Documents) == 0 {
		return DocsPublishOutcome{}
	}
	langs := make([]string, 0, len(result.Documents))
	for lang := range result.Documents {
		langs = append(langs, string(lang))
	}
	sort.Strings(langs)
	out := DocsPublishOutcome{Documents: make([]PublishedDocument, 0, len(langs))}
	for _, lang := range langs {
		ref := result.Documents[Language(lang)]
		if strings.TrimSpace(ref.ID) == "" {
			continue
		}
		out.Documents = append(out.Documents, PublishedDocument{
			Language: lang,
			DocID:    ref.ID,
			Link:     ref.Link,
		})
	}
	return out
}
