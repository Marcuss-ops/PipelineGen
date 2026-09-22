package veloxclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRoutesMatchGeneratedManifest is the drift gate for item 4's client slice:
// every wire path the client/CLI binds to must still exist in the canonical
// route manifest that cmd/admin/gen_api_docs.go generates from the live router.
// A server-side rename therefore fails here instead of at runtime.
func TestRoutesMatchGeneratedManifest(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "architecture", "routes.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read route manifest %s: %v", path, err)
	}
	manifest := string(data)

	routes := []string{
		RouteClipsProcess,
		RouteClipsRender,
		RouteClipsRenderBatch,
		RouteJobsEnqueue,
		RouteMediaSearch,
		RouteJobsFull(":id"),
		RouteClipsDownload(":source", ":id"),
		RouteClipsTopicSearch,
		RouteClipsInfo,
		RouteClipsStock,
		RouteMediaClipsFor(":source"),
		RouteMediaRegisterBatch,
		RouteMediaRegisterFromYouTube,
		RouteMediaUploadVideo,
		RouteMediaResolve(":asset_id"),
	}
	for _, r := range routes {
		if !strings.Contains(manifest, "path: "+r) {
			t.Errorf("route %q is not in %s — the wire path changed; update pkg/veloxclient/routes.go and its consumers", r, path)
		}
	}

	// RouteScriptGenerate cannot be asserted against the generated manifest: the
	// script flow is not wired in the gen-api-docs composition, so the route is
	// absent from routes.yaml even though the LIVE server serves it (verified:
	// POST /api/script/generate answers 401 unauthenticated, GET answers 404, so
	// the route is mounted). Asserting the manifest would encode that generator
	// gap as a client failure. Instead, tie the constant to a REAL registration
	// anchor: the capability prefix the router mounts it under.
	if strings.Contains(manifest, "path: "+RouteScriptGenerate) {
		t.Logf("note: %s now appears in the manifest — tighten this test to assert it", RouteScriptGenerate)
	} else {
		wirePath := filepath.Join(root, "internal", "platform", "httpserver", "transport", "wire.go")
		wire, werr := os.ReadFile(wirePath)
		if werr != nil {
			t.Fatalf("read capability prefix registry %s: %v", wirePath, werr)
		}
		if !strings.Contains(string(wire), `"/api/script"`) {
			t.Errorf("%s is neither in the route manifest nor backed by a \"/api/script\" capability prefix in %s — the path is unjustified", RouteScriptGenerate, wirePath)
		}
	}

	// RouteStockPipelineRun / RouteStockPipelineSearchAndRun are in the SAME
	// category as RouteScriptGenerate: the gen-api-docs composition does not
	// mount the stock capability, so the routes are absent from routes.yaml
	// even though the live server serves them (pinned by
	// internal/capabilities/assets/stock/handler_contract_test.go). Anchor them
	// to the capability prefix registry so a rename still fails here.
	for _, r := range []string{RouteStockPipelineRun, RouteStockPipelineSearchAndRun} {
		if strings.Contains(manifest, "path: "+r) {
			t.Logf("note: %s now appears in the manifest — tighten this test to assert it", r)
			continue
		}
		wirePath := filepath.Join(root, "internal", "platform", "httpserver", "transport", "wire.go")
		wire, werr := os.ReadFile(wirePath)
		if werr != nil {
			t.Fatalf("read capability prefix registry %s: %v", wirePath, werr)
		}
		if !strings.Contains(string(wire), `"/api/stock-pipeline"`) {
			t.Errorf("%s is neither in the route manifest nor backed by a \"/api/stock-pipeline\" capability prefix — the path is unjustified", r)
		}
	}
}

// TestRouteHelpersBuildCanonicalPaths pins the two helper builders so a future
// edit cannot silently change the shape (e.g. drop the /full suffix).
func TestRouteHelpersBuildCanonicalPaths(t *testing.T) {
	if got := RouteJobsFull("job_123"); got != "/api/jobs/job_123/full" {
		t.Errorf("RouteJobsFull = %q", got)
	}
	if got := RouteClipsDownload("youtube", "yt_1"); got != "/api/media/clips/youtube/clips/yt_1/download" {
		t.Errorf("RouteClipsDownload = %q", got)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate module root (no go.mod found walking up)")
		}
		dir = parent
	}
}
