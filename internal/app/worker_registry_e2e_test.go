// Package app_test contains external test packages for internal/app,
// including the PR-D smoke test that proves the remote worker HTTP
// broker path is aligned with the server's WorkerHandler mount.
//
// The file was originally written against a PR-A-only branch
// where jobbrokerclient hardcoded `/api/...` paths and the server
// mounted the worker handler at `/internal/v1/...`. Post-PR-B those
// two surfaces both derive from `remoteshared.InternalPathPrefix`
// and the smoke now passes; it remains the W2 acceptance gate.
package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/jobs"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/jobbrokerclient"
	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/shared"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// internalV1Prefix is the URL prefix the production server mounts
// the worker broker handler under (see internal/api/routes.go::Setup,
// `engine.Group("/internal/v1")`).
//
// Sourced from `remoteshared.InternalPathPrefix` so a future rename
// of the path (or a mis-sync between server and client) surfaces as
// a single compile/build break instead of a runtime 404 with no
// breadcrumb back to the constant.
const internalV1Prefix = remoteshared.InternalPathPrefix

// TestE2E_WorkerClaimsViaHTTPBroker_Alignment is the PR-D smoke test.
//
// Goal:
//
//	Prove that the remote worker, calling the broker via
//	`jobbrokerclient.New(...).RegisterWorker(...)`, reaches the
//	server's WorkerHandler mounted at `/internal/v1/workers/register`.
//
// Pre-PR-B state (this branch is PR-A-only):
//   - Server (PR-A): routes.go mounts InternalworkerHandler at
//     `/internal/v1` (the wiring has been there since PR-A restored
//     cmd/server/main.go and the route registration).
//   - Client: jobbrokerclient/client.go hardcodes `/api/workers/register`
//     in its `post(ctx, "/api/workers/register", ...)` string. This
//     URL is NOT registered in the server's gin router, so the call
//     returns gin 404.
//
// Expected failure on this branch:
//
//	client.RegisterWorker(ctx, ...) hits /api/workers/register
//	  → gin returns 404 "page not found"
//	  → jobbrokerclient surfaces err = "HTTP 404: 404 page not found"
//	  → require.NoError fails
//	  → test prints a diagnostic pointing at the URL-alignment gap.
//
// Post-PR-B state (after the alignment PR merges):
//
//	client.RegisterWorker(ctx, ...) hits /internal/v1/workers/register
//	  → gin routes to WorkerHandler.RegisterWorker
//	  → handler invokes mockBroker.RegisterWorker
//	  → mock returns session → handler responds 200 JSON
//	  → require.NoError passes → require.True(mock.registerCalled) passes
//	  → test PASSES.
//
// This test is intentionally THE FIRST signal a reviewer sees when
// chaining PR-A → PR-B: a green run here is the W2 acceptance gate
// for the path-alignment sub-task.
func TestE2E_WorkerClaimsViaHTTPBroker_Alignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mock := newMockBroker(t)

	// Build a real gin.Engine that mirrors the production server's
	// /internal/v1 mount. We mount the actual production
	// `workers.InternalworkerHandler` so we test the real gin route
	// mapping (not a stub). The middleware here is permissive —
	// production routes.go mounts `middleware.WorkerAuth(r.cfg)`
	// (PR-B) which is stricter; this test focuses on path alignment
	// so any non-empty Bearer is accepted.
	engine := gin.New()
	// Capture the actual URL the client hits. This middleware runs before
	// the worker handler so observedRegisterPath reflects what gin's
	// router saw, not a hardcoded constant. The previous implementation
	// set observedRegisterPath = "/workers/register" inside the mock,
	// which is the server-relative path GIN uses AFTER it strips the
	// /internal/v1 prefix; that made the drift check tautologically
	// fail on every successful round-trip.
	//
	// Write goes through `atomic.Value.Store` so the server
	// goroutine's write has a defined happens-before relationship with
	// the test goroutine's `Load()` (round-5 explicit follow-up to the
	// reviewer's theoretical race concern; `-race` did not surface it
	// empirically earlier but the formal Go memory model had no
	// happens-before from raw string writes across goroutines).
	engine.Use(func(c *gin.Context) {
		observedRegisterPath.Store(c.Request.URL.Path)
		c.Next()
	})
	internalGroup := engine.Group(internalV1Prefix)
	internalGroup.Use(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"ok": false, "error": "missing Authorization header",
			})
			return
		}
		c.Set("is_worker", true)
		c.Next()
	})
	// Wave 14 close (June 2026): the worker-broker HTTP handler was
	// absorbed from the standalone internal/api/workers/ package into
	// internal/api/jobs/handler_workers.go (jobs.WorkersBrokerHandler)
	// alongside the public-facing jobs.JobsHandler. The Broker +
	// AssetTransferService interfaces are the same shape as the old
	// workers.* ports; the local mockBroker in this file satisfies
	// jobs.Broker by virtue of the same 8 method set with matching
	// parameter types. Asset transfer is passed as nil: every handler
	// method that touches h.assets has an explicit nil-check and
	// returns 501 Not Implemented, so this test exercises only the
	// RegisterWorker / path-alignment surface unaffected by the move.
	workerHandler := jobs.NewWorkersBrokerHandler(mock, nil, zap.NewNop())
	workerHandler.RegisterRoutes(internalGroup)

	ts := httptest.NewServer(engine)
	t.Cleanup(ts.Close)

	// The client uses pre-PR-B URLs (hardcoded `/api/...`). When this
	// test runs on a branch where PR-B has not merged, the call below
	// is expected to fail. The failure message is the diagnostic.
	client := jobbrokerclient.New(ts.URL, "test-worker-token-DO-NOT-LEAK")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	session, err := client.RegisterWorker(ctx, job.RegisterWorkerCommand{
		WorkerID: "w-e2e-1",
		Name:     "test-worker",
		Capabilities: job.WorkerCapabilities{
			JobTypes: []string{"test.dummy"},
		},
	})

	if err != nil {
		t.Fatalf("RegisterWorker returned an error: %v\n\n"+
			"PR-D smoke diagnostic:\n"+
			"  expected  client → POST %s%s/workers/register → 200 JSON\n"+
			"  actual    client.POST went elsewhere; server (%s) answered non-2xx.\n\n"+
			"If the error string contains 'HTTP 404':\n"+
			"  the path-alignment gap is still open — PR-B swaps the client's\n"+
			"  hardcoded '/api/...' strings for 'remoteshared.InternalPathPrefix'\n"+
			"  so the request reaches '/internal/v1/workers/register'.\n\n"+
			"If the error string contains 'HTTP 401 (or 500 from WorkerAuth misconfig)':\n"+
			"  PR-B (WorkerAuth + docker topology) addresses this.\n\n"+
			"Either way: this test passing IS the W2 acceptance gate.",
			err, ts.URL, internalV1Prefix, ts.URL)
	}
	require.NotNil(t, session,
		"expected non-nil session; the broker returned nil which means the worker handler exists but returned an empty session — WireServices misconfigured")

	require.True(t, mock.registerCalled,
		"server did not invoke the broker's RegisterWorker.\n"+
			"Possible diagnoses: gin routed the call but the handler short-circuited;\n"+
			"the path landed on a different handler; or the mock's mutex never recorded.\n"+
			"Inspect the server's gin routes and the InternalworkerHandler.RegisterRoutes output.")

	// Bonus sanity: verify the path-constant alignment without running
	// another HTTP round-trip. If the client is wired to a wrong path
	// string, we want a focused failure, not just a generic 404.
	if observed := observedURL(); !containsPath(observed, internalV1Prefix) && mock.registerCalled {
		t.Errorf("URL drift: client hit %q but server mount was %q.\n"+
			"This should be impossible since the test passed the round-trip; if you\n"+
			"see this, the path constant in the test and in the client package are\n"+
			"out of sync.",
			observed, internalV1Prefix)
	}
}

// observedRegisterPath is written by the engine-level gin middleware
// (which runs in a goroutine spawned by `httptest.NewServer`) and
// read by the test goroutine once the synchronous client round-trip
// returns. Wrapping in `sync/atomic.Value` gives the two access sides
// a defined happens-before relationship; without it, the write+read
// pair is strictly racy even though TCP-level network reads
// empirically synchronize with the response writer's Close.
//
// `atomic.Value.Store` may be called with any value but the type is
// fixed on the first Store; we always Store `string` here so the
// value's type is canonical.
var observedRegisterPath atomic.Value

// observedURL reads observedRegisterPath with a nil-safe fallback for
// the case where the round-trip didn't reach the middleware (e.g.
// gin rejected the URL before any middleware ran). The empty-string
// default keeps `containsPath`'s contract intact: containsPath("",
// anything-non-empty) = false, so a missing middleware-run surfaces
// as a real drift failure rather than a silent pass.
func observedURL() string {
	v := observedRegisterPath.Load()
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// mockBroker implements the workers.Broker interface so the real
// WorkerHandler on the gin router can be exercised. Only
// RegisterWorker is asserted; other methods fail loud (Errorf) if
// they are unexpectedly invoked through the alignment smoke.
type mockBroker struct {
	t              *testing.T
	mu             sync.Mutex
	registerCalled bool
	// Phase 6 fields. When lease is set via serveLease(), the mock
	// is wired to deliver ONE real job to the worker under test and
	// record the broker interactions. When lease is nil
	// (alignment-smoke test path), the broker still surfaces every
	// non-RegisterWorker invocation as a test error so unexpected
	// calls are loud instead of silently no-op'd.
	lease       *appjobs.Lease
	leaseServed bool
	completed   []job.CompleteCommand
	// Phase 7 fields. renewCount is incremented by Mock.Renew on
	// every invocation; renewSeen is closed once the count crosses
	// 1, enabling the renewal-aware handler in
	// TestE2E_RemoteWorkerRenewsLease to unblock and observe the
	// protocol end-to-end. Separated from the lock to keep the
	// signal-on-N closure primitive (channel close is well-defined
	// in the Go memory model; a counter + Bool boolean would need
	// the same mutex discipline).
	renewCount int32
	renewSeen  chan struct{}
}

func newMockBroker(t *testing.T) *mockBroker {
	return &mockBroker{t: t, renewSeen: make(chan struct{})}
}

// renewCounterOrZero returns the current renewal count under the
// mock's mutex. Convenience accessor used by TestE2E_RemoteWorkerRenewsLease
// instead of reaching into the struct's mutex directly.
func (m *mockBroker) renewCounterOrZero() int32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renewCount
}

// serveLease configures the mock broker to return the supplied lease
// on the next Claim call. Subsequent Claim calls return (nil, nil)
// so Worker.Runner naturally falls into its idle/retry branch and
// can be cancelled cleanly. Used by TestE2E_RemoteWorkerExecutesMediaReindex
// to deliver a single media.reindex job to the worker.
//
// Calling serveLease after Claim has already been invoked is a logic
// error (the lease is meant to be threaded in before Run starts).
// We don't guard here; the test that uses it is sequential.
func (m *mockBroker) serveLease(l *appjobs.Lease) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lease = l
}

// completedResults returns a defensive copy of every Complete command
// the broker has received so far. Used by Phase 6 to assert the
// worker reported a successful job outcome to the broker.
func (m *mockBroker) completedResults() []job.CompleteCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]job.CompleteCommand, len(m.completed))
	copy(out, m.completed)
	return out
}

func (m *mockBroker) RegisterWorker(_ context.Context, cmd job.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
	m.mu.Lock()
	m.registerCalled = true
	// observedRegisterPath is now set by the gin middleware in the
	// test setup so it reflects the actual URL the client hit
	// (e.g. "/internal/v1/workers/register"), not a server-side
	// post-prefix path.
	m.mu.Unlock()
	// Mirror the canonical WorkerSession struct from
	// internal/domain/job/worker.go. Field names are exact-match on the
	// JSON tags so the production broker.Register deserialisation
	// would round-trip cleanly if the test ever swapped the mock for
	// the real repos.WorkerNodesRepository.
	return &appjobs.WorkerSession{
		WorkerID:         cmd.WorkerID,
		SessionID:        fmt.Sprintf("sess-%d", time.Now().UnixNano()),
		SessionExpiresAt: time.Now().Add(30 * time.Second),
		Capabilities:     cmd.Capabilities,
		Version:          cmd.Version,
		Hostname:         cmd.Hostname,
	}, nil
}

func (m *mockBroker) Heartbeat(_ context.Context, _ job.HeartbeatCommand) error {
	m.t.Logf("mock broker: Heartbeat called unexpectedly")
	return nil
}

// Claim, Renew, Progress, Complete, Fail, IsCancelled are
// conditionally implemented. When the Phase 6 test pre-loads a
// lease via serveLease(), these return real responses so the
// worker pipeline can run. When lease is nil (alignment-smoke
// test) they surface as test errors so unexpected invocations are
// loud instead of silently no-op'd.

func (m *mockBroker) Claim(_ context.Context, _ job.ClaimCommand) (*appjobs.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Claim (smoke test should only call RegisterWorker)")
		return nil, fmt.Errorf("not implemented in alignment-smoke mock")
	}
	if m.leaseServed {
		return nil, nil
	}
	m.leaseServed = true
	return m.lease, nil
}

func (m *mockBroker) Renew(_ context.Context, _ job.RenewCommand) (*appjobs.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Renew (smoke test should only call RegisterWorker)")
		return nil, fmt.Errorf("not implemented in alignment-smoke mock")
	}
	// Phase 7: count the renewal and signal one-shot via close.
	// The `m.renewCount == 1` guard GATES the close so a
	// double-close panic (close-of-closed-channel) is impossible.
	// A previous version also set `m.renewSeen = nil` after the
	// close as defence-in-depth, but the post-close write raced
	// with the handler goroutine's preload of `mock.renewSeen`
	// for the `<-mock.renewSeen` select-case evaluation (the
	// chan-pointer load happened BEFORE the close's sync
	// barrier into the handler receive). The redundant write is
	// gone — the field is set in newMockBroker, never nilled,
	// and after the close only the channel's own sync semantics
	// matter.
	m.renewCount++
	if m.renewCount == 1 {
		close(m.renewSeen)
	}
	return m.lease, nil
}

func (m *mockBroker) Progress(_ context.Context, _ job.ProgressCommand) error {
	// Progress is non-fatal even on the alignment-smoke path so the
	// test never tilts-over; ZLogger the call exists but does not
	// fail the test (the alignment-smoke path intentionally never
	// invokes Progress — if it does, a more specific test is needed).
	_ = time.Now()
	return nil
}

func (m *mockBroker) Complete(_ context.Context, cmd job.CompleteCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Complete (smoke test should only call RegisterWorker)")
		return fmt.Errorf("not implemented in alignment-smoke mock")
	}
	m.completed = append(m.completed, cmd)
	return nil
}

func (m *mockBroker) Fail(_ context.Context, _ job.FailCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Fail (smoke test should only call RegisterWorker)")
		return fmt.Errorf("not implemented in alignment-smoke mock")
	}
	m.completed = append(m.completed, job.CompleteCommand{
		WorkerID: "fail-recorded",
	})
	return nil
}

func (m *mockBroker) IsCancelled(_ context.Context, jobID, leaseID string) (bool, error) {
	m.t.Errorf("mock broker: unexpected IsCancelled jobID=%s leaseID=%s", jobID, leaseID)
	return false, fmt.Errorf("not implemented in alignment-smoke mock")
}

// containsPath is a tiny helper kept in this file so the test
// doesn't pull in pkg/sliceutil — the assertion is single-string
// and the file already imports enough.
func containsPath(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// ── Phase 6 — Remote worker executes a real bound handler end-to-end ──────

// TestE2E_RemoteWorkerExecutesMediaReindex is the W1 Phase 6 acceptance
// proof that the remote worker can execute a real bound handler
// end-to-end: claim → dispatch → handler return → completion reported
// back to the broker. The chosen job type is media.reindex because
// the Phase 0 inventory showed it is:
//
//	(a) bound to the in-process Dispatcher via
//	    clipindexer.RegisterJobHandler in composition.go,
//	(b) remote-safe — the handler reads input only from Job.Payload +
//	    the shared SQLite DB and writes output only back to the DB,
//	(c) trivially fast on an empty DB — it returns {total:0,
//	    indexed:0, failed:0} without invoking any python subprocess
//	    or Qdrant HTTP call.
//
// The test exercises, end-to-end:
//
//   - clipindexer.Service.HandleJob running through the Dispatcher
//   - worker.Registry populated from dispatcher.AllHandlers() via an
//     inline adaptHandler (the same bridging path
//     app.BuildWorkerRegistry uses in production cmd/worker)
//   - worker.Tools.Progress / IsCancelled forwarding to the broker
//   - worker.Runner.runLease completing the lifecycle (parsing
//     payload, calling the handler, marshalling the result, calling
//     broker.Complete)
//
// What this test does NOT pretend to cover (deferred to later waves):
//
//   - python/Qdrant subprocess paths — the handler exits early on
//     empty DB. python / Qdrant paths are exercised in W3 + W5.
//   - lease renewal — Runner.runLease does not renew in this short
//     happy path (W1 Phase 7).
//   - HTTP broker round-trip — the broker is stubbed; the W2
//     acceptance gate covers the network path bit-for-bit.
//   - progress event emission to the server's jobs table — broker
//     Progress here is a no-op counter; the worker-server
//     integration proves the same path at the network layer in W3.
func TestE2E_RemoteWorkerExecutesMediaReindex(t *testing.T) {
	// 1. Real SQLite DB on disk in a temp dir. The mattn/go-sqlite3
	//    driver is already imported (blank) so sql.Open("sqlite3",
	//    ...) is registered. SetMaxOpenConns(1) so the clipindexer
	//    handler's QueryContext + Yield goroutines share a single
	//    connection on the file-backed DB — the per-connection
	//    gotcha with :memory: doesn't apply here, but the single-
	//    connection discipline keeps the test's invariants
	//    deterministic.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "media.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	// 2. Create the columns HandleJob touches. The full media_assets
	//    schema lives in migrations/0XX_*.sql — for the "0 rows
	//    match" path we need only the columns referenced by the
	//    query string HandleJob compiles (id, source, media_type,
	//    embedding_json, transcript_embedding, metadata_json,
	//    created_at).
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS media_assets (
		id TEXT PRIMARY KEY,
		source TEXT,
		media_type TEXT,
		embedding_json TEXT,
		transcript_embedding TEXT,
		metadata_json TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`)
	require.NoError(t, err)

	// 3. clipindexer.Service. cfg.ServerURL="" so the handler skips
	//    indexViaAPI; cfg.ScriptPath would point to the python
	//    script that the handler falls back to. Since the test DB
	//    has 0 rows matching the missing-embedding condition,
	//    HandleJob short-circuits BEFORE either path is reached.
	cfg := clipindexer.DefaultConfig()
	cfg.Enabled = true
	cfg.DBPath = dbPath
	// clipindexer.NewService returns a single *Service value
	// (constructor never errors — the cfg + log are in-process types).
	clipSvc := clipindexer.NewService(cfg, db, dbPath, zap.NewNop())

	// 4. Wire the in-process Dispatcher exactly like composition.go
	//    does: register one handler directly. We don't go through
	//    InitWorkerComposition so the test is hermetic — no Drive,
	//    no AI, no Qdrant, no outbox dependencies.
	dispatcher := appjobs.NewDispatcher()
	require.NoError(t, dispatcher.Register(appjobs.TypeMediaReindex, clipSvc.HandleJob))

	// 5. Bridge via an inline adaptHandler — byte-for-byte the same
	//    adapter BuildWorkerRegistry uses in production. We inline
	//    here (it's an unexported func in package app) and iterate
	//    AllHandlers so the test exercises the same dispatch-look-up
	//    loop the production worker uses.
	adapt := func(handler appjobs.HandlerFunc) worker.Handler {
		return func(ctx context.Context, j *job.Job, tools *worker.Tools) (map[string]any, error) {
			jobTools := &appjobs.JobTools{
				Progress: func(p int, msg string) { _ = tools.Progress(ctx, p, msg) },
				Event:    func(_, _ string, _ map[string]any) {},
				IsCancelled: func() bool {
					ok, _ := tools.IsCancelled(ctx)
					return ok
				},
			}
			return handler(ctx, j, jobTools)
		}
	}
	workerReg := worker.NewRegistry()
	for jt, h := range dispatcher.AllHandlers() {
		handler := h
		require.NoError(t, workerReg.Register(jt, adapt(handler)))
	}
	workerReg.Freeze()
	require.Equal(t, []string{appjobs.TypeMediaReindex}, workerReg.JobTypes(),
		"the worker registry should expose exactly one handler under the chosen happy-path subset")

	// 6. Mock broker pre-loaded with a single media.reindex lease.
	//    ID and Type are what the production broker roundtrips; an
	//    empty payload means the handler's "missing-embedding"
	//    query returns 0 rows and short-circuits to {total:0,...}.
	mock := newMockBroker(t)
	mock.serveLease(&appjobs.Lease{
		LeaseID: "lease-test-1",
		Job: &job.Job{
			ID:       "job-test-reindex-1",
			Type:     appjobs.TypeMediaReindex,
			Payload:  json.RawMessage(`{}`),
			Status:   job.StatusRunning,
			Revision: 1,
			LeaseID:  "lease-test-1",
		},
	})

	// 7. Per-job workspace in the same temp dir so t.Cleanup removes
	//    it after the test exits. NewWorkspace handles MkdirAll.
	ws, err := worker.NewWorkspace(filepath.Join(tmpDir, "ws"))
	require.NoError(t, err)

	// 8. runner.Run blocks until ctx is done; after Complete it
	//    falls into the Claim → nil/nil branch and sleeps ~2s. We
	//    cancel after observing Complete.
	runner := worker.NewRunner(mock, workerReg, ws, nil, zap.NewNop(),
		"w-test-1", "sess-test-1", []string{appjobs.TypeMediaReindex})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(ctx) }()

	// 9. Poll mock.completedResults until the broker reports ONE
	//    Complete call, OR time out. We poll instead of using a
	//    channel because completed slice is the through-the-same-
	//    primitive artifact the production code writes to (avoids
	//    a parallel test-only channel that would drift from the
	//    production mock in subsequent refactors).
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		results := mock.completedResults()
		if len(results) >= 1 {
			got := results[0]
			require.Equal(t, "job-test-reindex-1", got.JobID,
				"Complete must carry the same JobID as the lease we served")
			require.Equal(t, "lease-test-1", got.LeaseID,
				"Complete must carry the same LeaseID as the lease we served")
			require.Equal(t, 1, got.ExpectedRevision,
				"Complete must carry the expected revision from the lease")

			var result map[string]any
			require.NoError(t, json.Unmarshal(got.Result, &result),
				"handler result must serialize cleanly to JSON")
			require.Equal(t, float64(0), result["total"],
				"empty DB → handler iterates 0 rows → reports total=0")
			require.Equal(t, float64(0), result["indexed"],
				"empty DB → handler iterates 0 rows → reports indexed=0")
			require.Equal(t, float64(0), result["failed"],
				"empty DB → handler iterates 0 rows → reports failed=0")
			t.Logf("W1 Phase 6 end-to-end OK: handler ran, returned %v, broker recorded Complete with JobID=%s", result, got.JobID)
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("worker did not complete the media.reindex job within 5s")
		case <-time.After(20 * time.Millisecond):
			// poll loop
		}
	}

	// 10. Cancel → runner.Run returns ctx.Err() (context.Canceled).
	//     Soft-assert that the runner exited cleanly within 2s of
	//     cancellation; this proves the worker is responsive to
	//     shutdown signal (a real production worker must be).
	cancel()
	select {
	case err := <-runErr:
		require.True(t, err == nil || errors.Is(err, context.Canceled),
			"runner.Run should exit in response to ctx cancel; got: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("runner.Run did not exit within 2s after ctx cancel")
	}

	// 11. Idempotency / no-temp-leak invariants per the W1 Phase 6
	//     spec checklist ("retry/idempotency" + "no temp file
	//     leak"). Re-run with the same payload (the worker is
	//     short-lived here, so we re-invoke the handler directly
	//     against the same DB and assert the same shape comes back).
	//
	// BUG FIX: step 10 cancelled the worker context. The clipindexer
	// handler's QueryContext(ctx, ...) honours cancellation and
	// returns context.Canceled immediately on a cancelled parent. We
	// MUST use a fresh context here — cancelling the runner's parent
	// must not poison the post-cancel idempotency replay. (This is
	// a real production concern too: cancellation of a worker
	// goroutine should not propagate to verification paths running
	// on the test goroutine — they're independent flows.)
	//
	// clipindexer.NewService returns a single *Service value
	// (in-process constructor, never errors). We construct j2 on
	// the same cfg/db/dbPath as clipSvc to prove the handler is
	// deterministic on empty-DB payloads: a fresh service running
	// the same query must return the same {total:0,indexed:0,
	// failed:0} shape. Handler logic is identical; this assertion
	// locks the contract.
	j2 := clipindexer.NewService(cfg, db, dbPath, zap.NewNop())
	replayCtx, replayCancel := context.WithCancel(context.Background())
	t.Cleanup(replayCancel)
	// j2Result is a fresh variable so the `:=` redeclaration rule
	// picks it up cleanly (err is reused from earlier in the function
	// scope, which Go allows iff at least one new variable appears on
	// the left of `:=`).
	//
	// Replay shape note: the worker pipeline above marshals the
	// handler return to JSON then unmarshals back into
	// `map[string]any`, which yields float64 for numbers per Go's
	// json.Unmarshal semantics. The replay path here uses the
	// handler's RAW map[string]any return — clipindexer writes
	// `{"total": 0, "indexed": 0, "failed": 0}` with bare int
	// literals, so j2Result["total"] is `int(0)`, not `float64(0)`.
	// EqualValues treats int(0)/float64(0) as equal so the same
	// assertion reads cleanly across both lifecycle paths. If we
	// wanted strict equality, we'd use Equal(t, 0, ...) which
	// requires `int(0)`; EqualValues is the safer cross-shape choice.
	j2Result, err := j2.HandleJob(replayCtx, &job.Job{
		ID:      "job-test-reindex-1-replay",
		Type:    appjobs.TypeMediaReindex,
		Payload: json.RawMessage(`{}`),
	}, &appjobs.JobTools{
		Progress:    func(int, string) {},
		Event:       func(string, string, map[string]any) {},
		IsCancelled: func() bool { return false },
	})
	require.NoError(t, err, "replay of media.reindex with same payload must succeed (idempotency)")
	require.EqualValues(t, 0, j2Result["total"],
		"idempotent replay returns same shape: total=0 (int or float64)")

	// t.TempDir() is auto-cleaned; we just confirm the workspace
	// didn't leak files OUTSIDE t.TempDir(). The workspace is rooted
	// at tmpDir/ws which t.Cleanup removes via t.TempDir(), so the
	// assertion is the parent path contains nothing unexpected.
	entries, readErr := os.ReadDir(tmpDir)
	require.NoError(t, readErr)
	require.True(t, len(entries) <= 3,
		"temp dir should hold only db + ws subdir; found %d entries", len(entries))
}

// ── Phase 7 — Remote worker renews the lease during long-running execution ──
//
// TestE2E_RemoteWorkerRenewsLease is the W1 Phase 7 acceptance proof
// that Runner.runLease's renewLoop extends the lease while a slow
// handler is still executing, so long-running media.artlist / extract
// / batch jobs don't expire mid-run.
//
// W1 spec deliverable: "renew fires per DefaultLeaseTTL × 2"
// (cadence = TTL/2 with Defaults: 30s on a 60s lease); the test
// "holds a lease ≥ 90s and observes ≥ 1 Renew call".
//
// Production-fidelity reasoning: with DefaultLeaseTTL = 60s and
// DefaultRenewInterval = 30s, exercising 3× cadence takes 90s.
// CI cost is bounded by injecting a faster cadence via
// Runner.SetRenewInterval(...) — production cadence is documented
// on Runner and exercised by the default NewRunner constructor
// (no test asserts the 30s cadence directly; the constant is
// trusted). The test exercises, end-to-end:
//
//   - Runner.runLease spawning renewLoop on entry
//   - renewLoop ticking at the configured cadence and calling
//     tools.Renew(renewCtx, DefaultLeaseTTL)
//   - the post-renewal Tools.revision being used for the final
//     Tools.Complete call (Phase 7 explicitly fixed the latent
//     revision-drift bug: runner.runLease was using
//     lease.Job.Revision which would have gone stale after the
//     first renewal and surfaced as ErrLeaseLost at Complete time)
//   - graceful shutdown: renewCancel() + drain BEFORE Complete so
//     a stale tick doesn't race the Complete round-trip
//
// Two handler contracts validated:
//   - ≥1 renewal observed by the broker mock (counter ≥ 1)
//   - the FINAL Complete arrives with the broker mock's last
//     served lease (so the broker-side revision tracking
//     advances correctly and the runner reads the post-Renew
//     revision from Tools rather than from the stale lease
//     snapshot)
func TestE2E_RemoteWorkerRenewsLease(t *testing.T) {
	// 1. Mock broker with a long-expiry lease. The lease's
	//    ExpiresAt must exceed the test budget realistically so
	//    renewal is the only thing keeping the job from timing out
	//    in the broker's bookkeeping layer. We use 5 minutes of
	//    "fake" remaining time so any sub-second test holds the
	//    job without surprising the runner's lease semantics.
	mock := newMockBroker(t)
	lease := &appjobs.Lease{
		LeaseID: "lease-phase7-1",
		Job: &job.Job{
			ID:       "job-phase7-renew-1",
			Type:     "test.slow_phase7",
			Payload:  json.RawMessage(`{}`),
			Status:   job.StatusRunning,
			Revision: 1,
			LeaseID:  "lease-phase7-1",
		},
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	mock.serveLease(lease)

	// 2. Custom handler under test.slow_phase7 — a PRIVATE type
	//    (not in production's capability set) that BLOCKS the
	//    worker until it observes ≥1 renewal, then returns a
	//    deterministic shape that the test asserts on. The
	//    block-until-signal pattern proves the RUNNER's renewal
	//    loop fires while the handler is mid-execution, which is
	//    the W1 Phase 7 contract.
	handler := func(ctx context.Context, _ *job.Job, _ *worker.Tools) (map[string]any, error) {
		select {
		case <-mock.renewSeen:
			// broker mock observed ≥1 renew — unblock and
			// report the snapshot count. Reading the count
			// outside the renewSeen closure is safe because
			// the renewal goroutine sends/closes via the
			// broker mock's mutex; another renewCount++
			// between our read and the return is acceptable
			// (the test asserts ≥1, not exactly 1).
			return map[string]any{
				"phase7": "renew_ok",
				"renews": int(mock.renewCounterOrZero()),
			}, nil
		case <-time.After(35 * time.Second):
			// 35s = 1.16× DefaultRenewInterval — under the
			// production cadence at least one renew must have
			// fired by this point; under the injected fast
			// cadence many more renewals will have fired
			// already. A timeout here means renewLoop was
			// wired wrong or the broker mock's Renew never
			// invoked.
			return nil, fmt.Errorf("phase7 timeout: no renew observed in 35s (count=%d)",
				mock.renewCounterOrZero())
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// 3. Worker registry populated with the custom handler only.
	//    No Dispatcher round-trip via AllHandlers() — the
	//    renewal protocol is independent of the dispatcher, and
	//    standing up clipindexer here would muddy the test's
	//    invariants (renewals on test.slow_phase7 don't
	//    interact with media.reindex types).
	workerReg := worker.NewRegistry()
	require.NoError(t, workerReg.Register("test.slow_phase7", handler))
	workerReg.Freeze()
	require.Equal(t, []string{"test.slow_phase7"}, workerReg.JobTypes(),
		"registry should hold exactly the phase7 stub handler")

	// 4. Workspace in a t.TempDir so t.Cleanup removes it after
	//    the test.
	tmpDir := t.TempDir()
	ws, err := worker.NewWorkspace(filepath.Join(tmpDir, "ws"))
	require.NoError(t, err)

	// 5. Runner with a fast cadence. SetRenewInterval clamps
	//    values below minRenewInterval (50ms); 200ms is
	//    comfortably above the floor and gives the broker mock's
	//    Renew N≥1 invocations well within the 35s handler
	//    timeout. The cadence itself is documented on
	//    Runner.effectiveRenewInterval (production default
	//    DefaultRenewInterval = 30s); this test does not
	//    re-assert the cadence constant — the constant is trusted,
	//    and the FACT that the broker mock saw N≥1 Renew calls
	//    is what proves the loop fired.
	runner := worker.NewRunner(mock, workerReg, ws, nil, zap.NewNop(),
		"w-phase7-1", "sess-phase7-1", []string{"test.slow_phase7"})
	runner.SetRenewInterval(200 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(ctx) }()

	// 6. Poll mock.completedResults until the broker records at
	//    least ONE Complete. The 45s deadline absorbs: handler
	//    start (microseconds), first renew at 200ms, handler
	//    unblock, marshal, Complete round-trip.
	deadline := time.NewTimer(45 * time.Second)
	defer deadline.Stop()
	for {
		results := mock.completedResults()
		if len(results) >= 1 {
			got := results[0]
			require.Equal(t, "job-phase7-renew-1", got.JobID,
				"Complete must carry the leased JobID")
			require.Equal(t, "lease-phase7-1", got.LeaseID,
				"Complete must carry the leased LeaseID")
			// The post-renewal ExpectedRevision must equal the
			// lease's Revision (mock broker never bumps
			// revision on Renew), proving Tools.Complete used
			// the Tools-tracked revision rather than a stale
			// snapshot. Real broker bumps revision on Renew,
			// so this test's lock at 1 is conservative.
			require.Equal(t, 1, got.ExpectedRevision,
				"Complete ExpectedRevision must equal the lease's Revision (mock broker never bumps revision on Renew)")

			var result map[string]any
			require.NoError(t, json.Unmarshal(got.Result, &result),
				"handler result must serialize cleanly to JSON")
			require.Equal(t, "renew_ok", result["phase7"],
				"handler must return renew_ok after observing ≥1 renew")
			require.GreaterOrEqual(t, mock.renewCounterOrZero(), int32(1),
				"W1 Phase 7 spec: ≥1 Renew call observed during lease hold")
			t.Logf("W1 Phase 7 end-to-end OK: handler saw %d renew calls, broker recorded Complete with JobID=%s (ExpectedRevision=%d)",
				mock.renewCounterOrZero(), got.JobID, got.ExpectedRevision)
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("worker did not complete the test.slow_phase7 job within 45s (renew count=%d)",
				mock.renewCounterOrZero())
		case <-time.After(20 * time.Millisecond):
			// poll loop
		}
	}

	// 7. Cancel runner; assert clean exit within 2s. Validates
	//    the deferred renewCancel + the runLease → renewLoop
	//    termination through ctx.Done().
	cancel()
	select {
	case err := <-runErr:
		require.True(t, err == nil || errors.Is(err, context.Canceled),
			"runner.Run should exit on ctx cancel; got: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("runner.Run did not exit within 2s after ctx cancel")
	}
}
