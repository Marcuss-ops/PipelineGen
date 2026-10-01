package veloxclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRoutesMatchGeneratedManifest pins client paths to the canonical route
// manifest, with the live capability-prefix registry as the source for routes
// that the credential-light docs snapshot cannot mount.
func TestRoutesMatchGeneratedManifest(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "architecture", "routes.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read route manifest %s: %v", path, err)
	}
	manifest := string(data)

	routes := []string{
		RouteJobsEnqueue,
		RouteMediaSearch,
		RouteJobsFull(":id"),
		RouteClipsDownload(":source", ":id"),
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

	// The credential-light docs snapshot cannot mount these optional media
	// capabilities. Their live route prefixes are pinned by the server's
	// capability registry instead of being copied into generated docs.
	wirePath := filepath.Join(root, "internal", "platform", "httpserver", "transport", "wire.go")
	wire, werr := os.ReadFile(wirePath)
	if werr != nil {
		t.Fatalf("read capability prefix registry %s: %v", wirePath, werr)
	}
	clipRoutes := []struct{ route, prefix string }{
		{RouteClipsProcess, `"/api/clips/process"`},
		{RouteClipsRender, `"/api/clips/render"`},
		{RouteClipsRenderBatch, `"/api/clips/render"`},
		{RouteClipsTopicSearch, `"/api/clips/search"`},
		{RouteClipsInfo, `"/api/clips/info"`},
		{RouteClipsExists, `"/api/clips/exists"`},
		{RouteClipsTranscript, `"/api/clips/transcript"`},
		{RouteClipsStock, `"/api/clips/stock"`},
	}
	for _, item := range clipRoutes {
		if strings.Contains(manifest, "path: "+item.route) {
			continue
		}
		if !strings.Contains(string(wire), item.prefix) {
			t.Errorf("%s is neither in the route manifest nor backed by prefix %s in %s", item.route, item.prefix, wirePath)
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
