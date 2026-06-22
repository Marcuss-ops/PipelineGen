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
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/api/workers"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/jobbrokerclient"
	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/shared"
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
//   Prove that the remote worker, calling the broker via
//   `jobbrokerclient.New(...).RegisterWorker(...)`, reaches the
//   server's WorkerHandler mounted at `/internal/v1/workers/register`.
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
//   client.RegisterWorker(ctx, ...) hits /api/workers/register
//     → gin returns 404 "page not found"
//     → jobbrokerclient surfaces err = "HTTP 404: 404 page not found"
//     → require.NoError fails
//     → test prints a diagnostic pointing at the URL-alignment gap.
//
// Post-PR-B state (after the alignment PR merges):
//
//   client.RegisterWorker(ctx, ...) hits /internal/v1/workers/register
//     → gin routes to WorkerHandler.RegisterWorker
//     → handler invokes mockBroker.RegisterWorker
//     → mock returns session → handler responds 200 JSON
//     → require.NoError passes → require.True(mock.registerCalled) passes
//     → test PASSES.
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
	workerHandler := workers.NewInternalworkerHandler(mock, nil, zap.NewNop())
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
}

func newMockBroker(t *testing.T) *mockBroker { return &mockBroker{t: t} }

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
// deliberately unimplemented: any invocation through this smoke
// indicates the worker's pipeline is calling a method we did NOT
// assert. surface as test errors so the operator notices.

func (m *mockBroker) Claim(_ context.Context, _ appjobs.ClaimCommand) (*appjobs.Lease, error) {
	m.t.Errorf("mock broker: unexpected Claim (smoke test should only call RegisterWorker)")
	return nil, fmt.Errorf("not implemented in alignment-smoke mock")
}

func (m *mockBroker) Renew(_ context.Context, _ appjobs.RenewCommand) (*appjobs.Lease, error) {
	m.t.Errorf("mock broker: unexpected Renew (smoke test should only call RegisterWorker)")
	return nil, fmt.Errorf("not implemented in alignment-smoke mock")
}

func (m *mockBroker) Progress(_ context.Context, _ appjobs.ProgressCommand) error {
	m.t.Errorf("mock broker: unexpected Progress (smoke test should only call RegisterWorker)")
	return fmt.Errorf("not implemented in alignment-smoke mock")
}

func (m *mockBroker) Complete(_ context.Context, _ appjobs.CompleteCommand) error {
	m.t.Errorf("mock broker: unexpected Complete (smoke test should only call RegisterWorker)")
	return fmt.Errorf("not implemented in alignment-smoke mock")
}

func (m *mockBroker) Fail(_ context.Context, _ appjobs.FailCommand) error {
	m.t.Errorf("mock broker: unexpected Fail (smoke test should only call RegisterWorker)")
	return fmt.Errorf("not implemented in alignment-smoke mock")
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
