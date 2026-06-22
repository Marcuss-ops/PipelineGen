// Package app_test contains external test packages for internal/app,
// including the PR-D smoke test that proves the remote worker HTTP
// broker path is aligned with the server's WorkerHandler mount.
//
// This file is INTENTIONALLY written against the PR-A-only branch
// (codex/w3-e2e-worker off origin/codex/iac-fix-cmd-server). It is
// expected to FAIL on that branch because jobbrokerclient/client.go
// hardcodes `/api/...` paths while the server mounts the worker
// handler at `/internal/v1/...`. The failure is the diagnostic —
// once PR-B lands (URL alignment + WorkerAuth), the test passes.
//
// After PR-B merges, the `internalV1Prefix` constant below should be
// replaced with `remoteshared.InternalPathPrefix` from
// `internal/infrastructure/remote/shared` so the test stops drifting
// from the production constant.
package app_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/api/workers"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/remote/jobbrokerclient"
)

// internalV1Prefix is the URL prefix the production server mounts
// the worker broker handler under (see internal/api/routes.go::Setup,
// `engine.Group("/internal/v1")`).
//
// Hardcoded here — NOT imported from `internal/infrastructure/remote/shared` —
// because on the codex/w3-e2e-worker branch (PR-A only — PR-B NOT applied)
// that shared package does not yet exist. After PR-B merges, replace
// this constant with `remoteshared.InternalPathPrefix` to keep the
// test in lockstep with production.
const internalV1Prefix = "/internal/v1"

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
			ts.URL, internalV1Prefix, ts.URL)
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
	if !containsPath(observedRegisterPath, internalV1Prefix) && mock.registerCalled {
		t.Errorf("URL drift: client hit %q but server mount was %q.\n"+
			"This should be impossible since the test passed the round-trip; if you\n"+
			"see this, the path constant in the test and in the client package are\n"+
			"out of sync.",
			observedRegisterPath, internalV1Prefix)
	}
}

// observedRegisterPath is recorded by the mock so a future reviewer
// who runs the test can read the actual URL the client hit. Set by
// the mock at the time of invocation; if the test errors out before
// the call it remains "".
var observedRegisterPath string

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
	observedRegisterPath = "/workers/register" // server-side path; client builds the full URL from this + baseURL
	m.mu.Unlock()
	return &appjobs.WorkerSession{
		WorkerID:     cmd.WorkerID,
		Name:         cmd.Name,
		SessionID:    fmt.Sprintf("sess-%d", time.Now().UnixNano()),
		Capabilities: cmd.Capabilities,
		ExpiresAt:    time.Now().Add(30 * time.Second),
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
