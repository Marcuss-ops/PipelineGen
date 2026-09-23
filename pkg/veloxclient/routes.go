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

import (
	"fmt"
	"net/url"
)

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
	// RouteJobsEnqueue enqueues a job directly on the admin surface.
	RouteJobsEnqueue = "/api/jobs"
	// RouteM2MJobs is the scoped remote submit surface. Its body is the
	// canonical EnqueueRequest envelope: {type, payload, ...}.
	RouteM2MJobs = "/api/v1/jobs"
	// RouteMediaSearch is the unified media search surface.
	RouteMediaSearch = "/api/media/search"
)

// Discovery / metadata endpoints (the remote material agent's read side).
const (
	// RouteClipsTopicSearch is GET /api/clips/search — LIVE YouTube discovery
	// and ranking for a keyword. Distinct from RouteMediaSearch, which
	// searches the already-registered local catalog.
	RouteClipsTopicSearch = "/api/clips/search"
	// RouteClipsInfo is GET /api/clips/info?url=... — full metadata for a
	// single YouTube URL without downloading it.
	RouteClipsInfo = "/api/clips/info"
	// RouteClipsExists is GET /api/clips/exists?url=... — the pre-extraction
	// dedup probe: answers {exists, clip_id} for a YouTube URL so an agent
	// skips /api/clips/process entirely for candidates already in the
	// catalog (0 downloads, 0 jobs, 0 rate-limit).
	RouteClipsExists = "/api/clips/exists"
	// RouteClipsTranscript is GET /api/clips/transcript?url=...&start=&end=
	// — transcript-as-a-service: readable text + per-cue timings for a
	// YouTube URL WITHOUT downloading the video or running Whisper
	// (yt-dlp --write-subs/--write-auto-subs --skip-download + the
	// canonical VTT parser server-side).
	RouteClipsTranscript = "/api/clips/transcript"
	// RouteClipsStock is POST /api/clips/stock — the clip-side stock ingest.
	RouteClipsStock = "/api/clips/stock"
	// RouteMediaClipsList is GET /api/media/clips/:source/clips — list rows
	// in a source's clip tree (the catalog browse arm).
	RouteMediaClipsList = "/api/media/clips/%s/clips"
	// RouteMediaRegisterBatch is POST /api/media/register-batch — the
	// wire-shape-only registration path for clips already on Drive. The
	// canonical way a remote adds material it produced itself.
	RouteMediaRegisterBatch = "/api/media/register-batch"
	// RouteMediaRegisterFromYouTube is POST /api/media/register-from-youtube.
	RouteMediaRegisterFromYouTube = "/api/media/register-from-youtube"
	// RouteMediaUploadVideo is POST /api/media/clips/upload-video — multipart
	// upload of a video file with metadata (the upload arm of self-import).
	RouteMediaUploadVideo = "/api/media/clips/upload-video"
)

// Stock-pipeline endpoints. NOTE: like RouteScriptGenerate, these are absent
// from the generated architecture/routes.yaml because the gen-api-docs
// composition does not mount the stock capability; routes_test.go therefore
// anchors them to the capability prefix registry instead of the manifest.
const (
	// RouteStockPipelineRun is POST /api/stock-pipeline/run (search_queries /
	// direct_urls / drive_urls / clips).
	RouteStockPipelineRun = "/api/stock-pipeline/run"
	// RouteStockPipelineSearchAndRun is POST
	// /api/stock-pipeline/search-and-run (queries:[{q,limit}] — NOT
	// search_queries, which is the legacy /run shape and fails closed here).
	RouteStockPipelineSearchAndRun = "/api/stock-pipeline/search-and-run"
)

// RouteJobsFull is GET /api/jobs/{id}/full — the canonical admin poll endpoint.
func RouteJobsFull(jobID string) string {
	return "/api/jobs/" + jobID + "/full"
}

// RouteM2MJobTypes is GET /api/v1/jobs/types, the scoped runnable-job catalog.
const RouteM2MJobTypes = "/api/v1/jobs/types"

// RouteM2MJob is GET /api/v1/jobs/{id}, the scoped remote poll endpoint.
func RouteM2MJob(jobID string) string {
	return "/api/v1/jobs/" + url.PathEscape(jobID)
}

// RouteClipsDownload is POST /api/media/clips/{source}/clips/{id}/download.
// POST-only: a GET returns 404, which is the mistake this constant prevents.
func RouteClipsDownload(source, clipID string) string {
	return fmt.Sprintf("/api/media/clips/%s/clips/%s/download", source, clipID)
}

// RouteMediaClipsFor builds GET /api/media/clips/{source}/clips.
func RouteMediaClipsFor(source string) string {
	return fmt.Sprintf(RouteMediaClipsList, source)
}

// RouteMediaResolve is GET /api/media/resolve/{asset_id} — resolve an asset
// id to its canonical record without searching.
func RouteMediaResolve(assetID string) string {
	return "/api/media/resolve/" + url.PathEscape(assetID)
}
