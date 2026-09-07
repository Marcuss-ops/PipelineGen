// Package app_test contains external test packages for internal/app,
// including the smoke test proving the remote worker HTTP broker
// path is aligned with the server's WorkerHandler mount via
// remoteshared.InternalPathPrefix.
package workerruntime_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/worker"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remote/jobbrokerclient"
	remoteshared "github.com/Marcuss-ops/PipelineGen/internal/platform/remote/shared"
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
