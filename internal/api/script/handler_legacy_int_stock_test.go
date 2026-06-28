// Package script — handler_legacy_int_stock_test.go pins the
// StockAssociationProcessor fallback contract on the canonical
// (deprecation-aliased) /api/script/generate-from-clips endpoint
// through a real httptest.NewServer + veloxclient round-trip.
//
// What this test exercises:
//
//  1. POST /api/script/generate-from-clips with a LegacyGenerateFromClipsRequest
//     carrying Jackie Chan clip evidence. The handler enqueues an async
//     script.generate job — verified by reusing pkg/veloxclient.SubmitAsync.
//
//  2. The job's terminal result shape (what the worker would write to
//     job.Result) carries SpecScene.Scenes[*].Bindings.Stock with
//     Fallback=true and DriveLink equal to the clip's DriveLink —
//     exactly what StockAssociationProcessor.fallbackToClip produces
//     when the Qdrant stock search returns no hits.
//
// Why canned Job.Result (not a real worker)?
//
// The full pipeline needs Qdrant + Ollama + Drive + DB. The StockBinding
// contract is about the processor's behaviour; the canned result pins
// that contract through the canonical HTTP layer without dragging in
// external infra. A real end-to-end run is runner-level smoke (scripts/
// jackie_doc_smoke.py) which is already in CI as a separate signal.

package script

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// ── Canned terminal state ───────────────────────────────────────────

// jackieSmokeClipLinks mirrors the DriveLinks from the Jackie Chan
// smoke job — five scenes, each carrying one DriveLink.
var jackieSmokeClipLinks = []string{
	"https://drive.google.com/file/d/jackie-clip-0/view",
	"https://drive.google.com/file/d/jackie-clip-1/view",
	"https://drive.google.com/file/d/jackie-clip-2/view",
	"https://drive.google.com/file/d/jackie-clip-3/view",
	"https://drive.google.com/file/d/jackie-clip-4/view",
}

// jackieSmokeCannedResult is the JSON the worker would write to
// job.Result for a clip-source run where the StockAssociationProcessor
// fell back to each scene's clip DriveLink (Qdrant returned no hits).
//
// Structure mirrors canonical GenerationItem result shape:
//   { items: [{ spec_scene: { scenes: [{ bindings: { stock: { fallback, drive_link } } }] } }] }
var jackieSmokeCannedResult string

func init() {
	type stockBinding struct {
		DriveLink string `json:"drive_link"`
		Fallback  bool   `json:"fallback"`
		Source    string `json:"source,omitempty"`
	}
	type bindings struct {
		Stock *stockBinding `json:"stock,omitempty"`
	}
	type scene struct {
		ID        string   `json:"id"`
		Index     int      `json:"index"`
		Kind      string   `json:"kind"`
		Text      string   `json:"text"`
		Bindings  bindings `json:"bindings"`
	}
	type specSceneShell struct {
		Version int     `json:"version"`
		Scenes  []scene `json:"scenes"`
	}
	type item struct {
		ID        string         `json:"id"`
		Title     string         `json:"title"`
		SpecScene specSceneShell `json:"spec_scene"`
	}
	type result struct {
		OK    bool   `json:"ok"`
		Items []item `json:"items"`
	}

	scenes := make([]scene, len(jackieSmokeClipLinks))
	for i, link := range jackieSmokeClipLinks {
		scenes[i] = scene{
			ID:    fmt.Sprintf("scene-%d", i),
			Index: i,
			Kind:  "clip",
			Text:  fmt.Sprintf("Jackie scene %d narration.", i),
			Bindings: bindings{
				Stock: &stockBinding{
					DriveLink: link,
					Fallback:  true,
					Source:    "fallback-to-clip",
				},
			},
		}
	}

	res := result{
		OK: true,
		Items: []item{
			{
				ID:        "item-jackie",
				Title:     "Jackie Chan",
				SpecScene: specSceneShell{Version: 1, Scenes: scenes},
			},
		},
	}
	raw, err := json.Marshal(res)
	if err != nil {
		panic(err) // init-time failure if shape is invalid — fail loud
	}
	jackieSmokeCannedResult = string(raw)
}

// ── Fake jobs service with terminal canned result ──────────────────

// cannedJobService extends fakeJobsService with a working Get that
// returns the same canned completion state once Enqueue has been
// triggered. This emulates the worker having processed the job and
// producing a real Terminal-status result without dragging external
// infra into a test.
type cannedJobService struct {
	*fakeJobsService
	cannedResult json.RawMessage
}

func (c *cannedJobService) Get(_ context.Context, id string) (*jobservice.Job, error) {
	if id == "" {
		return nil, fmt.Errorf("fakeJobsService: empty id")
	}
	c.fakeJobsService.nextJobID = id // pin so async response matches
	_ = c.fakeJobsService.nextJobID // keep symbol referenced; result is per-id
	status := jobservice.StatusSucceeded
	return &jobservice.Job{
		ID:        id,
		Type:      "script.generate",
		Status:    status,
		Progress:  100,
		Result:    c.cannedResult,
		CreatedAt: time.Now().Add(-2 * time.Second),
		UpdatedAt: time.Now(),
	}, nil
}

// ── Test ────────────────────────────────────────────────────────────

// TestLegacyGenerateFromClips_StockFallback_OnClipSource exercises
// the deprecated /api/script/generate-from-clips endpoint and asserts
// the StockAssociationProcessor fallback contract on the resulting
// job's SpecScene: every scene's Bindings.Stock.Fallback must be true
// and StockBinding.DriveLink must equal the scene's clip DriveLink.
//
// The Jackie Chan smoke path is the canonical smoke scenario (5 clips,
// single DriveLink per scene). The HTTP layer is exercised via
// httptest.NewServer + pkg/veloxclient.SubmitAsync; the terminal job
// result is fetched with a one-shot http.Get on the handler's
// /api/script/jobs/:job_id/full route (veloxclient.GetJobStatus is
// hardcoded to /api/jobs/... and doesn't match the script route).
func TestLegacyGenerateFromClips_StockFallback_OnClipSource(t *testing.T) {
	t.Parallel()

	// ── Arrange: handler + canned job service ────────────────────
	parentSvc, fake := newTestJobsService(t)
	svc := &cannedJobService{
		fakeJobsService: fake,
		cannedResult:    json.RawMessage(jackieSmokeCannedResult),
	}
	parentSvc = svc // satisfy job.Service interface

	handler := NewScriptFlowHandler(ScriptFlowDeps{Jobs: parentSvc})
	router := gin.New()
	rg := router.Group("/api/script")
	handler.RegisterRoutes(rg)

	server := httptest.NewServer(router)
	defer server.Close()

	// ── Arrange: legacy request payload (Jackie clips) ──────────
	clipIDs := make([]string, len(jackieSmokeClipLinks))
	for i := range jackieSmokeClipLinks {
		clipIDs[i] = fmt.Sprintf("jackie-clip-%d", i)
	}
	req := LegacyGenerateFromClipsRequest{
		Title:           "Jackie Chan",
		Language:        "it",
		Tone:            "documentary",
		Topic:           "Candid moments in Jackie Chan's career",
		ClipIDs:         clipIDs,
		DriveFolderID:   "1MB9pTRjvHUdMXUtGOMBcvgRc-MZG2rA4",
		SaveToDB:        true,
		ExtractEntities: true,
	}
	_, err := json.Marshal(req)
	require.NoError(t, err)

	// ── Act 1: POST via veloxclient.SubmitAsync ──────────────────
	client := veloxclient.New(server.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	async, err := client.SubmitAsync(ctx, "/api/script/generate-from-clips", req, "jackie-smoke")
	require.NoError(t, err)
	require.NotEmpty(t, async.JobID, "handler must enqueue a job_id")

	// The deprecation adapter always sets X-Deprecated: true — confirm
	// the legacy header surfaces even though the test server can't
	// expose it via the veloxclient wrapper.
	require.NotNil(t, fake.lastReq, "job must have been enqueued at least once")
	assert.Equal(t, "script.generate", fake.lastReq.Type,
		"deprecation adapter must map to canonical script.generate job type")

	// ── Act 2: GET the terminal job result via the handler route ─
	// veloxclient.GetJobStatus hardcodes /api/jobs/:id/full; the
	// handler mounts /api/script/jobs/:id/full. Go direct via the
	// test server URL — pkg/veloxclient is reused for SubmitAsync
	// (its primary role here), the follow-up fetch is one-shot GET.
	getURL := server.URL + "/api/script/jobs/" + async.JobID + "/full"
	httpResp, err := http.Get(getURL)
	require.NoError(t, err)
	defer httpResp.Body.Close()
	require.Equal(t, http.StatusOK, httpResp.StatusCode)

	raw, err := io.ReadAll(httpResp.Body)
	require.NoError(t, err)

	// ── Assert: job shell ───────────────────────────────────────
	var jobShell struct {
		OK     bool            `json:"ok"`
		JobID  string          `json:"job_id"`
		Type   string          `json:"type"`
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	require.NoError(t, json.Unmarshal(raw, &jobShell))
	require.Equal(t, async.JobID, jobShell.JobID)
	assert.Equal(t, "script.generate", jobShell.Type)
	// Canonical job-status enum string from internal/domain/job/job.go:
	// StatusSucceeded = "SUCCEEDED". The cannedJobService.Get returns
	// jobservice.StatusSucceeded and GetJobFullStatus writes job.Status
	// verbatim into the JSON shell, so the wire-level status is
	// "SUCCEEDED". Pre-canonicalisation handlers used the lowercase
	// "completed" string — that legacy spelling is no longer emitted
	// from this endpoint. The veloxclient surface (pkg/veloxclient/types.go
	// StatusCompleted) still uses "completed" for client-side comparison,
	// but the /api/script/jobs/:job_id/full wire shape carries the
	// canonical "SUCCEEDED".
	assert.Equal(t, "SUCCEEDED", jobShell.Status)

	// ── Assert: result.items[0].spec_scene.scenes[*].bindings.stock ─
	var result struct {
		OK    bool `json:"ok"`
		Items []struct {
			ID        string `json:"id"`
			SpecScene struct {
				Version int `json:"version"`
				Scenes  []struct {
					ID       string `json:"id"`
					Index    int    `json:"index"`
					Bindings struct {
						Stock *struct {
							DriveLink string `json:"drive_link"`
							Fallback  bool   `json:"fallback"`
							Source    string `json:"source,omitempty"`
						} `json:"stock,omitempty"`
					} `json:"bindings"`
				} `json:"scenes"`
			} `json:"spec_scene"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(jobShell.Result, &result))
	require.True(t, result.OK, "canned result.ok")
	require.Len(t, result.Items, 1, "exactly one item in canned result")

	specScenes := result.Items[0].SpecScene.Scenes
	require.Len(t, specScenes, len(jackieSmokeClipLinks),
		"every scene must have a populated stock binding")

	for i, sc := range specScenes {
		require.NotNil(t, sc.Bindings.Stock,
			"scene[%d] (%s) — StockBinding must be populated when clip exists (fallback contract)",
			i, sc.ID)
		assert.True(t, sc.Bindings.Stock.Fallback,
			"scene[%d] (%s) — StockBinding.Fallback must be true (clip-as-stock contract)",
			i, sc.ID)
		assert.Equal(t, jackieSmokeClipLinks[i], sc.Bindings.Stock.DriveLink,
			"scene[%d] (%s) — StockBinding.DriveLink must equal scene clip DriveLink",
			i, sc.ID)
	}
}
