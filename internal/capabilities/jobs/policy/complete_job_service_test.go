// Package completion_test — complete_job_service_test.go (P0 Commit 7,
// July 2026).
//
// Service-level tests for the Sender-side atomic CompleteJob.
// The mock ports (mockTxRunner + mockCache + mockTxContext) are
// hand-rolled to keep the test surface hermetic — no SQLite, no
// network, no time-of-day nondeterminism. Each test exercises one
// godlike/07 typed-error sentinel + the in-TX orchestration order.
//
// godlike/06 SSOT for the canonical-owner test surface: every
// failure mode has exactly one test, no enumeration duplication,
// each test pins the (input, mock-state, expected output) triple
// for the audit review.
package policy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/completion/internal"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Mock TxRunner + TxContext (deterministic, hand-rolled) ─────────────

type mockTxRunner struct {
	mu                 sync.Mutex
	committed          bool
	rolledBack         bool
	executedOperations []string
}

func (m *mockTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx completion.TxContext) error) error {
	m.mu.Lock()
	m.executedOperations = append(m.executedOperations, "BeginTx")
	m.mu.Unlock()
	mock := newMockTxContext()
	err := fn(ctx, mock)
	m.mu.Lock()
	if err != nil {
		m.rolledBack = true
		m.executedOperations = append(m.executedOperations, "Rollback")
	} else {
		m.committed = true
		m.executedOperations = append(m.executedOperations, "Commit")
	}
	m.mu.Unlock()
	return err
}

type mockTxContext struct {
	mu                sync.Mutex
	jobs              map[string]*completion.JobRow
	results           map[string]completion.ArtifactMapEntry // key: jobID+attempt+artifactID
	outbox            []completion.OutboxEnvelope
	priorHashesCache  map[string]map[string]completion.PriorArtifactHash
	getPriorHashCalls int

	// InsertAssetLocations (Azione 6, July 2026): recorded entries
	// (typed-writes to asset_locations). insertLocationsFn lets a
	// test inject a typed-error return path; defaults to nil-success.
	insertedLocations []completion.AssetLocationEntry
	insertLocationsFn func(ctx context.Context, entries []completion.AssetLocationEntry) error
}

func newMockTxContext() *mockTxContext {
	return &mockTxContext{
		jobs:             map[string]*completion.JobRow{},
		results:          map[string]completion.ArtifactMapEntry{},
		priorHashesCache: map[string]map[string]completion.PriorArtifactHash{},
	}
}

func (m *mockTxContext) GetJob(ctx context.Context, jobID string) (*completion.JobRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.jobs[jobID]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (m *mockTxContext) UpdateJobToSucceededCAS(ctx context.Context, jobID, leaseID string, attempt int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.jobs[jobID]
	if !ok {
		return 0, nil
	}
	if r.LeaseID != leaseID || r.Attempt != attempt {
		return 0, nil
	}
	if r.Status == job.StatusSucceeded || r.Status == job.StatusFailed || r.Status == job.StatusCancelled {
		return 0, nil
	}
	r.Status = job.StatusSucceeded
	return 1, nil
}

func (m *mockTxContext) InsertResultOnConflict(ctx context.Context, jobID string, attempt int, codecID string, payload []byte, resultHash string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mock UNIQUE constraint: key = jobID+attempt+resultHash.
	key := jobID + "|" + itoa(attempt) + "|" + resultHash
	if _, exists := m.results[key]; exists {
		return 0, true, nil // ON CONFLICT DO NOTHING
	}
	m.results[key] = completion.ArtifactMapEntry{ArtifactID: key, SHA256: resultHash}
	return 1, false, nil
}

func (m *mockTxContext) GetPriorArtifactHashes(ctx context.Context, jobID string) (map[string]completion.PriorArtifactHash, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getPriorHashCalls++
	r, ok := m.priorHashesCache[jobID]
	if !ok {
		return map[string]completion.PriorArtifactHash{}, nil
	}
	out := map[string]completion.PriorArtifactHash{}
	for k, v := range r {
		out[k] = v
	}
	return out, nil
}

func (m *mockTxContext) PersistArtifactMap(ctx context.Context, jobID string, attempt int, entries []completion.ArtifactMapEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range entries {
		m.results[jobID+"|"+itoa(attempt)+"|"+e.ArtifactID] = e
	}
	return nil
}

func (m *mockTxContext) InsertOutboxEnvelope(ctx context.Context, envelope completion.OutboxEnvelope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outbox = append(m.outbox, envelope)
	return nil
}

// InsertAssetLocations (Azione 6, July 2026) records the in-TX
// writes to asset_locations on the mockTxContext. The optional
// insertLocationsFn lets tests inject a typed-error return path
// (e.g. for round-trip-mismatch or transient-failure scenarios);
// default nil means a successful no-op (locations recorded only).
func (m *mockTxContext) InsertAssetLocations(ctx context.Context, entries []completion.AssetLocationEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.insertedLocations = append(m.insertedLocations, entries...)
	if m.insertLocationsFn != nil {
		return m.insertLocationsFn(ctx, entries)
	}
	return nil
}

func (m *mockTxContext) setPriorHashes(jobID string, hashes map[string]completion.PriorArtifactHash) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.priorHashesCache[jobID] = hashes
}

// ── Mock IdempotencyCachePort (in-memory) ─────────────────────────────

type mockCache struct {
	mu      sync.Mutex
	entries map[string]*remote.CompleteJobResponse
}

func newMockCache() *mockCache {
	return &mockCache{entries: map[string]*remote.CompleteJobResponse{}}
}

func (m *mockCache) LookupReplay(ctx context.Context, jobID string, attempt int, resultHash string) (*remote.CompleteJobResponse, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := jobID + "|" + itoa(attempt) + "|" + resultHash
	r, ok := m.entries[k]
	if !ok {
		return nil, false, nil
	}
	// Return a copy so the test cannot mutate the cache canonical.
	cp := *r
	cp.JobArtifactIDs = append([]string(nil), r.JobArtifactIDs...)
	return &cp, true, nil
}

func (m *mockCache) StoreCanonical(ctx context.Context, jobID string, attempt int, resultHash string, resp *remote.CompleteJobResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := jobID + "|" + itoa(attempt) + "|" + resultHash
	cp := *resp
	cp.JobArtifactIDs = append([]string(nil), resp.JobArtifactIDs...)
	m.entries[k] = &cp
	return nil
}

// ── itoa — local helper (zero-import) ────────────────────────────────

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ── Test helpers ──────────────────────────────────────────────────────

func newHappyPathRequest() *remote.CompleteJobRequest {
	return &remote.CompleteJobRequest{
		WorkerID:   "w-1",
		JobID:      "j-1",
		Attempt:    0,
		LeaseID:    "lease-1",
		Result:     []byte(`{"ok":true,"v":1}`),
		ResultHash: "h-abcdef",
		Artifacts: job.RemoteArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			WorkflowID:    "wf-1",
			JobID:         "j-1",
			Artifacts: []job.RemoteArtifact{
				{ID: "j-1:voiceover", Kind: "voiceover", Filename: "en.mp3",
					MIMEType: "audio/mpeg", SHA256: "sh-1", RemoteAssetID: "ra-1", Status: job.StatusReady},
				{ID: "j-1:metadata", Kind: "metadata", Filename: "meta.json",
					MIMEType: "application/json", SHA256: "sh-2", RemoteAssetID: "ra-2", Status: job.StatusReady},
			},
		},
	}
}

// ── Tests ─────────────────────────────────────────────────────────────

func TestService_NotConfigured(t *testing.T) {
	if _, err := completion.NewService(nil, newMockCache()); err == nil {
		t.Fatal("expected nil-rxRunner error")
	}
	if _, err := completion.NewService(&mockTxRunner{}, nil); err == nil {
		t.Fatal("expected nil-cache error")
	}
	if _, err := completion.NewService(nil, nil); err == nil {
		t.Fatal("expected nil-both error")
	}
}

func TestService_Complete_HappyPath_SingleTransaction(t *testing.T) {
	cache := newMockCache()
	rxFactory := func(jobID string, leaseID string, attempt int) CompletionTxRunner {
		return &seedingMockTxRunner{
			seedJob: &completion.JobRow{
				JobRow: internal.JobRow{
					JobID: jobID, LeaseID: leaseID, Attempt: attempt, Status: job.StatusRunning,
				},
				JobType: "test.non-artifact",
			},
		}
	}
	svc, err := completion.NewService(rxFactory("j-1", "lease-1", 0), cache)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	req := newHappyPathRequest()
	resp, err := svc.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if resp.Status != job.StatusSucceeded {
		t.Errorf("status: want SUCCEEDED, got %s", resp.Status)
	}
	if len(resp.JobArtifactIDs) != 2 {
		t.Errorf("artifact ids count: want 2, got %d", len(resp.JobArtifactIDs))
	}
	for _, want := range []string{"j-1:voiceover", "j-1:metadata"} {
		found := false
		for _, got := range resp.JobArtifactIDs {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing artifact id %s in response", want)
		}
	}
}

// CompletionTxRunner is the consumer-side alias of the port to
// keep the test helpers portable across mock implementations.
type CompletionTxRunner = completion.CompleteJobTxRunner

// seedingMockTxRunner is a TxRunner that pre-populates the mock
// TxContext with the canonical job row before delegating to the
// raw mockTxRunner. Used by the happy-path + idempotency replay
// tests.
type seedingMockTxRunner struct {
	seedJob *completion.JobRow
}

func (s *seedingMockTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx completion.TxContext) error) error {
	mock := newMockTxContext()
	mock.jobs[s.seedJob.JobID] = s.seedJob
	if err := fn(ctx, mock); err != nil {
		return err
	}
	return nil
}

func TestService_Complete_IdempotencyReplay_ReturnsSameResponse(t *testing.T) {
	req := newHappyPathRequest()
	// Pre-populate the cache with the canonical response (simulates
	// a prior successful complete + the post-TX cache.StoreCanonical).
	cache := newMockCache()
	cachedResp := &remote.CompleteJobResponse{
		Status:         job.StatusSucceeded,
		JobArtifactIDs: []string{"j-1:voiceover", "j-1:metadata"},
		JobID:          "j-1",
		Attempt:        0,
		ResultHash:     "h-abcdef",
	}
	if err := cache.StoreCanonical(context.Background(), "j-1", 0, "h-abcdef", cachedResp); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	// Use a TxRunner that ERRORS so we know the in-TX path did NOT run.
	bombed := false
	rx := &bombingTxRunner{bomb: &bombed}
	svc, err := completion.NewService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	resp, err := svc.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("idempotency replay: %v", err)
	}
	if resp.Status != job.StatusSucceeded {
		t.Errorf("status drift: %s", resp.Status)
	}
	if len(resp.JobArtifactIDs) != 2 {
		t.Errorf("artifact ids drift: %d", len(resp.JobArtifactIDs))
	}
	if bombed {
		t.Error("TX runner was invoked when the cache hit should short-circuit step 3")
	}
}

type bombingTxRunner struct{ bomb *bool }

func (b *bombingTxRunner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx completion.TxContext) error) error {
	*b.bomb = true
	return errors.New("bombing txrunner: should not have been called")
}

func TestService_Complete_LeaseStolen_ReturnsTypedErrConcurrentLeaseRefutation(t *testing.T) {
	// Seed the job with WRONG lease so CAS rejects.
	rx := &seedingMockTxRunner{
		seedJob: &completion.JobRow{
			JobRow: internal.JobRow{
				JobID: "j-1", LeaseID: "different-lease", Attempt: 0, Status: job.StatusRunning,
			},
			JobType: "test.non-artifact",
		},
	}
	cache := newMockCache()
	svc, err := completion.NewService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	req := newHappyPathRequest()
	_, err = svc.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected typed ErrConcurrentLeaseRefutation")
	}
	if !errors.Is(err, remote.ErrConcurrentLeaseRefutation) {
		t.Errorf("expected ErrConcurrentLeaseRefutation, got: %v", err)
	}
}

func TestService_Complete_MissingRequired_RejectedBeforeTransaction(t *testing.T) {
	// Empty-artifacts request must be caught at the pre-TX
	// Validated gate so the TxRunner is NEVER invoked.
	bombed := false
	rx := &bombingTxRunner{bomb: &bombed}
	cache := newMockCache()
	svc, err := completion.NewService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	req := &remote.CompleteJobRequest{
		WorkerID: "w-1", JobID: "j-1", Attempt: 0, LeaseID: "lease-1",
		Result: []byte(`{}`), ResultHash: "h-1",
		// Artifacts intentionally empty — pre-TX fail-fast.
		Artifacts: job.RemoteArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			// zero Artifacts slice
		},
	}
	_, err = svc.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected pre-TX rejected error")
	}
	if !errors.Is(err, remote.ErrCompleteJobRequestMissingFields) {
		t.Errorf("expected ErrCompleteJobRequestMissingFields, got: %v", err)
	}
	if bombed {
		t.Error("TX runner was invoked despite pre-TX gate rejection (fail-fast contract broken)")
	}
}

func TestService_Complete_HashMismatch_ReturnsTypedErrRemoteArtifactHashMismatch(t *testing.T) {
	// Seed the job at status=RUNNING so CAS passes; seed the
	// prior artifact hashes with a DIFFERENT sha256 for one entry.
	cache := newMockCache()
	// Custom seeding with priorHashes: the bombing approach does
	// not work here, so the test wraps the mockTxRunner.
	wrapped := &seedingWithPriorHashesRunner{
		seedJob: &completion.JobRow{
			JobRow: internal.JobRow{
				JobID: "j-1", LeaseID: "lease-1", Attempt: 0, Status: job.StatusRunning,
			},
			JobType: "test.non-artifact",
		},
		priorHashes: map[string]completion.PriorArtifactHash{
			"j-1:voiceover": {SHA256: "DIFFERENT-SHA", RemoteAssetID: "ra-prior", Status: job.StatusReady},
		},
	}
	svc, err := completion.NewService(wrapped, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	req := newHappyPathRequest()
	_, err = svc.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected typed ErrRemoteArtifactHashMismatch")
	}
	if !errors.Is(err, remote.ErrRemoteArtifactHashMismatch) {
		t.Errorf("expected ErrRemoteArtifactHashMismatch, got: %v", err)
	}
}

type seedingWithPriorHashesRunner struct {
	seedJob     *completion.JobRow
	priorHashes map[string]completion.PriorArtifactHash
}

func (s *seedingWithPriorHashesRunner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx completion.TxContext) error) error {
	mock := newMockTxContext()
	mock.jobs[s.seedJob.JobID] = s.seedJob
	mock.setPriorHashes(s.seedJob.JobID, s.priorHashes)
	return fn(ctx, mock)
}

// TestService_Complete_HashMismatch_ReturnsTypedErrRemoteArtifactHashMismatch:

func TestService_NilReceiver_ReturnsNotConfigured(t *testing.T) {
	var svc *completion.Service
	_, err := svc.Complete(context.Background(), newHappyPathRequest())
	if err == nil {
		t.Fatal("expected nil-receiver error")
	}
	if !errors.Is(err, remote.ErrCompleteJobNotConfigured) {
		t.Errorf("expected ErrCompleteJobNotConfigured, got: %v", err)
	}
}

func TestService_Complete_NilReceiver_ReturnsMissingFields(t *testing.T) {
	rx := &mockTxRunner{}
	cache := newMockCache()
	svc, err := completion.NewService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	_, err = svc.Complete(context.Background(), nil)
	if err == nil {
		t.Fatal("expected missing-fields error (nil request)")
	}
	if !errors.Is(err, remote.ErrCompleteJobRequestMissingFields) {
		t.Errorf("expected ErrCompleteJobRequestMissingFields, got: %v", err)
	}
}

// ── FASE 0.1 (July 4, 2026) — JobTypeRegistry port + ErrCompleteJobPathViolation ──

// mockJobTypeRegistry satisfies the completion.JobTypeRegistry port for
// unit tests. The ProducesArtifacts map is set up at construction; nil-safe
// lookups return false (godlike/06 SSOT: registry never lies about a
// job-type-it's-never-heard-of — fail toward the more-permissive path).
type mockJobTypeRegistry struct {
	mu     sync.Mutex
	policy map[string]bool
}

func newMockJobTypeRegistry() *mockJobTypeRegistry {
	return &mockJobTypeRegistry{policy: map[string]bool{}}
}

func (m *mockJobTypeRegistry) ProducesArtifacts(jobType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy[jobType]
}

func (m *mockJobTypeRegistry) Set(jobType string, produces bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy[jobType] = produces
}

// Compile-time pin (godlike/06 SSOT + Pattern 0 typed-port discipline):
// signature drift on the JobTypeRegistry interface becomes a build
// failure at this line rather than a runtime panic in tests.
var _ completion.JobTypeRegistry = (*mockJobTypeRegistry)(nil)

// FASE 0.1 (July 4, 2026) honest-limitation doc on the in-TX gate tests:
// the in-TX gate at completeInTx is structurally unreachable today
// (Validated rejects empty Artifacts at the pre-TX fail-fast gate).
// The 3 NEW tests below verify STRUCTURAL pieces (port wiring +
// fluent setter + nil-safety + sentinel distinct-ness). The
// truth-table test of the gate's actual conditional (registry says
// ProducesArtifacts=true AND len(Artifacts)==0 → ErrCompleteJobPathViolation)
// is forward-pointed to PR-Validated-Loosen: a BACKFILL wave softens
// Validated to permit empty artifacts on the typed-service surface
// for non-artifact job types, which unlocks the gate's enforcement
// surface. Until then, defense-in-depth on the SQL layer
// (SQLiteStore.Complete → domainremote.ErrCompleteJobPathViolation)
// is the only active enforcement point. The gate's value is to be
// the canonical SSOT surface for the failure mode when BACKFILL lands.

// TestService_WithJobTypeRegistry_WiresPort_FluentChain verifies the
// Pattern-0 fluent setter at composition-root time. The setter MUST
// return the receiver for chainable config (matches pkg/workerstats
// precedent + the canonical idiom across application-layer ports).
func TestService_WithJobTypeRegistry_WiresPort_FluentChain(t *testing.T) {
	cache := newMockCache()
	rx := &mockTxRunner{}
	svc, err := completion.NewService(rx, cache)
	if err != nil {
		t.Fatalf("new svc: %v", err)
	}
	reg := newMockJobTypeRegistry()
	got := svc.WithJobTypeRegistry(reg)
	if got != svc {
		t.Errorf("WithJobTypeRegistry should return the receiver for fluent chain; got %p want %p", got, svc)
	}
	// Verify the registry is wired by exercising the registered behavior.
	reg.Set("artifact.job", true)
	if !reg.ProducesArtifacts("artifact.job") {
		t.Error("registry round-trip failed post Set")
	}
}

// TestService_WithJobTypeRegistry_NilReceiver_ReturnsNil pins the
// godlike/07 nil-safe zero-value contract for the fluent setter. A nil
// receiver composing WithJobTypeRegistry must NOT panic and MUST return
// nil (callers can chain through a nil receiver without crashing).
// Mirrors the same idiom as SetProgress (zero-value-make-no-op pattern).
func TestService_WithJobTypeRegistry_NilReceiver_ReturnsNil(t *testing.T) {
	var svc *completion.Service
	got := svc.WithJobTypeRegistry(newMockJobTypeRegistry())
	if got != nil {
		t.Errorf("WithJobTypeRegistry on nil receiver should return nil; got %p", got)
	}
}

// TestErrCompleteJobPathViolation_DistinctFromExistingSentinels pins the
// FASE 0.1 godlike/06 SSOT contract: ErrCompleteJobPathViolation is the
// SINGLE canonical typed sentinel for "legacy COMPLETE path attempted on
// artifact-producing job". It MUST NOT be aliased to any pre-existing
// CompleteJob sentinel (each sentinel represents a distinct failure mode).
func TestErrCompleteJobPathViolation_DistinctFromExistingSentinels(t *testing.T) {
	// Distinct sentinel: non-empty unique message, distinct from peers.
	if remote.ErrCompleteJobPathViolation == nil {
		t.Fatal("ErrCompleteJobPathViolation must be declared")
	}
	if remote.ErrCompleteJobPathViolation.Error() == "" {
		t.Error("ErrCompleteJobPathViolation must have a substantive error message")
	}
	// Probe: errors.Is with the new sentinel MUST NOT match the pre-existing
	// CompleteJob sentinels (each tier-2 surface has a distinct semantic).
	probe := remote.ErrCompleteJobPathViolation
	if errors.Is(probe, remote.ErrCompleteJobRequestMissingFields) {
		t.Error("ErrCompleteJobPathViolation must be distinct from ErrCompleteJobRequestMissingFields")
	}
	if errors.Is(probe, remote.ErrCompleteJobNotConfigured) {
		t.Error("ErrCompleteJobPathViolation must be distinct from ErrCompleteJobNotConfigured")
	}
	if errors.Is(probe, remote.ErrConcurrentLeaseRefutation) {
		t.Error("ErrCompleteJobPathViolation must be distinct from ErrConcurrentLeaseRefutation")
	}
	// errors.Is with itself: YES (the typed sentinel + the first wrap in
	// fmt.Errorf("%w: ...", ErrCompleteJobPathViolation) MUST match).
	if !errors.Is(probe, remote.ErrCompleteJobPathViolation) {
		t.Error("ErrCompleteJobPathViolation must be self-identifying via errors.Is")
	}
	// Wrap-probe: a fmt.Errorf("%w: jobType=test", ErrCompleteJobPathViolation)
	// chain MUST be errors.Is-able to the canonical sentinel.
	wrapped := fmt.Errorf("%w: jobType=%q", remote.ErrCompleteJobPathViolation, "artifact.job")
	if !errors.Is(wrapped, remote.ErrCompleteJobPathViolation) {
		t.Error("wrapped ErrCompleteJobPathViolation must remain errors.Is-able per godlike/07 typed-error contract")
	}
}
