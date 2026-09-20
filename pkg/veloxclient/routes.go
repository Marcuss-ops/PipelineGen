// routes.go — the ONE place the client-side wire paths live.
//
// The drift this closes: the same endpoint was spelled out by hand in the CLI,
// in the README and in agent rules, so a route change (or an additive change to
// its body, like ACK-only → ACK+job_id) had to be chased across every copy and
// the copies silently fell behind. Server routes are generated into
// architecture/routes.yaml by cmd/admin/gen_api_docs.go; routes_test.go asserts
// every constant below still appears there, so a server-side rename fails a
// test instead of failing at runtime.
package veloxclient

import "fmt"

// Static async endpoints.
const (
	// RouteClipsProcess extracts YouTube clips (async; returns job_id).
	RouteClipsProcess = "/api/clips/process"
	// RouteClipsRender renders one canonical clip (async clip.render job).
	RouteClipsRender = "/api/clips/render"
	// RouteClipsRenderBatch renders up to 50 clips (async clip.render jobs).
	RouteClipsRenderBatch = "/api/clips/render/batch"
	// RouteScriptGenerate generates a script (async).
	RouteScriptGenerate = "/api/script/generate"
	// RouteJobsEnqueue enqueues a job directly.
	RouteJobsEnqueue = "/api/jobs"
	// RouteMediaSearch is the unified media search surface.
	RouteMediaSearch = "/api/media/search"
)

// RouteJobsFull is GET /api/jobs/{id}/full — the canonical poll endpoint.
func RouteJobsFull(jobID string) string {
	return "/api/jobs/" + jobID + "/full"
}

// RouteClipsDownload is POST /api/media/clips/{source}/clips/{id}/download.
// POST-only: a GET returns 404, which is the mistake this constant prevents.
func RouteClipsDownload(source, clipID string) string {
	return fmt.Sprintf("/api/media/clips/%s/clips/%s/download", source, clipID)
}
