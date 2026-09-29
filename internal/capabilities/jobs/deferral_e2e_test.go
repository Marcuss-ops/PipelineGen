package jobs

// Live end-to-end proof of the deferral contract (docs/operations/job-debug-runbook.md §11).
//
// Everything here is PRODUCTION code except the handler's work:
//
//	real migrations (migrations/sqlite_jobs, scope "jobs") on a temp DB
//	  → real *sqljobs.SQLiteStore
//	  → real queue Service (admission + persistence)
//	  → real Dispatcher + Registry
//	  → real Worker.Start poll loop (claim → runJob → finalize → requeue sweep)
//
// The handler defers on its first attempt and succeeds on the second — the
// exact shape of a clip.render settle whose remote render is not terminal yet.
// What this test therefore proves, on the real state machine and the real rows:
//
//	QUEUED → RUNNING → RETRY_WAIT (retry_count UNCHANGED) → QUEUED → SUCCEEDED
//
// and, in between, that the deferred row is NOT re-claimed before its
// deferred_until instant (the whole point of the hint), plus the durable
// job_events audit trail an operator reads during an incident.
//
// The assertions read the DATABASE, not the in-memory objects: a promise that
// only holds inside a struct is not a promise an operator can rely on.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
)

const (
	deferralE2EJobType = TypeScriptGenerate
	// deferralE2EDelay is the wait the handler states. Deliberately short so
	// the test observes the re-dispatch in real time instead of sleeping for a
	// production cadence.
	deferralE2EDelay = 400 * time.Millisecond
)

// TestDeferredJobRunsThroughTheRealQueue is the end-to-end proof. It is NOT a
// unit test with fakes: see the file header.
func TestDeferredJobRunsThroughTheRealQueue(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "jobs", "jobs.db.sqlite")
	if err := runJobsPlaneMigrations(dbPath); err != nil {
		t.Fatalf("apply migrations/sqlite_jobs: %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open migrated jobs DB: %v", err)
	}
	defer db.Close()

	store := sqljobs.NewSQLiteStore(db, zap.NewNop())

	// The registry is the typed per-job-type policy (timeout + retry budget).
	// A deferral must survive even at retry_count == max_retries, so the budget
	// is the smallest interesting value: ONE allowed retry, spent by NOTHING.
	reg := NewRegistry()
	if err := reg.Register(RegistryEntry{
		Completion: CompletionDeclaration{
			JobType:              deferralE2EJobType,
			ArtifactOwnership:    ArtifactOwnershipNone,
			FinalizationStrategy: FinalizationStrategyLegacyComplete,
		},
		Description:       "deferral e2e job",
		Timeout:           30 * time.Second,
		DefaultMaxRetries: 1,
	}); err != nil {
		t.Fatalf("register %s: %v", deferralE2EJobType, err)
	}
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	svc, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// The handler: attempt 1 is a WAIT, attempt 2 is the work. It counts its
	// own invocations so the test can prove the deferred row was not claimed
	// again before its instant.
	var (
		mu    sync.Mutex
		calls int
	)
	handler := func(_ context.Context, _ *job.Job, _ *JobExecutionTools) (Result, error) {
		mu.Lock()
		calls++
		attempt := calls
		mu.Unlock()
		if attempt == 1 {
			return nil, job.DeferredAfter(deferralE2EDelay, "remote render still running")
		}
		return Result{"ok": true, "attempt": attempt}, nil
	}

	dispatcher := NewDispatcher()
	if err := dispatcher.Register(deferralE2EJobType, handler); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	dispatcher.Freeze()

	worker := NewWorker(WorkerDeps{
		ID:         "deferral-e2e-worker",
		Repo:       store,
		Dispatcher: dispatcher,
		Notifier:   store,
		Log:        zap.NewNop(),
		LeaseTTL:   30 * time.Second,
		PollEvery:  20 * time.Millisecond,
		Backoff:    BackoffConfig{MaxBackoff: 200 * time.Millisecond, ConsecutiveEmptyThreshold: 3},
		Types:      []string{deferralE2EJobType},
	}).WithRegistry(reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	// A transition trace, sampled from the DATABASE every 5ms: this is the
	// "do the states really change?" evidence, and it is also what makes a
	// missed transition window (a state that lives for less than one poll)
	// visible instead of silently swallowed by an eventual assertion.
	trace := newStateTrace()
	trace.start(ctx, t, store)
	defer trace.dump(t)

	enqueued, err := svc.Enqueue(ctx, &job.EnqueueRequest{
		Type:    deferralE2EJobType,
		Payload: map[string]any{"run_id": "deferral-e2e", "scene_count": 3},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	t.Logf("enqueued job_id=%s type=%s max_retries=%d", enqueued.ID, enqueued.Type, enqueued.MaxRetries)
	trace.watch(enqueued.ID)

	// ── 1. The WAIT lands on the row ────────────────────────────────────────
	deferred := waitForStatus(t, store, enqueued.ID, job.StatusRetryWait, trace)
	if deferred.RetryCount != 0 {
		t.Fatalf("a deferral must NOT consume the retry budget, got retry_count=%d/%d",
			deferred.RetryCount, deferred.MaxRetries)
	}
	if deferred.DeferredUntil == nil {
		t.Fatal("a deferred row must carry the instant it may come back (deferred_until is NULL)")
	}
	if !strings.Contains(deferred.Error, "remote render still running") {
		t.Fatalf("the handler's reason must be persisted on the row, got error=%q", deferred.Error)
	}
	t.Logf("state=RETRY_WAIT retry_count=%d/%d deferred_until=%s error=%q",
		deferred.RetryCount, deferred.MaxRetries,
		deferred.DeferredUntil.UTC().Format("15:04:05.000"), deferred.Error)

	remaining := time.Until(*deferred.DeferredUntil)
	if remaining <= 0 {
		t.Fatalf("deferred_until must be in the FUTURE right after the deferral, got %s", remaining)
	}

	// ── 2. The hint is honoured: no claim before deferred_until ─────────────
	// Sleep for most of the remaining wait, then read the row again. A row
	// re-dispatched early would show RUNNING/SUCCEEDED and a second call.
	if sleep := time.Duration(float64(remaining) * 0.5); sleep > 10*time.Millisecond {
		time.Sleep(sleep)
	}
	mid := getJob(t, store, enqueued.ID)
	if mid.Status != job.StatusRetryWait {
		t.Fatalf("before deferred_until the row must stay RETRY_WAIT, got %s", mid.Status)
	}
	mu.Lock()
	callsBeforeHint := calls
	mu.Unlock()
	if callsBeforeHint != 1 {
		t.Fatalf("the deferred row must not be dispatched before deferred_until, handler ran %d times", callsBeforeHint)
	}
	t.Logf("before the hint: status=%s handler_calls=%d (not re-dispatched)", mid.Status, callsBeforeHint)

	// ── 3. At the hint the sweep requeues it and the work completes ─────────
	succeeded := waitForStatus(t, store, enqueued.ID, job.StatusSucceeded, trace)
	if succeeded.RetryCount != 0 {
		t.Fatalf("the whole wait must cost ZERO retries, got retry_count=%d", succeeded.RetryCount)
	}
	mu.Lock()
	totalCalls := calls
	mu.Unlock()
	if totalCalls != 2 {
		t.Fatalf("the handler must run exactly twice (defer, then work), ran %d times", totalCalls)
	}
	if succeeded.DeferredUntil != nil {
		t.Fatalf("deferred_until must be cleared on the way out of RETRY_WAIT, got %s", succeeded.DeferredUntil)
	}
	t.Logf("state=SUCCEEDED retry_count=%d/%d handler_calls=%d result=%s",
		succeeded.RetryCount, succeeded.MaxRetries, totalCalls, string(succeeded.Result))

	// ── 4. The audit trail an operator actually reads ───────────────────────
	events := eventsFor(t, store, enqueued.ID)
	for _, e := range events {
		t.Logf("event %-14s %s  %s", e.Type, e.CreatedAt.UTC().Format("15:04:05.000"), e.Message)
	}
	if !hasEvent(events, "job_deferred") {
		t.Fatalf("the timeline must record the deferral as its own event type, got [%s]", eventTypes(events))
	}
	if !hasEvent(events, "job_queued") {
		t.Fatalf("the requeue that followed the wait must be visible in the timeline, got [%s]", eventTypes(events))
	}

	// ── 5. The transition trace, asserted as a sequence ─────────────────────
	// Sampled from the DB while it ran: the queue admitted the job, the worker
	// claimed it, the deferral parked it WITHOUT touching the budget, and the
	// sweep re-dispatched it to completion.
	// Only the PARKED WAIT is required of the sampler, and that is deliberate.
	//
	// The sampler is started once the id is known, so it can lose the race for
	// the QUEUED window outright and open on RUNNING; and the test body can end
	// before its next 5ms tick after the row reaches SUCCEEDED, so it can also
	// miss the terminal state. Requiring either of those made the assertion a
	// coin flip over windows the sampler does not own. Both are proven on the
	// durable plane instead, which is strictly stronger than catching a
	// transient row: QUEUED by the job_queued event asserted above (the same
	// division of labour this test already applies to RUNNING), and SUCCEEDED by
	// the waitForStatus call that returned the row this block runs on.
	//
	// RETRY_WAIT is the one state the sampler uniquely documents as a LIVE
	// observation, and it is the one that reliably outlives a tick: the parked
	// wait is what the whole test is about.
	trace.requiresSequence(t,
		"RETRY_WAIT retry_count=0/1",
	)
	// The execution half is proven by the timeline rather than by the sampler:
	// RUNNING is a short window (claim → dispatch) and a 5ms sampler can miss
	// it, while the events are written by the worker itself on every attempt.
	if !hasEvent(events, "job_running") {
		t.Fatalf("the worker must record the RUNNING phase of each attempt, got [%s]", eventTypes(events))
	}
	if !hasEvent(events, "leased") {
		t.Fatalf("each attempt must be claimed (leased), got [%s]", eventTypes(events))
	}
}

// TestDeferredRowSurvivesTheRetryBudgetBeingExhausted is the second half of the
// contract, on the same real plane: a job whose retry budget is ALREADY spent
// must still be allowed to WAIT. A retry would be refused here (retry_count ==
// max_retries is terminal for failures); a deferral must not be, because a wait
// has no budget to exhaust.
func TestDeferredRowSurvivesTheRetryBudgetBeingExhausted(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "jobs", "jobs.db.sqlite")
	if err := runJobsPlaneMigrations(dbPath); err != nil {
		t.Fatalf("apply migrations/sqlite_jobs: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open migrated jobs DB: %v", err)
	}
	defer db.Close()
	store := sqljobs.NewSQLiteStore(db, zap.NewNop())

	reg := NewRegistry()
	if err := reg.Register(RegistryEntry{
		Completion: CompletionDeclaration{
			JobType:              deferralE2EJobType,
			ArtifactOwnership:    ArtifactOwnershipNone,
			FinalizationStrategy: FinalizationStrategyLegacyComplete,
		},
		Description:       "deferral e2e budget job",
		Timeout:           30 * time.Second,
		DefaultMaxRetries: 1,
	}); err != nil {
		t.Fatalf("register %s: %v", deferralE2EJobType, err)
	}
	store.SetProducesArtifacts(reg.ProducesArtifactsMap())

	created, err := NewService(store, nil, zap.NewNop(), reg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	enqueued, err := created.Enqueue(context.Background(), &job.EnqueueRequest{
		Type:    deferralE2EJobType,
		Payload: map[string]any{"run_id": "budget-exhausted"},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Drive the row to RUNNING exactly like the worker's claim does, then set
	// retry_count to the max: the state every long external wait eventually
	// reaches.
	claimed, err := store.ClaimNext(context.Background(), "budget-worker", time.Minute, []string{deferralE2EJobType})
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext = (%v, %v)", claimed, err)
	}
	if _, err := db.ExecContext(context.Background(),
		`UPDATE jobs SET retry_count = max_retries WHERE id = ?`, enqueued.ID); err != nil {
		t.Fatalf("spend the retry budget: %v", err)
	}
	fresh := getJob(t, store, enqueued.ID)

	// The canonical finalize call the worker issues for a deferral.
	if _, err := store.FinalizeAttempt(context.Background(), job.FinalizeAttemptCommand{
		JobID:            fresh.ID,
		Outcome:          job.OutcomeDeferred,
		WorkerID:         "budget-worker",
		LeaseID:          fresh.LeaseID,
		ExpectedRevision: fresh.Revision,
		ErrorMessage:     "waiting on the provider window",
		Backoff:          90 * time.Second,
		EventType:        "job_deferred",
	}); err != nil {
		t.Fatalf("a deferral at an exhausted budget must be accepted, got %v", err)
	}

	after := getJob(t, store, enqueued.ID)
	if after.Status != job.StatusRetryWait {
		t.Fatalf("status = %s, want RETRY_WAIT", after.Status)
	}
	if after.RetryCount != after.MaxRetries {
		t.Fatalf("a deferral must not change retry_count: %d -> %d", fresh.RetryCount, after.RetryCount)
	}
	if after.DeferredUntil == nil {
		t.Fatal("deferred_until must be set")
	}
	t.Logf("budget exhausted (retry_count=%d/%d): status=%s deferred_until=%s",
		after.RetryCount, after.MaxRetries, after.Status,
		after.DeferredUntil.UTC().Format("15:04:05.000"))
}

// ── helpers ──────────────────────────────────────────────────────────────

func runJobsPlaneMigrations(dbPath string) error {
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations", "sqlite_jobs"))
	if err != nil {
		return err
	}
	// The production opener owns its data directory; here the temp path is
	// built two levels deep on purpose so the test also exercises a split-plane
	// layout (jobs/ next to the media DB).
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	return storage.RunMigrationsOnDB(dbPath, zap.NewNop(), dir, "jobs")
}

func getJob(t *testing.T, store *sqljobs.SQLiteStore, id string) *job.Job {
	t.Helper()
	j, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get(%s): %v", id, err)
	}
	if j == nil {
		t.Fatalf("job %s disappeared", id)
	}
	return j
}

func waitForStatus(t *testing.T, store *sqljobs.SQLiteStore, id string, want job.Status, trace *stateTrace) *job.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last job.Status
	for time.Now().Before(deadline) {
		j := getJob(t, store, id)
		if j.Status == want {
			return j
		}
		if j.Status.IsTerminal() && j.Status != want {
			trace.dump(t)
			t.Fatalf("job reached terminal status %s while waiting for %s", j.Status, want)
		}
		last = j.Status
		time.Sleep(5 * time.Millisecond)
	}
	trace.dump(t)
	t.Fatalf("timed out waiting for status %s (last seen %s)", want, last)
	return nil
}

// stateTrace records the row's state transitions as seen through the store.
type stateTrace struct {
	mu          sync.Mutex
	id          string
	transitions []string
	stop        chan struct{}
}

func newStateTrace() *stateTrace {
	return &stateTrace{stop: make(chan struct{})}
}

func (s *stateTrace) start(ctx context.Context, t *testing.T, store *sqljobs.SQLiteStore) {
	t.Helper()
	s.id = ""
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		var prev string
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				id := s.id
				s.mu.Unlock()
				if id == "" {
					continue
				}
				j, err := store.Get(ctx, id)
				if err != nil || j == nil {
					continue
				}
				snapshot := fmt.Sprintf("%s retry_count=%d/%d deferred_until=%s",
					j.Status, j.RetryCount, j.MaxRetries, formatDeferredUntil(j))
				if snapshot == prev {
					continue
				}
				prev = snapshot
				s.mu.Lock()
				s.transitions = append(s.transitions,
					time.Now().UTC().Format("15:04:05.000")+"  "+snapshot)
				s.mu.Unlock()
			}
		}
	}()
}

func (s *stateTrace) watch(id string) {
	s.mu.Lock()
	s.id = id
	s.mu.Unlock()
}

func (s *stateTrace) snapshots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.transitions))
	copy(out, s.transitions)
	return out
}

func (s *stateTrace) dump(t *testing.T) {
	t.Helper()
	for _, line := range s.snapshots() {
		t.Logf("state %s", line)
	}
}

func formatDeferredUntil(j *job.Job) string {
	if j.DeferredUntil == nil {
		return "NULL"
	}
	return j.DeferredUntil.UTC().Format("15:04:05.000")
}

func (s *stateTrace) requiresSequence(t *testing.T, want ...string) {
	t.Helper()
	seen := strings.Join(s.snapshots(), "\n")
	for _, w := range want {
		if !strings.Contains(seen, w) {
			t.Errorf("the live state trace never showed %q; observed:\n%s", w, seen)
		}
	}
}

func eventsFor(t *testing.T, store *sqljobs.SQLiteStore, id string) []job.Event {
	t.Helper()
	events, err := store.ListEvents(context.Background(), id)
	if err != nil {
		t.Fatalf("ListEvents(%s): %v", id, err)
	}
	return events
}

func hasEvent(events []job.Event, want string) bool {
	for _, e := range events {
		if e.Type == want {
			return true
		}
	}
	return false
}

func eventTypes(events []job.Event) string {
	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}
	return strings.Join(types, ", ")
}
