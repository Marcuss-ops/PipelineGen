// Package jobs — docs_publish_handler.go: the `script.docs_publish` child
// handler, i.e. the deferred post-core Docs leg of the durable DAG
// (TICKET-CORE-READY-DURABLE-DAG step 2).
//
// # WHY THIS JOB EXISTS
//
// `Worker.runJob` is `Dispatch(jobCtx)` and then `finalizeJob(finalizationCtx)`.
// Google Docs publication used to happen INSIDE Dispatch, at the very end of
// the run's own wall clock, and the artifact/Drive finalization happened AFTER
// it — so the two were strictly sequential and no ordering tweak inside the
// runner could overlap them (ticket section 3, blocker B2). The measured cost
// on the recorded reference run was 11.6 s of tail, split roughly evenly
// between the Docs leg and the worker's own finalization.
//
// Moving the Docs work out of Dispatch is what makes the overlap expressible.
// The parent publishes its own spine, records CORE_READY, enqueues ONE child of
// this type, and defers its terminal flip to the aggregator, which now owns the
// terminal state and flips it only once this child is terminal. The durable
// snapshot the parent already writes at CORE_READY is the child's input.
//
// # WHAT THE CHILD RECEIVES — AND WHAT IT DELIBERATELY DOES NOT
//
// The payload carries a run REFERENCE (run_id, plus the parent job id for the
// aggregation link). It never carries a copy of the generated result. The
// child re-reads the durable snapshot and re-enters the post-core path from it.
// That is the whole point: a copy in the payload would be a second version of
// the plan, and the two would diverge the first time a document is published on
// an earlier attempt. It is also what makes crash-recovery work — a duplicate
// Doc on retry is prevented by the per-language idempotent checkpoint the
// publish path already performs against the SAME durable run, not by the
// child's payload.
//
// # THE PORT
//
// DocsPublishRunner is the narrow Pattern-0 surface (AGENTS.md) the handler
// needs: "publish this run's documents, from its durable snapshot". The
// production implementation lives on the durable runner
// (capabilities/scripts), which already owns the document renderer, the
// document publisher, the folder resolver and the run repository — so the
// publish path keeps exactly ONE implementation and exactly ONE owner. Keeping
// the port narrow means a test injects a recording stub without building the
// renderer, the Drive client or the checkpoint resolver.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// DocsChildPayload is the typed payload of a `script.docs_publish` child.
//
// ParentJobID is the aggregation link the parent aggregator reads; RunID is the
// durable reference the handler resolves. There is deliberately no field
// carrying result data: the snapshot is READ, never shipped.
type DocsChildPayload struct {
	// ParentJobID is the script.generate job that enqueued this child. The
	// parent aggregator uses it to decide if the parent is still awaiting
	// aggregation, and the handler echoes it in its result so the aggregate
	// can be reconciled without a second read of the child row.
	ParentJobID string `json:"parent_job_id"`
	// RunID is the durable generation run whose CORE_READY snapshot holds the
	// plan the documents are rendered from.
	RunID string `json:"run_id"`
	// ParentJobType is informational: it records which parent contract
	// produced the child, so an operator reading feed_events can tell a
	// docs child apart from a batch item child. It is never used to route.
	ParentJobType string `json:"parent_job_type,omitempty"`
}

// Typed errors. Every failure path in this handler returns one of these (or
// wraps it), so a caller can classify the failure without string matching and
// the broker never has to guess whether a failure was retryable.
var (
	// ErrDocsPublishMalformedPayload marks a child whose payload cannot be
	// decoded or whose required references are absent. It is NOT retryable in
	// practice: the same payload will decode the same way, so the retry budget
	// is spent against a deterministic failure. It is raised loudly instead of
	// being defaulted, because a child with no run reference cannot be
	// "published best-effort" — it would silently publish nothing and the
	// parent would still flip to SUCCEEDED.
	ErrDocsPublishMalformedPayload = errors.New("script.docs_publish: malformed payload")
	// ErrDocsPublishRunnerUnavailable marks a composition error: the handler
	// was constructed (or registered) without the port. Fail-closed rather
	// than a nil-dereference, and distinct from a publish failure so the two
	// are never confused in a log.
	ErrDocsPublishRunnerUnavailable = errors.New("script.docs_publish: docs publish runner is not configured")
	// ErrDocsPublishRunNotFound marks a payload that names a run the durable
	// repository does not have. The child cannot re-enter the post-core path
	// without the snapshot, and inventing one would publish documents from
	// nothing.
	//
	// It is an ALIAS of the scripts capability's own sentinel rather than a
	// second definition: the capability owns the run repository and is the
	// only component that can actually decide a run is missing, so the fact is
	// declared there once and merely re-exported here for callers that
	// classify a child failure.
	ErrDocsPublishRunNotFound = scriptgen.ErrRunNotFound
)

// DocsPublishRequest is the child's identity plus the durable reference it was
// given. It carries NO result data: the implementation re-reads the snapshot.
//
// It is an ALIAS of the scripts capability's own type, not a second struct:
// the capability owns both ends (it resolves the reference and runs the
// publish), so the shape is declared once, next to the implementation that
// must honour it.
type DocsPublishRequest = scriptgen.DocsPublishRequest

// DocsPublishRunner is the narrow port that performs the deferred Docs leg.
//
// Implementations MUST be idempotent per (run, language): the child is a
// broker job and the broker retries it, so a second call after a partial
// success must publish only the languages that are still missing and MUST NOT
// create a second document for a language that already has one.
type DocsPublishRunner interface {
	// PublishRunDocuments publishes the run's documents and reports the run's
	// COMPLETE document set (not just the delta this call published, because a
	// retried child legitimately skips languages that are already durable).
	PublishRunDocuments(ctx context.Context, req DocsPublishRequest) (scriptgen.DocsPublishOutcome, error)
}

// docsPublishRunnerFunc lets a plain function satisfy the port, so the
// composition root bridges the durable runner without this package taking a
// dependency on its concrete type (port direction stays outward).
type docsPublishRunnerFunc func(context.Context, DocsPublishRequest) (scriptgen.DocsPublishOutcome, error)

func (f docsPublishRunnerFunc) PublishRunDocuments(ctx context.Context, req DocsPublishRequest) (scriptgen.DocsPublishOutcome, error) {
	return f(ctx, req)
}

// NewDocsPublishRunnerFunc adapts a plain function to DocsPublishRunner. Returns
// a nil port for a nil function so the handler's fail-closed path stays
// reachable instead of turning into a call-time panic.
func NewDocsPublishRunnerFunc(fn func(context.Context, DocsPublishRequest) (scriptgen.DocsPublishOutcome, error)) DocsPublishRunner {
	if fn == nil {
		return nil
	}
	return docsPublishRunnerFunc(fn)
}

// DocsPublishJobHandler is the canonical handler for `script.docs_publish`.
type DocsPublishJobHandler struct {
	runner DocsPublishRunner
	logger *zap.Logger
}

// NewDocsPublishJobHandler constructs the handler.
//
// runner is MANDATORY (panic on nil — fail-fast per the repo's WireUp pattern,
// matching NewScriptGenerateItemJobHandler). A handler that cannot publish is
// not a degraded handler, it is a composition bug: the parent would defer its
// terminal flip to a child that can never complete it, which is a run that
// hangs instead of a run that fails.
func NewDocsPublishJobHandler(runner DocsPublishRunner, logger *zap.Logger) *DocsPublishJobHandler {
	if runner == nil {
		panic("scripts.Jobs.NewDocsPublishJobHandler: runner is required (DocsPublishRunner port)")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &DocsPublishJobHandler{runner: runner, logger: logger}
}

// Register binds the handler to the canonical jobs.Service dispatcher for the
// docs-publish job type. Returns an error so the composition root can fail
// closed at boot, mirroring ScriptGenerateItemJobHandler.Register.
func (h *DocsPublishJobHandler) Register(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("DocsPublishJobHandler.Register: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(domainScript.TypeDocsPublish, appjobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("DocsPublishJobHandler.Register: bind %q to dispatcher: %w", domainScript.TypeDocsPublish, err)
	}
	h.logger.Info("registered script.docs_publish handler",
		zap.String("job_type", domainScript.TypeDocsPublish))
	return nil
}

// HandleJob publishes the documents of the run named by the payload.
//
// It returns (result, err) so the dispatcher marks the child FAILED or
// SUCCEEDED correctly: this handler never reports success for work it did not
// do, because the parent aggregator reads this child's terminal state to decide
// whether the parent run may flip to COMPLETED.
func (h *DocsPublishJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	eventFn := appjobs.SafeEventFn(tools)

	payload, err := decodeDocsChildPayload(j)
	if err != nil {
		// A malformed payload is a deterministic failure: report it as one
		// instead of spending the retry budget on a payload that cannot change.
		h.logger.Error("script.docs_publish: malformed payload",
			zap.String("job_id", j.ID), zap.Error(err))
		return nil, err
	}

	eventFn("docs_publish.started", "document publication started", map[string]any{
		"job_id":        j.ID,
		"run_id":        payload.RunID,
		"parent_job_id": payload.ParentJobID,
	})

	if h.runner == nil {
		return nil, ErrDocsPublishRunnerUnavailable
	}

	outcome, pubErr := h.runner.PublishRunDocuments(ctx, DocsPublishRequest{
		RunID:      payload.RunID,
		ChildJobID: j.ID,
		Attempt:    j.RetryCount + 1,
	})
	if pubErr != nil {
		// The error is returned UNCHANGED. It is classified by whatever the
		// implementation wrapped it in (errors.Is against the sentinels above),
		// so an operator reading the child's row can tell a Drive failure from
		// a missing snapshot without this handler re-labelling a failure it
		// cannot actually diagnose.
		h.logger.Error("script.docs_publish: publication failed",
			zap.String("job_id", j.ID),
			zap.String("run_id", payload.RunID),
			zap.String("parent_job_id", payload.ParentJobID),
			zap.Bool("run_not_found", errors.Is(pubErr, ErrDocsPublishRunNotFound)),
			zap.Error(pubErr))
		eventFn("docs_publish.failed", "document publication failed", map[string]any{
			"job_id": j.ID,
			"run_id": payload.RunID,
			"error":  pubErr.Error(),
		})
		return nil, pubErr
	}

	eventFn("docs_publish.completed", "document publication completed", map[string]any{
		"job_id":        j.ID,
		"run_id":        payload.RunID,
		"parent_job_id": payload.ParentJobID,
	})

	// The aggregate contract. `ok` is the field the parent aggregator reads to
	// decide between a real success and a false one, so it is emitted
	// unconditionally on the success path; parent_state mirrors the other
	// script children so the aggregator's typed ScriptChildResult decodes this
	// result without a special case.
	//
	// item_id + doc_id + doc_link are emitted because that is what the
	// aggregator actually reads to surface document references on the parent
	// (ScriptChildResult.DocLink/DocID, keyed by ItemID). A child that reported
	// only `ok` would finalize the parent correctly and still lose every
	// document link from the operator-facing result — the publication would be
	// durable but unreadable.
	scalarDocID, scalarDocLink := "", ""
	if primary, ok := outcome.Primary(); ok {
		scalarDocID, scalarDocLink = primary.DocID, primary.Link
	}
	documents := make([]map[string]any, 0, len(outcome.Documents))
	for _, doc := range outcome.Documents {
		documents = append(documents, map[string]any{
			"language": doc.Language,
			"doc_id":   doc.DocID,
			"link":     doc.Link,
		})
	}

	return map[string]any{
		"ok":                 true,
		"status":             string(job.StatusSucceeded),
		"kind":               "docs_publish",
		"job_id":             j.ID,
		"parent_job_id":      payload.ParentJobID,
		"run_id":             payload.RunID,
		"parent_state":       string(ScriptParentSucceeded),
		"terminal_artifacts": "documents",
		// The aggregation key: one docs child aggregates under the run it
		// published, so the parent's child_doc_links map is keyed by the run —
		// which is also the only key both sides can agree on, since the child
		// never sees the parent's item id.
		"item_id":   payload.RunID,
		"doc_id":    scalarDocID,
		"doc_link":  scalarDocLink,
		"documents": documents,
	}, nil
}

// decodeDocsChildPayload decodes and validates the child payload.
//
// Validation is deliberately strict and happens BEFORE any work: a child
// without a run reference must never reach the publish path, because the
// publish path would have nothing to render from and the parent would still
// see a SUCCEEDED child.
func decodeDocsChildPayload(j *appjobs.Job) (DocsChildPayload, error) {
	var payload DocsChildPayload
	if j == nil {
		return payload, fmt.Errorf("%w: job is nil", ErrDocsPublishMalformedPayload)
	}
	if err := json.Unmarshal(j.Payload, &payload); err != nil {
		return payload, fmt.Errorf("%w: decode: %v", ErrDocsPublishMalformedPayload, err)
	}
	if strings.TrimSpace(payload.RunID) == "" {
		return payload, fmt.Errorf("%w: payload carries no run_id (the child must reference the durable snapshot, never a copy of the result)", ErrDocsPublishMalformedPayload)
	}
	return payload, nil
}
