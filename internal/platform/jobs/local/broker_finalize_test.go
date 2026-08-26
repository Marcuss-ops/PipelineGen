// Package local — broker_finalize_test.go: unit tests for the overlay
// parent-video folder resolution seam (RenderingGen overlay → /video/.../overlay/).
package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// stubArtifactFolderResolver is a deterministic ArtifactFolderResolver stub.
type stubArtifactFolderResolver struct {
	folderID string
	err      error
	calls    int
	gotID    string
}

func (s *stubArtifactFolderResolver) ResolveArtifactFolder(_ context.Context, parentVideoID string) (string, error) {
	s.calls++
	s.gotID = parentVideoID
	return s.folderID, s.err
}

var _ finalization.ArtifactFolderResolver = (*stubArtifactFolderResolver)(nil)

func TestResolveOverlayParentFolder_ResolvesWhenWired(t *testing.T) {
	stub := &stubArtifactFolderResolver{folderID: "video-folder-847"}
	folderID, ok, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, stub)
	if err != nil {
		t.Fatalf("resolveOverlayParentFolder: %v", err)
	}
	if !ok || folderID != "video-folder-847" {
		t.Fatalf("resolved = (%q, %v), want (video-folder-847, true)", folderID, ok)
	}
	if stub.gotID != "video-847" {
		t.Fatalf("resolver received video_id %q, want video-847", stub.gotID)
	}
}

func TestResolveOverlayParentFolder_NoOpWithoutResolver(t *testing.T) {
	if _, ok, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, nil); err != nil || ok {
		t.Fatalf("nil resolver should be a no-op, got ok=%v err=%v", ok, err)
	}
}

func TestResolveOverlayParentFolder_NoOpWithoutVideoID(t *testing.T) {
	stub := &stubArtifactFolderResolver{folderID: "video-folder-847"}
	for _, meta := range []map[string]any{nil, {}, {"video_id": "  "}} {
		if _, ok, err := resolveOverlayParentFolder(context.Background(), meta, stub); err != nil || ok {
			t.Fatalf("meta %#v should be a no-op, got ok=%v err=%v", meta, ok, err)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("resolver called %d times, want 0 (no video_id present)", stub.calls)
	}
}

func TestResolveOverlayParentFolder_EmptyFolderIsNotResolved(t *testing.T) {
	stub := &stubArtifactFolderResolver{folderID: ""}
	if _, ok, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, stub); err != nil || ok {
		t.Fatalf("empty folder should mean not-resolved, got ok=%v err=%v", ok, err)
	}
}

func TestResolveOverlayParentFolder_PropagatesResolverError(t *testing.T) {
	want := errors.New("folder lookup failed")
	stub := &stubArtifactFolderResolver{err: want}
	if _, _, err := resolveOverlayParentFolder(context.Background(), map[string]any{"video_id": "video-847"}, stub); !errors.Is(err, want) {
		t.Fatalf("expected resolver error to propagate, got %v", err)
	}
}

// stubJobStore embeds the full job.Store interface and overrides only the
// methods the broker's CompleteWithArtifacts path touches (Get), keeping the
// test hermetic without scaffolding 20+ methods.
type stubJobStore struct {
	job.Store
	job *job.Job
}

func (s *stubJobStore) Get(_ context.Context, id string) (*job.Job, error) {
	if s.job != nil && s.job.ID == id {
		return s.job, nil
	}
	return nil, nil
}

var _ job.Store = (*stubJobStore)(nil)

// stubJobFinalizer is a deterministic finalization.JobFinalizer stub.
// before, when set, runs at the top of CompleteWithArtifacts so tests can
// observe the publisher state at the exact moment the single SQLite TX
// commits; gotIDs captures the artifact order the finalizer received.
type stubJobFinalizer struct {
	calls  int
	refs   []finalization.ArtifactRef
	before func()
	gotIDs []string
}

func (f *stubJobFinalizer) CompleteWithArtifacts(_ context.Context, req finalization.FinalizationRequest) (*finalization.FinalizationResult, error) {
	if f.before != nil {
		f.before()
	}
	f.calls++
	f.gotIDs = make([]string, len(req.Artifacts))
	for i, a := range req.Artifacts {
		f.gotIDs[i] = a.ArtifactID
	}
	return &finalization.FinalizationResult{
		JobID:        req.Result.JobID,
		Status:       "SUCCEEDED",
		ArtifactRefs: f.refs,
	}, nil
}

var _ finalization.JobFinalizer = (*stubJobFinalizer)(nil)

// stubPublisherPort is a deterministic finalization.PublisherPort stub for
// the broker test (mirrors the stub in the finalizer package).
type stubPublisherPort struct {
	location finalization.AssetLocation
	err      error
}

func (s *stubPublisherPort) Publish(_ context.Context, _ finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	return s.location, s.err
}

var _ finalization.PublisherPort = (*stubPublisherPort)(nil)

// TestBroker_CompleteWithArtifacts_RecordsFinalizeOperations pins the full
// post_writer_finalize metric split end-to-end through the in-process
// broker: with a run bound to ctx, one staged artifact yields the four
// operations finalize.artifact_prepare / artifact_hash / drive_publish /
// completion_tx, all attributed to the post_writer_finalize stage, so the
// RunReport no longer hides the sequential Drive I/O inside a black box.
func TestBroker_CompleteWithArtifacts_RecordsFinalizeOperations(t *testing.T) {
	content := []byte("broker finalize bytes")
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])

	stagedBytes, err := json.Marshal(remote.StagedArtifacts{{
		ArtifactID:  "job-finalize-1:script_json",
		Destination: "script",
		Path:        path,
		Filename:    "script.json",
		MIMEType:    "application/json",
		SizeBytes:   int64(len(content)),
		SHA256:      sha,
		Required:    true,
		DriveGroup:  "job-finalize-1",
	}})
	if err != nil {
		t.Fatal(err)
	}

	b := &Broker{
		jobs: &stubJobStore{job: &job.Job{
			ID: "job-finalize-1", WorkerID: "w-1", LeaseID: "lease-1",
			Revision: 3, RetryCount: 0,
		}},
		finalizer:   &stubJobFinalizer{},
		preparation: assetfinalizer.NewArtifactPreparation(&stubPublisherPort{location: finalization.AssetLocation{Provider: "drive", FileID: "drive-file-1", Action: finalization.PublishCreated}}, nil),
		log:         zap.NewNop(),
	}

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{AttemptID: "attempt-broker-1"})
	defer run.Finish()
	ctx := kernobs.WithRun(context.Background(), run)

	if _, err := b.CompleteWithArtifacts(ctx, appjobs.CompleteWithArtifactsCommand{
		WorkerID:         "w-1",
		LeaseID:          "lease-1",
		JobID:            "job-finalize-1",
		ExpectedRevision: 3,
		StagedArtifacts:  stagedBytes,
	}); err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}

	got := make(map[string]kernobs.OperationReport)
	for _, op := range run.Report().Operations {
		if op.Component == "finalize" {
			got[op.Operation] = op
		}
	}
	for _, want := range []string{"artifact_prepare", "artifact_hash", "drive_publish", "completion_tx"} {
		op, ok := got[want]
		if !ok {
			t.Errorf("missing finalize.%s operation in RunReport", want)
			continue
		}
		if op.Stage != "post_writer_finalize" {
			t.Errorf("finalize.%s stage = %q, want post_writer_finalize", want, op.Stage)
		}
		if op.Status != kernobs.StageStatusCompleted {
			t.Errorf("finalize.%s status = %q, want completed", want, op.Status)
		}
	}
	if tx := got["completion_tx"]; tx.Items != 1 {
		t.Errorf("finalize.completion_tx items = %d, want 1 published artifact", tx.Items)
	}
}

// ── Bounded-parallel publication (P0: post_writer_finalize) ──────────

// blockingPublisher records publish concurrency and blocks every Publish on
// a gate channel. It signals (once) the first time the in-flight count
// reaches finalizePublishConcurrency, so the test can prove the bound is
// both respected (never exceeded) and actually reached (real parallelism,
// not a disguised sequential loop).
type blockingPublisher struct {
	mu           sync.Mutex
	active       int
	maxActive    int
	calls        int
	gate         chan struct{}
	boundOnce    sync.Once
	boundReached chan struct{}
}

func newBlockingPublisher() *blockingPublisher {
	return &blockingPublisher{
		gate:         make(chan struct{}),
		boundReached: make(chan struct{}),
	}
}

func (p *blockingPublisher) Publish(_ context.Context, va finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	p.mu.Lock()
	p.active++
	p.calls++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	reached := p.active >= finalizePublishConcurrency
	p.mu.Unlock()
	if reached {
		p.boundOnce.Do(func() { close(p.boundReached) })
	}

	<-p.gate

	p.mu.Lock()
	p.active--
	p.mu.Unlock()

	return finalization.AssetLocation{Provider: "drive", FileID: "f-" + va.ArtifactID, Action: finalization.PublishCreated}, nil
}

func (p *blockingPublisher) maxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

// snapshot returns (calls, active) so the finalizer hook can assert the TX
// runs strictly after every publication finished.
func (p *blockingPublisher) snapshot() (calls, active int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.active
}

// failPublisher succeeds for every Publish except the failOn-th call
// (1-based), which fails deterministically. Gate-free: calls return
// immediately, so the errgroup's fail-fast behavior is what stops the
// remaining artifact scheduling.
type failPublisher struct {
	mu     sync.Mutex
	calls  int
	failOn int
}

func (p *failPublisher) Publish(_ context.Context, va finalization.VerifiedArtifact) (finalization.AssetLocation, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == p.failOn {
		return finalization.AssetLocation{}, fmt.Errorf("drive publish failed for %s", va.ArtifactID)
	}
	return finalization.AssetLocation{Provider: "drive", FileID: "f-" + va.ArtifactID, Action: finalization.PublishCreated}, nil
}

func (p *failPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// stagedArtifactRefs writes n distinct on-disk artifacts and returns them as
// canonical StagedArtifactReference pointers plus the input-order IDs.
func stagedArtifactRefs(t *testing.T, n int) (remote.StagedArtifacts, []string) {
	t.Helper()
	dir := t.TempDir()
	refs := make(remote.StagedArtifacts, 0, n)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("job-conc-%d", i)
		content := []byte(fmt.Sprintf("artifact payload for %s", id))
		path := filepath.Join(dir, id+".json")
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		refs = append(refs, &remote.StagedArtifactReference{
			ArtifactID:  id,
			Destination: "script",
			Path:        path,
			Filename:    id + ".json",
			MIMEType:    "application/json",
			SizeBytes:   int64(len(content)),
			SHA256:      hex.EncodeToString(sum[:]),
			Required:    true,
			DriveGroup:  "job-conc",
		})
		ids = append(ids, id)
	}
	return refs, ids
}

// brokerForFinalize wires a Broker with the deterministic job store, the
// given preparation spine and finalizer stub, all sharing one job identity.
func brokerForFinalize(prep finalization.ArtifactPreparationService, fin *stubJobFinalizer) *Broker {
	return &Broker{
		jobs: &stubJobStore{job: &job.Job{
			ID: "job-conc-1", WorkerID: "w-1", LeaseID: "lease-1",
			Revision: 3, RetryCount: 0,
		}},
		finalizer:   fin,
		preparation: prep,
		log:         zap.NewNop(),
	}
}

// TestBroker_CompleteWithArtifacts_BoundedParallelism pins the P0
// post_writer_finalize optimization: with 8 staged artifacts and a
// blocking publisher, the bound (4 workers) is reached — proving real
// parallelism — never exceeded, the published slice keeps the manifest
// order, and the finalizer's single TX runs strictly AFTER all 8
// publications complete (atomic contract preserved).
func TestBroker_CompleteWithArtifacts_BoundedParallelism(t *testing.T) {
	const n = 8
	refs, ids := stagedArtifactRefs(t, n)
	stagedBytes, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}

	pub := newBlockingPublisher()
	fin := &stubJobFinalizer{}
	b := brokerForFinalize(assetfinalizer.NewArtifactPreparation(pub, nil), fin)

	// Assert atomicity from inside the finalizer: at TX time every
	// publication must already have completed.
	fin.before = func() {
		calls, active := pub.snapshot()
		if calls != n {
			t.Errorf("finalizer TX ran with %d publications done, want %d (TX must run after ALL publishes)", calls, n)
		}
		if active != 0 {
			t.Errorf("finalizer TX ran while %d publishes still in flight", active)
		}
	}

	obs := kernobs.NewRunObserver(nil)
	run := obs.StartRun(context.Background(), kernobs.RunInfo{AttemptID: "attempt-conc-1"})
	defer run.Finish()
	ctx := kernobs.WithRun(context.Background(), run)

	resultCh := make(chan error, 1)
	go func() {
		_, err := b.CompleteWithArtifacts(ctx, appjobs.CompleteWithArtifactsCommand{
			WorkerID:         "w-1",
			LeaseID:          "lease-1",
			JobID:            "job-conc-1",
			ExpectedRevision: 3,
			StagedArtifacts:  stagedBytes,
		})
		resultCh <- err
	}()

	// Prove the bound is actually reached: 4 workers must be publishing
	// simultaneously before any completes (the gate is still closed).
	select {
	case <-pub.boundReached:
	case <-time.After(10 * time.Second):
		t.Fatal("bound not reached: fewer than finalizePublishConcurrency concurrent publishes")
	}
	close(pub.gate)

	if err := <-resultCh; err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}

	if max := pub.maxConcurrent(); max != finalizePublishConcurrency {
		t.Errorf("max concurrent publishes = %d, want exactly %d (bound respected)", max, finalizePublishConcurrency)
	}
	if fin.calls != 1 {
		t.Errorf("finalizer called %d times, want exactly 1 TX", fin.calls)
	}
	if len(fin.gotIDs) != n {
		t.Fatalf("finalizer received %d artifacts, want %d", len(fin.gotIDs), n)
	}
	for i, want := range ids {
		if fin.gotIDs[i] != want {
			t.Errorf("artifact order: index %d = %q, want %q (manifest order must be preserved)", i, fin.gotIDs[i], want)
		}
	}
}

// TestBroker_CompleteWithArtifacts_FailsClosedOnPublishError pins the
// fail-closed contract under concurrency: the first publish failure fails
// the whole finalize, the terminal TX never runs (no partial success), and
// the failure propagates to the caller.
func TestBroker_CompleteWithArtifacts_FailsClosedOnPublishError(t *testing.T) {
	refs, _ := stagedArtifactRefs(t, 8)
	stagedBytes, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}

	pub := &failPublisher{failOn: 3}
	fin := &stubJobFinalizer{}
	b := brokerForFinalize(assetfinalizer.NewArtifactPreparation(pub, nil), fin)

	_, err = b.CompleteWithArtifacts(context.Background(), appjobs.CompleteWithArtifactsCommand{
		WorkerID:         "w-1",
		LeaseID:          "lease-1",
		JobID:            "job-conc-1",
		ExpectedRevision: 3,
		StagedArtifacts:  stagedBytes,
	})
	if err == nil {
		t.Fatal("expected error when the 3rd Drive publish fails")
	}
	if !strings.Contains(err.Error(), "job-conc-") {
		t.Errorf("error %q does not identify the failed artifact", err)
	}
	if fin.calls != 0 {
		t.Errorf("finalizer ran %d times, want 0 (no TX after publish failure)", fin.calls)
	}
	if calls := pub.callCount(); calls < 3 {
		t.Errorf("publisher ran %d calls, want at least 3 (the failure must have been reached)", calls)
	}
}

// TestBroker_CompleteWithArtifacts_PreValidationFailsClosed pins the
// synchronous pre-validation phase: a malformed staged manifest (unknown
// destination on the 2nd artifact) aborts with zero Drive I/O — no publish
// ever starts.
func TestBroker_CompleteWithArtifacts_PreValidationFailsClosed(t *testing.T) {
	refs, _ := stagedArtifactRefs(t, 2)
	refs[1].Destination = "bogus_destination"
	stagedBytes, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}

	pub := &failPublisher{failOn: 999}
	fin := &stubJobFinalizer{}
	b := brokerForFinalize(assetfinalizer.NewArtifactPreparation(pub, nil), fin)

	_, err = b.CompleteWithArtifacts(context.Background(), appjobs.CompleteWithArtifactsCommand{
		WorkerID:         "w-1",
		LeaseID:          "lease-1",
		JobID:            "job-conc-1",
		ExpectedRevision: 3,
		StagedArtifacts:  stagedBytes,
	})
	if err == nil {
		t.Fatal("expected error for unsupported staged artifact destination")
	}
	if !strings.Contains(err.Error(), "unsupported staged artifact destination") {
		t.Errorf("error %q does not name the destination failure", err)
	}
	if calls := pub.callCount(); calls != 0 {
		t.Errorf("publisher ran %d calls, want 0 (pre-validation must abort before any Drive I/O)", calls)
	}
	if fin.calls != 0 {
		t.Errorf("finalizer ran %d times, want 0", fin.calls)
	}
}
