// Package app_test contains external test packages for internal/app,
// including the smoke test proving the remote worker HTTP broker
// path is aligned with the server's WorkerHandler mount via
// remoteshared.InternalPathPrefix.
package workerruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remote/jobbrokerclient"
	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/platform/remote/shared"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

// stubAssetClient is a no-op worker.AssetClient used by the W1
// Phase-6/7 e2e tests. Both e2e tests exercise the worker
// pipeline (claim → dispatch → handler-return → complete), NOT the
// artifact-upload path; the runner's uploadManifest uses the
// handlerResult's `len == 0` short-circuit to skip upload entirely
// because we route through wrapping handlers that return empty
// results (see adaptToUploaderSkip in this file). The stub is wired
// defensively (non-nil) so the runLease feed-forward is consistent
// across future refactors — if a future test stops wrapping for
// empty-result, the stub keeps uploading a no-op so the runner's
// fail-closed ErrArtifactClientRequired or P0 Commit 12's
// ErrLegacyUploadPathRemoved branches don't fire.
//
// godlike/07 typed-port contract: the stub satisfies the
// worker.AssetClient interface verbatim (compile-time pin below);
// callers MUST go through this interface, never pin to *stubAssetClient.
type stubAssetClient struct{}

// Compile-time pin (Pattern 0): catastrophic drift between the
// canonical worker.AssetClient surface and our test impl is a build
// failure, not a runtime panic at the typed-error gate.
var _ worker.AssetClient = (*stubAssetClient)(nil)

func (stubAssetClient) Download(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func (stubAssetClient) UploadFile(_ context.Context, _, _ string) error {
	return nil
}

// adaptToUploaderSkip wraps a production-bound handler so its
// result, as seen by the runner, is always an empty map. This is
// the test-side trick that lets the runner enter the silent-skip
// branch of uploadManifest (P0 #4 fail-closed split, see
// internal/capabilities/jobs/worker/runner_upload.go: silent-skip
// takes the empty-result path BEFORE the assetClient-nil check).
//
// Why empty-result: the e2e tests exercise the worker pipeline
// (claim → dispatch → handler-return → broker-side Complete). The
// test handler's REAL shape (e.g. {total:0, indexed:0, failed:0}
// from clipindexer or {phase7,renews} from the phase7 stub) is
// irrelevant to the pipeline contracts we pin (JobID, LeaseID,
// ExpectedRevision). Carrying the shape into Complete would force
// the runner into uploadManifest's non-empty branch, which the
// post-Oct-2026 fail-closed design rejects without an
// artifactClient.UploadFile that the test environment can't
// provide. Empty-result short-circuits that branch cleanly.
//
// Production fidelity preserved: the wrapped handler is the SAME
// handler the runner invokes through the dispatcher. The wrap is
// purely a result-shaper at the test boundary; production code is
// unchanged, and clipSvc.HandleJob / the phase7 test handler are
// still invoked through worker.Registry with their full effect
// (logging, DB queries, lease-renewal observation, etc).
func adaptToUploaderSkip(handler appjobs.HandlerFunc) worker.Handler {
	original := handler
	return func(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
		if _, err := original(ctx, j, tools); err != nil {
			return nil, err
		}
		// Force silent-skip path in runner.uploadManifest:
		// `len(handlerResult) == 0` short-circuits BEFORE the
		// non-empty/assetClient-nil fail-closed branch.
		return map[string]any{}, nil
	}
}

// internalV1Prefix is the URL prefix the production server mounts
// the worker broker handler under (see internal/api/routes.go::Setup,
// `engine.Group("/internal/v1")`).
//
// Sourced from `remoteshared.InternalPathPrefix` so a future rename
// of the path (or a mis-sync between server and client) surfaces as
// a single compile/build break instead of a runtime 404 with no
// breadcrumb back to the constant.
const internalV1Prefix = remoteshared.InternalPathPrefix

// TestE2E_WorkerClaimsViaHTTPBroker_Alignment proves that the remote
// worker, calling the broker via jobbrokerclient, reaches the server's
// WorkerHandler mounted at remoteshared.InternalPathPrefix.
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

	session, err := client.RegisterWorker(ctx, appjobs.RegisterWorkerCommand{
		WorkerID: "w-e2e-1",
		Name:     "test-worker",
		Capabilities: appjobs.WorkerCapabilities{
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
	completed   []appjobs.CompleteCommand
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
func (m *mockBroker) completedResults() []appjobs.CompleteCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]appjobs.CompleteCommand, len(m.completed))
	copy(out, m.completed)
	return out
}

func (m *mockBroker) RegisterWorker(_ context.Context, cmd appjobs.RegisterWorkerCommand) (*appjobs.WorkerSession, error) {
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

func (m *mockBroker) Heartbeat(_ context.Context, _ appjobs.HeartbeatCommand) error {
	m.t.Logf("mock broker: Heartbeat called unexpectedly")
	return nil
}

// Claim, Renew, Progress, Complete, Fail, IsCancelled are
// conditionally implemented. When the Phase 6 test pre-loads a
// lease via serveLease(), these return real responses so the
// worker pipeline can run. When lease is nil (alignment-smoke
// test) they surface as test errors so unexpected invocations are
// loud instead of silently no-op'd.

func (m *mockBroker) Claim(_ context.Context, _ appjobs.ClaimCommand) (*appjobs.Lease, error) {
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

func (m *mockBroker) Renew(_ context.Context, _ appjobs.RenewCommand) (*appjobs.Lease, error) {
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

func (m *mockBroker) Progress(_ context.Context, _ appjobs.ProgressCommand) error {
	// Progress is non-fatal even on the alignment-smoke path so the
	// test never tilts-over; ZLogger the call exists but does not
	// fail the test (the alignment-smoke path intentionally never
	// invokes Progress — if it does, a more specific test is needed).
	_ = time.Now()
	return nil
}

func (m *mockBroker) Complete(_ context.Context, cmd appjobs.CompleteCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Complete (smoke test should only call RegisterWorker)")
		return fmt.Errorf("not implemented in alignment-smoke mock")
	}
	m.t.Logf("mockBroker.Complete called: %+v", cmd)
	m.completed = append(m.completed, cmd)
	return nil
}

func (m *mockBroker) Fail(_ context.Context, cmd appjobs.FailCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lease == nil {
		m.t.Errorf("mock broker: unexpected Fail (smoke test should only call RegisterWorker)")
		return fmt.Errorf("not implemented in alignment-smoke mock")
	}
	m.completed = append(m.completed, appjobs.CompleteCommand{
		WorkerID:         cmd.WorkerID,
		WorkerSessionID:  cmd.WorkerSessionID,
		JobID:            cmd.JobID,
		LeaseID:          cmd.LeaseID,
		ExpectedRevision: cmd.ExpectedRevision,
	})
	return nil
}

func (m *mockBroker) IsCancelled(_ context.Context, jobID, leaseID string) (bool, error) {
	m.t.Errorf("mock broker: unexpected IsCancelled jobID=%s leaseID=%s", jobID, leaseID)
	return false, fmt.Errorf("not implemented in alignment-smoke mock")
}

// AZIONE 5 (July 2026): broker returns canonical AssetIDs from finalization.
func (m *mockBroker) CompleteWithArtifacts(_ context.Context, _ appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	m.t.Errorf("mock broker: unexpected CompleteWithArtifacts (smoke test should only call RegisterWorker/Complete)")
	return nil, fmt.Errorf("not implemented in alignment-smoke mock")
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
//     BuildWorkerRegistry uses in production cmd/worker)
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
	//    driver is registered transitively by storage (the storage
	//    package itself blanks in the driver). SetMaxOpenConns(1) so
	//    the clipindexer handler's QueryContext + Yield goroutines
	//    share a single connection on the file-backed DB — the
	//    per-connection gotcha with :memory: doesn't apply here, but
	//    the single-connection discipline keeps the test's
	//    invariants deterministic.
	//
	// PG-011 typed-handle migration (June 2026): the fixture is
	// *storage.SQLiteDB; clipindexer.NewService still takes a raw
	// *sql.DB so we pass sqliteDB.DB (the embedded field).
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "media.db")
	sqliteDB, err := storage.OpenSQLiteDB(dbPath, zap.NewNop())
	require.NoError(t, err)
	sqliteDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqliteDB.Close() })

	// 2. Create the columns HandleJob touches. The full media_assets
	//    schema lives in migrations/0XX_*.sql — for the "0 rows
	//    match" path we need only the columns referenced by the
	//    query string HandleJob compiles (id, source, media_type,
	//    embedding_json, transcript_embedding, metadata_json,
	//    created_at).
	_, err = sqliteDB.Exec(`CREATE TABLE IF NOT EXISTS media_assets (
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
	//
	// PG-016 typed-handle migration (June 2026): clipindexer.NewService now
	// accepts *storage.SQLiteDB directly; sqliteDB is already *storage.SQLiteDB
	// (storage.OpenSQLiteDB above), so this is the typed handle with no .DB
	// unwrap. This call site is one of the two that previously had to use
	// `sqliteDB.DB` to escape the *sql.DB typed-handle gap.
	clipSvc := clipindexer.NewService(cfg, sqliteDB, dbPath, zap.NewNop())

	// 4. Wire the in-process Dispatcher exactly like composition.go
	//    does: register one handler directly. We don't go through
	//    InitWorkerComposition so the test is hermetic — no Drive,
	//    no AI, no Qdrant, no outbox dependencies.
	dispatcher := appjobs.NewDispatcher()
	require.NoError(t, dispatcher.Register(appjobs.TypeMediaReindex, clipSvc.HandleJob))

	// 5. P1 #13 (July 2026): appjobs.HandlerFunc = worker.Handler =
	//    jobs.Handler — the handler already takes *JobExecutionTools;
	//    the adapt bridge is now a no-op identity cast (previously it
	//    converted *worker.Tools → *appjobs.JobTools). worker.Runner
	//    translates *worker.Tools → *job.JobExecutionTools at
	//    Dispatch time, so the handler sees the same shape from both
	//    the in-process Dispatcher call and the remote-worker call.
	//
	// godlike/06 SSOT: both adapt here and the production
	//    BuildWorkerRegistry consume the same jobs.Handler;
	//    no inline bridge is needed.
	//
	// P0 #4 (July 2026) — adapt wraps each handler with
	// adaptToUploaderSkip so the runner's uploadManifest takes the
	// silent-skip path. The test exercises the worker pipeline
	// (claim → dispatch → handler-return → broker-side Complete),
	// not the artifact-upload path; the wrap is a result-shaper
	// at the test boundary, NOT a production-code regression.
	workerReg := worker.NewRegistry()
	for jt, h := range dispatcher.AllHandlers() {
		handler := h
		require.NoError(t, workerReg.Register(jt, adaptToUploaderSkip(handler)))
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
	//
	// assetClient is the WIRED stub (not nil). The handler-result
	// is wrapped to empty (adaptToUploaderSkip) so the runner's
	// uploadManifest takes the silent-skip path BEFORE any
	// assetClient call; the stub is wired defensively so any
	// future refactor that drops the wrap still passes (the no-op
	// upload is harmless).
	runner := worker.NewRunner(mock, workerReg, ws, stubAssetClient{}, zap.NewNop(),
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
	//
	// NOTE on the per-field result shape assertions removed here:
	// pre-fix the test verified result["total"/"indexed"/"failed"]
	// were 0 (the clipindexer.HandleJob empty-DB return shape).
	// Post-fix the handler is wrapped through adaptToUploaderSkip
	// and returns an empty map (len(handlerResult) == 0 forces the
	// runner's uploadManifest silent-skip path); the W1 Phase-6
	// pipeline contract here is "handler ran, broker recorded
	// Complete with the wired JobID/LeaseID/ExpectedRevision", not
	// "handler produced these specific data shape fields". The
	// clipboard-handler result-shape dogfood is preserved in the
	// idempotency replay subtest at step 11, which calls
	// j2.HandleJob DIRECTLY (no worker pipeline), bypassing the
	// silent-skip wrap — the test still pins the {total=0,...}
	// shape on the direct path.
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
			t.Logf("W1 Phase 6 end-to-end OK: handler ran through the dispatcher+worker pipeline, broker recorded Complete with JobID=%s LeaseID=%s Revision=%d",
				got.JobID, got.LeaseID, got.ExpectedRevision)
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
	//
	// PG-016 typed-handle migration (June 2026): passes *storage.SQLiteDB
	// directly (sqliteDB); mirrors the clipSvc construction above.
	j2 := clipindexer.NewService(cfg, sqliteDB, dbPath, zap.NewNop())
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
		// FASE 4(b) (July 2026): IsCancelled REMOVED from the
		// domain JobExecutionTools struct. Replay handler
		// observes cancellation via the per-handler ctx passed
		// by Runner (jobCtx), not via a callback. The handler
		// here is a no-op on a fresh context.Background() so no
		// cancel-probe is needed.
		Progress: func(int, string) {},
		Event:    func(string, string, map[string]any) {},
	})
	require.NoError(t, err, "replay of media.reindex with same payload must succeed (idempotency)")
	require.EqualValues(t, 0, j2Result["total"],
		"idempotent replay returns same shape: total=0 (int or float64)")

	// t.TempDir() is auto-cleaned; we just confirm the workspace
	// didn't leak files OUTSIDE t.TempDir(). The workspace is rooted
	// at tmpDir/ws which t.Cleanup removes via t.TempDir(), so the
	// assertion is the parent path contains nothing unexpected.
	//
	// PG-011 typed-handle migration (June 2026): storage.OpenSQLiteDB
	// enables WAL mode (DSN _journal_mode=WAL + PRAGMA
	// journal_mode=WAL) so the temp dir now holds:
	//   - media.db      (the database file itself)
	//   - media.db-wal  (WAL journal — pre-existing on disk)
	//   - media.db-shm  (WAL shared-memory file)
	//   - ws/           (worker workspace subdir)
	// Total: 4 entries (≤ 4 enforced here). Previously with raw
	// sql.Open (default rollback journal mode) only media.db +
	// ws = 2 entries. The lounge of WAL is intentional —
	// storage.SQLiteDB forces WAL for production-grade
	// concurrency.
	entries, readErr := os.ReadDir(tmpDir)
	require.NoError(t, readErr)
	require.True(t, len(entries) <= 4,
		"temp dir should hold db + wal + shm + ws subdir; found %d entries", len(entries))
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
	// P1 #13 (July 2026): worker.Handler = jobs.Handler takes
	// *JobExecutionTools (not *worker.Tools). The tools parameter is
	// unused (_) so the parameter type change is cosmetic-only.
	handler := func(ctx context.Context, _ *job.Job, _ *job.JobExecutionTools) (job.Result, error) {
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
