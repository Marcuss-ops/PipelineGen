package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// recordingParentNotifier captures the children reported to the parent
// completion port, mirroring how the composition root's adapter receives them.
type recordingParentNotifier struct {
	mu       sync.Mutex
	children []*job.Job
	err      error
}

func (n *recordingParentNotifier) NotifyChildTerminal(_ context.Context, child *job.Job) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.children = append(n.children, child)
	return n.err
}

func (n *recordingParentNotifier) reported() []*job.Job {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]*job.Job, len(n.children))
	copy(out, n.children)
	return out
}

func newParentNotifyWorker(t *testing.T, notifier ParentCompletionNotifier) *Worker {
	t.Helper()
	w := NewWorker(WorkerDeps{
		ID:        "test-worker-" + t.Name(),
		Log:       zaptest.NewLogger(t),
		LeaseTTL:  time.Minute,
		PollEvery: time.Second,
		Backoff:   BackoffConfig{},
	})
	if notifier != nil {
		w = w.WithParentCompletionNotifier(notifier)
	}
	return w
}

// TestNotifyParentCompletionReportsTerminalChild pins the healthy path: a child
// that carries a parent link is reported so the parent can flip immediately
// instead of waiting for the recovery sweep.
func TestNotifyParentCompletionReportsTerminalChild(t *testing.T) {
	notifier := &recordingParentNotifier{}
	w := newParentNotifyWorker(t, notifier)

	w.notifyParentCompletion(context.Background(), &job.Job{
		ID: "settle-child", Type: "clip.render", ParentJobID: "parent-1",
	})

	reported := notifier.reported()
	if len(reported) != 1 {
		t.Fatalf("reported children = %d, want 1", len(reported))
	}
	if reported[0].ID != "settle-child" || reported[0].ParentJobID != "parent-1" {
		t.Fatalf("reported child = %+v, want id=settle-child parent=parent-1", reported[0])
	}
}

// TestNotifyParentCompletionResolvesLinkFromPayload pins the restart/remote
// path: the parent link travels in the payload, so a child row without the
// column still notifies.
func TestNotifyParentCompletionResolvesLinkFromPayload(t *testing.T) {
	notifier := &recordingParentNotifier{}
	w := newParentNotifyWorker(t, notifier)

	payload, err := json.Marshal(map[string]any{"render_phase": "settle", "parent_job_id": "parent-9"})
	if err != nil {
		t.Fatal(err)
	}
	w.notifyParentCompletion(context.Background(), &job.Job{
		ID: "settle-child", Type: "clip.render", Payload: payload,
	})

	reported := notifier.reported()
	if len(reported) != 1 || reported[0].ParentJobID != "parent-9" {
		t.Fatalf("reported = %+v, want one child addressed to parent-9", reported)
	}
}

// TestNotifyParentCompletionSkipsUnlinkedChild pins that a root job (or one
// whose link was lost) causes no work at all.
func TestNotifyParentCompletionSkipsUnlinkedChild(t *testing.T) {
	notifier := &recordingParentNotifier{}
	w := newParentNotifyWorker(t, notifier)

	w.notifyParentCompletion(context.Background(), &job.Job{ID: "root-job", Type: "clip.render"})
	w.notifyParentCompletion(context.Background(), nil)

	if got := len(notifier.reported()); got != 0 {
		t.Fatalf("reported children = %d, want 0 for unlinked/nil children", got)
	}
}

// TestNotifyParentCompletionIsBestEffort pins the durability contract: the
// child's terminal state is already committed when this runs, so a notifier
// failure is logged and swallowed — it must never fail the completed job. The
// recovery sweep remains the net.
func TestNotifyParentCompletionIsBestEffort(t *testing.T) {
	notifier := &recordingParentNotifier{err: errors.New("aggregator unavailable")}
	w := newParentNotifyWorker(t, notifier)

	// No panic, no returned error: the method is void by design.
	w.notifyParentCompletion(context.Background(), &job.Job{
		ID: "settle-child", Type: "clip.render", ParentJobID: "parent-1",
	})
	if got := len(notifier.reported()); got != 1 {
		t.Fatalf("notifier calls = %d, want 1 (the attempt must still be made)", got)
	}
}

// TestNotifyParentCompletionWithoutNotifierIsNoop pins nil-tolerance so a
// deployment that wires no notifier keeps the polling-only behaviour.
func TestNotifyParentCompletionWithoutNotifierIsNoop(t *testing.T) {
	w := newParentNotifyWorker(t, nil)
	w.notifyParentCompletion(context.Background(), &job.Job{
		ID: "settle-child", Type: "clip.render", ParentJobID: "parent-1",
	})
	var nilWorker *Worker
	nilWorker.notifyParentCompletion(context.Background(), &job.Job{ID: "x", ParentJobID: "y"})
}

// recordingStore embeds the store interface so only the methods the legacy
// completion path actually calls have to be implemented.
type recordingStore struct {
	job.Store
	mu        sync.Mutex
	completed []string
}

func (s *recordingStore) Complete(_ context.Context, id, _, _ string, _ int, _ json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, id)
	return nil
}

// TestFinalizeLegacyCompleteNotifiesParent pins the wiring point that matters
// most for clip.render: clip.render completes through the legacy path (its
// registry policy is FinalizationStrategyLegacyComplete), so the notification
// must fire from there — not only from the artifact-producing path.
func TestFinalizeLegacyCompleteNotifiesParent(t *testing.T) {
	store := &recordingStore{}
	notifier := &recordingParentNotifier{}
	w := newParentNotifyWorker(t, notifier)
	w.repo = store

	w.finalizeJobLegacyComplete(context.Background(),
		&job.Job{ID: "settle-child", Type: "clip.render", ParentJobID: "parent-1"},
		w.id, "lease-1", 1, map[string]any{"ok": true})

	store.mu.Lock()
	completed := append([]string(nil), store.completed...)
	store.mu.Unlock()
	if len(completed) != 1 || completed[0] != "settle-child" {
		t.Fatalf("completed jobs = %v, want [settle-child]", completed)
	}
	reported := notifier.reported()
	if len(reported) != 1 || reported[0].ParentJobID != "parent-1" {
		t.Fatalf("reported = %+v, want the completed child reported", reported)
	}
}

// TestFinalizeLegacyCompleteDoesNotNotifyOnLeaseLoss pins that a child which did
// NOT commit terminal never reports a parent: notifying on a failed write would
// let the aggregator read a still-running child and (correctly) refuse, but the
// point is that the notification is tied to the commit.
func TestFinalizeLegacyCompleteDoesNotNotifyOnLeaseLoss(t *testing.T) {
	notifier := &recordingParentNotifier{}
	w := newParentNotifyWorker(t, notifier)
	w.repo = &leaseLostStore{}

	w.finalizeJobLegacyComplete(context.Background(),
		&job.Job{ID: "settle-child", Type: "clip.render", ParentJobID: "parent-1"},
		w.id, "lease-1", 1, map[string]any{"ok": true})

	if got := len(notifier.reported()); got != 0 {
		t.Fatalf("reported children = %d, want 0 when the terminal commit failed", got)
	}
}

type leaseLostStore struct {
	job.Store
}

func (s *leaseLostStore) Complete(context.Context, string, string, string, int, json.RawMessage) error {
	return job.ErrLeaseLost
}

// TestFinalizeArtifactPathNotifiesParent pins the same contract on the
// artifact-producing path (the CompletionPort branch).
func TestFinalizeArtifactPathNotifiesParent(t *testing.T) {
	notifier := &recordingParentNotifier{}
	broker := &flakyTransientBroker{}
	w := newParentNotifyWorker(t, notifier).WithBroker(broker)

	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "wf-parent-notify",
		JobID:         "clip_1:rendered",
		Artifacts: []job.Artifact{{
			ID:       "clip_1:rendered",
			Kind:     job.ArtifactKindScriptJSON,
			Path:     "/tmp/clip_1/rendered.mp4",
			Filename: "rendered.mp4",
			MIMEType: "video/mp4",
			Required: true,
		}},
	}
	result := map[string]any{job.ManifestKey: manifestToRawJSON(t, manifest)}

	w.finalizeJobArtifactPath(context.Background(),
		&job.Job{ID: "clip_1", Type: "clip.render", ParentJobID: "parent-7"},
		w.id, "lease-1", 1, result)

	reported := notifier.reported()
	if len(reported) != 1 || reported[0].ParentJobID != "parent-7" {
		t.Fatalf("reported = %+v, want the artifact-completed child reported", reported)
	}
}
