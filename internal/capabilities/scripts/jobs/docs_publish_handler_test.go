package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// recordingDocsRunner is the hermetic stand-in for the durable runner. It
// records every run it was asked to publish and returns a programmed error, so
// the tests can assert BOTH that the work happened and that it did not.
type recordingDocsRunner struct {
	calls    []string
	requests []DocsPublishRequest
	outcome  scriptgen.DocsPublishOutcome
	err      error
	panics   bool
}

func (r *recordingDocsRunner) PublishRunDocuments(_ context.Context, req DocsPublishRequest) (scriptgen.DocsPublishOutcome, error) {
	if r.panics {
		panic("docs runner must not be called on this path")
	}
	r.calls = append(r.calls, req.RunID)
	r.requests = append(r.requests, req)
	return r.outcome, r.err
}

func docsChildJob(t *testing.T, payload any) *job.Job {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &job.Job{ID: "job_docs_child", Type: domainScript.TypeDocsPublish, Payload: raw}
}

// TestDocsPublishJobHandler_PublishesTheNamedRun pins the success contract: the
// child resolves the DURABLE reference it was given, and reports a result the
// parent aggregator can read as an unambiguous success.
func TestDocsPublishJobHandler_PublishesTheNamedRun(t *testing.T) {
	t.Parallel()

	runner := &recordingDocsRunner{}
	h := NewDocsPublishJobHandler(runner, nil)

	res, err := h.HandleJob(context.Background(), docsChildJob(t, DocsChildPayload{
		ParentJobID: "job_parent",
		RunID:       "run_42",
	}), nil)
	if err != nil {
		t.Fatalf("HandleJob() error = %v, want nil", err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "run_42" {
		t.Fatalf("runner calls = %v, want exactly [run_42]", runner.calls)
	}
	// The child's OWN identity must reach the publish path: the execution
	// ledger attributes the work to the child that performed it, not to the
	// parent whose wall clock is already closed.
	if got := runner.requests[0].ChildJobID; got != "job_docs_child" {
		t.Fatalf("request child_job_id = %q, want job_docs_child", got)
	}
	if got := runner.requests[0].Attempt; got != 1 {
		t.Fatalf("request attempt = %d, want 1", got)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("result[ok] = %v, want true — the aggregator reads this field to decide the parent terminal", res["ok"])
	}
	if got := res["parent_state"]; got != string(ScriptParentSucceeded) {
		t.Fatalf("result[parent_state] = %v, want %q", got, ScriptParentSucceeded)
	}
	if got := res["run_id"]; got != "run_42" {
		t.Fatalf("result[run_id] = %v, want run_42", got)
	}
	if got := res["parent_job_id"]; got != "job_parent" {
		t.Fatalf("result[parent_job_id] = %v, want job_parent", got)
	}
	if got := res["status"]; got != string(job.StatusSucceeded) {
		t.Fatalf("result[status] = %v, want %q", got, job.StatusSucceeded)
	}
}

// TestDocsPublishJobHandler_NeverPublishesWithoutARunReference is the load
// bearing invariant of the child: a payload with no run reference must fail
// TYPED and must not reach the publish path at all. If it published
// "best-effort" the child would report SUCCEEDED while publishing nothing, and
// the aggregator would flip the parent to COMPLETED on that empty success.
func TestDocsPublishJobHandler_NeverPublishesWithoutARunReference(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload any
	}{
		{"run id absent", DocsChildPayload{ParentJobID: "job_parent"}},
		{"run id blank", DocsChildPayload{ParentJobID: "job_parent", RunID: "   "}},
		{"empty object", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// panics=true makes any call into the port a loud test failure, so
			// "did not publish" is proven rather than inferred from the error.
			runner := &recordingDocsRunner{panics: true}
			h := NewDocsPublishJobHandler(runner, nil)

			_, err := h.HandleJob(context.Background(), docsChildJob(t, tc.payload), nil)
			if !errors.Is(err, ErrDocsPublishMalformedPayload) {
				t.Fatalf("HandleJob() error = %v, want ErrDocsPublishMalformedPayload", err)
			}
		})
	}
}

// TestDocsPublishJobHandler_UndecodablePayloadIsTyped pins the same invariant
// for a payload that is not JSON at all.
func TestDocsPublishJobHandler_UndecodablePayloadIsTyped(t *testing.T) {
	t.Parallel()

	runner := &recordingDocsRunner{panics: true}
	h := NewDocsPublishJobHandler(runner, nil)

	_, err := h.HandleJob(context.Background(), &job.Job{
		ID:      "job_docs_child",
		Type:    domainScript.TypeDocsPublish,
		Payload: json.RawMessage(`{"run_id":`),
	}, nil)
	if !errors.Is(err, ErrDocsPublishMalformedPayload) {
		t.Fatalf("HandleJob() error = %v, want ErrDocsPublishMalformedPayload", err)
	}
}

// TestDocsPublishJobHandler_PropagatesTheRunnerFailure pins that a publish
// failure is reported as a failure and that the implementation's typed wrap
// survives, so an operator can classify it without string matching.
func TestDocsPublishJobHandler_PropagatesTheRunnerFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("drive 503")
	runner := &recordingDocsRunner{err: fmt.Errorf("publish documents: %w", sentinel)}
	h := NewDocsPublishJobHandler(runner, nil)

	res, err := h.HandleJob(context.Background(), docsChildJob(t, DocsChildPayload{
		ParentJobID: "job_parent", RunID: "run_42",
	}), nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("HandleJob() error = %v, want it to wrap %v", err, sentinel)
	}
	if res != nil {
		t.Fatalf("HandleJob() result = %v, want nil on failure (a failed child must not look successful)", res)
	}
}

// TestDocsPublishJobHandler_RunNotFoundStaysClassifiable pins that the missing
// snapshot sentinel is preserved end to end: a run that is gone is a
// deterministic failure, and the retry budget must not be spent on it silently.
func TestDocsPublishJobHandler_RunNotFoundStaysClassifiable(t *testing.T) {
	t.Parallel()

	runner := &recordingDocsRunner{
		err: fmt.Errorf("load run run_42: %w", ErrDocsPublishRunNotFound),
	}
	h := NewDocsPublishJobHandler(runner, nil)

	_, err := h.HandleJob(context.Background(), docsChildJob(t, DocsChildPayload{
		ParentJobID: "job_parent", RunID: "run_42",
	}), nil)
	if !errors.Is(err, ErrDocsPublishRunNotFound) {
		t.Fatalf("HandleJob() error = %v, want ErrDocsPublishRunNotFound", err)
	}
}

// TestDocsPublishRunnerFuncAdapter pins the composition-root seam: a plain
// function bridges to the port, and a nil function yields a nil port rather
// than a port that panics at call time.
func TestDocsPublishRunnerFuncAdapter(t *testing.T) {
	t.Parallel()

	if got := NewDocsPublishRunnerFunc(nil); got != nil {
		t.Fatalf("NewDocsPublishRunnerFunc(nil) = %v, want nil", got)
	}

	var seen DocsPublishRequest
	port := NewDocsPublishRunnerFunc(func(_ context.Context, req DocsPublishRequest) (scriptgen.DocsPublishOutcome, error) {
		seen = req
		return scriptgen.DocsPublishOutcome{}, nil
	})
	want := DocsPublishRequest{RunID: "run_7", ChildJobID: "job_child_7", Attempt: 2}
	if _, err := port.PublishRunDocuments(context.Background(), want); err != nil {
		t.Fatalf("PublishRunDocuments() error = %v, want nil", err)
	}
	if seen != want {
		t.Fatalf("adapter forwarded %+v, want %+v", seen, want)
	}
}

// TestNewDocsPublishJobHandler_PanicsWithoutRunner pins the fail-fast
// constructor: a handler that cannot publish would let the parent defer its
// terminal flip to a child that can never complete it, which is a hung run
// rather than a failed one.
func TestNewDocsPublishJobHandler_PanicsWithoutRunner(t *testing.T) {
	t.Parallel()

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("NewDocsPublishJobHandler(nil, nil) did not panic")
		}
	}()
	_ = NewDocsPublishJobHandler(nil, nil)
}

// TestDocsPublishJobHandler_RegisterRequiresService pins the boot-time
// fail-closed registration, mirroring the sibling child handler.
func TestDocsPublishJobHandler_RegisterRequiresService(t *testing.T) {
	t.Parallel()

	h := NewDocsPublishJobHandler(&recordingDocsRunner{}, nil)
	if err := h.Register(nil); !errors.Is(err, appjobs.ErrMissingDeps) {
		t.Fatalf("Register(nil) error = %v, want ErrMissingDeps", err)
	}
}

// TestDocsPublishJobHandler_ResultIsReadableByTheParentAggregator is the
// integration gate between this child and the aggregator that is ALREADY wired
// in production (app/wiring/lifecycle_worker.go).
//
// The aggregator does not merely read `ok`: it surfaces document references by
// decoding each child result into ScriptChildResult and keying DocLink/DocID by
// ItemID. A child that reported only success would finalize the parent
// correctly and still lose every document link from the operator-facing
// result — the publication would be durable but unreadable. This decodes the
// handler's own output through the aggregator's own type and fails if a field
// the aggregator reads is missing.
func TestDocsPublishJobHandler_ResultIsReadableByTheParentAggregator(t *testing.T) {
	t.Parallel()

	runner := &recordingDocsRunner{
		outcome: scriptgen.DocsPublishOutcome{Documents: []scriptgen.PublishedDocument{
			{Language: "en", DocID: "doc_en", Link: "https://docs.google.com/document/d/doc_en/edit"},
			{Language: "it", DocID: "doc_it", Link: "https://docs.google.com/document/d/doc_it/edit"},
		}},
	}
	h := NewDocsPublishJobHandler(runner, nil)

	res, err := h.HandleJob(context.Background(), docsChildJob(t, DocsChildPayload{
		ParentJobID: "job_parent", RunID: "run_42",
	}), nil)
	if err != nil {
		t.Fatalf("HandleJob() error = %v, want nil", err)
	}

	// Round-trip exactly the way the aggregator does: JSON through its own DTO.
	raw, marshalErr := json.Marshal(res)
	if marshalErr != nil {
		t.Fatalf("marshal child result: %v", marshalErr)
	}
	var child ScriptChildResult
	if unmarshalErr := json.Unmarshal(raw, &child); unmarshalErr != nil {
		t.Fatalf("the aggregator must be able to decode this result: %v", unmarshalErr)
	}

	if child.OK == nil || !*child.OK {
		t.Fatalf("aggregator sees ok=%v, want true — a nil or false ok finalizes the parent as failed", child.OK)
	}
	if child.ItemID != "run_42" {
		t.Fatalf("aggregator keys the doc maps by item_id, got %q, want run_42", child.ItemID)
	}
	if child.DocID == "" || child.DocLink == "" {
		t.Fatalf("aggregator would surface no document link (doc_id=%q doc_link=%q)", child.DocID, child.DocLink)
	}
	// The scalar doc_link is a headline for the first language in
	// deterministic order; the complete set must still be present, so a
	// multi-language run is never silently reduced to one document.
	docs, ok := res["documents"].([]map[string]any)
	if !ok || len(docs) != 2 {
		t.Fatalf("result[documents] = %v, want the complete 2-language set", res["documents"])
	}
}
