// Package e2e — scheduled_video_create_api_e2e_test.go.
//
// End-to-end proof of the deferred-scheduling lane THROUGH THE HTTP API:
//
//	POST /api/jobs (scheduled_at in the future)
//	  → SCHEDULED (persisted, listed by /api/jobs/scheduled, NOT claimable)
//	  → scheduler Tick (real store CAS + quota/lane admission)
//	  → QUEUED (+ job_queued event, gone from /api/jobs/scheduled)
//	  → the REAL worker (appjobs.Runner → Worker → Dispatcher) claims it
//	  → the handler reports per-stage sub-status through JobExecutionTools
//	  → GET /api/jobs/{id}/stages returns the populated stage table
//
// Everything durable is real: the SQLite jobs plane with the production
// migrations (including 005_job_scheduling.sql), the real enqueue/idempotency
// path, the real promotion CAS, the real claim, the real worker loop.
//
// The ONE substitution is the video.create WORKLOAD: the real ladder needs the
// LLM, ffmpeg/VeloxEditing and Drive planes, which cannot run in a unit e2e.
// The stub is registered on the real dispatcher and receives the SAME
// JobExecutionTools the real workflow receives — which is exactly the seam
// under test (the handler must be handed the durable stage sink). What the real
// ladder writes into that sink is pinned separately by
// internal/capabilities/videocreate/stage_status_test.go.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	jobscheduling "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"

	_ "github.com/mattn/go-sqlite3"
)

const schedE2EActiveKey = "e2e-day1-item1"

// schedE2EPost posts a JSON body and returns the status + decoded body.
func schedE2EPost(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp.StatusCode, decoded
}

// schedE2EGet fetches a URL and returns the status + decoded body.
func schedE2EGet(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var decoded map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp.StatusCode, decoded
}

// schedE2EJobStatus polls GET /api/jobs/{id} until the status satisfies want.
func schedE2EJobStatus(t *testing.T, baseURL, jobID string, want func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		code, body := schedE2EGet(t, baseURL+"/api/jobs/"+jobID)
		require.Equal(t, http.StatusOK, code, "GET /api/jobs/%s", jobID)
		last, _ = body["status"].(string)
		if want(last) {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("job %s never reached the expected status (last=%q)", jobID, last)
	return last
}

func TestScheduledVideoCreateThroughAPI(t *testing.T) {
	ctx := context.Background()
	log := zap.NewNop()
	dir := t.TempDir()

	// ── real storage plane, real migrations (incl. sqlite_jobs/005) ──
	set, err := storage.OpenSet(storage.StorageConfig{
		DataDir:    dir,
		JobsDBPath: filepath.Join(dir, "jobs", "jobs.db.sqlite"),
	}, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = set.Close() })
	require.NoError(t, set.Migrate(log), "jobs-plane migrations must apply (005_job_scheduling.sql included)")

	// ── real jobs capability: store → broker → service ──
	store := sqljobs.NewSQLiteStore(set.Jobs.DB, log)
	registry := appjobs.Compose()
	// The stub produces no artifacts; the real ladder does so through the media
	// planes. Disabling the artifact contract for THIS job type keeps the
	// scheduling + stage-table chain under test (the store's produces-artifacts
	// map is exactly the seam that says so).
	produces := registry.ProducesArtifactsMap()
	produces[appjobs.TypeVideoCreate] = false
	store.SetProducesArtifacts(produces)

	dispatcher := appjobs.NewDispatcher()
	svc, err := appjobs.NewService(sqljobs.NewBroker(store), dispatcher, log, registry)
	require.NoError(t, err)
	require.NotNil(t, svc.ScheduleStore(), "the SQLite jobs plane must expose the scheduling port")
	require.NotNil(t, svc.StageStatusStore(), "the SQLite jobs plane must expose the stage-status port")

	// ── the workload stub: reports stages through the tools it is handed ──
	var (
		mu       sync.Mutex
		reported []job.StageName
	)
	require.NoError(t, dispatcher.Register(appjobs.TypeVideoCreate, appjobs.HandlerFunc(
		func(handlerCtx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
			if tools == nil || tools.StageStatus == nil {
				return nil, fmt.Errorf("worker did not hand the durable stage sink to the handler")
			}
			for _, stage := range []job.StageName{job.StageScript, job.StageVoiceover, job.StageRender} {
				if err := tools.StageStatus.UpsertJobStageStatus(handlerCtx, job.JobStageStatus{
					JobID: j.ID, Stage: stage, Status: job.StageRunning, Progress: 50, UpdatedAt: time.Now().UTC(),
				}); err != nil {
					return nil, fmt.Errorf("report %s: %w", stage, err)
				}
				mu.Lock()
				reported = append(reported, stage)
				mu.Unlock()
			}
			if err := tools.StageStatus.UpsertJobStageStatus(handlerCtx, job.JobStageStatus{
				JobID: j.ID, Stage: job.StageUpload, Status: job.StageCompleted, Progress: 100, UpdatedAt: time.Now().UTC(),
			}); err != nil {
				return nil, fmt.Errorf("report upload: %w", err)
			}
			return job.Result{}, nil
		})))

	// ── the real HTTP surface ──
	gin.SetMode(gin.TestMode)
	handler := appjobs.NewJobsHandler(svc, svc, log)
	handler.SetScheduling(svc.ScheduleStore(), svc.StageStatusStore())
	engine := gin.New()
	handler.RegisterRoutes(engine.Group("/api/jobs"))
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)

	// ── 1. submit a DEFERRED job through the API ──
	runAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	body := fmt.Sprintf(
		`{"type":"video.create","payload":{"topic":"e2e scheduled","language":"en","duration_seconds":30},"scheduled_at":%q,"active_key":%q}`,
		runAt.Format(time.RFC3339), schedE2EActiveKey)

	code, resp := schedE2EPost(t, srv.URL+"/api/jobs", body)
	require.Equal(t, http.StatusAccepted, code, "submit: %v", resp)
	jobID, _ := resp["job_id"].(string)
	require.NotEmpty(t, jobID)

	// 2. it is SCHEDULED, not QUEUED
	code, got := schedE2EGet(t, srv.URL+"/api/jobs/"+jobID)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, string(job.StatusScheduled), got["status"], "a future scheduled_at must persist SCHEDULED")

	// 3. it shows up in the scheduled backlog and is NOT yet due
	code, got = schedE2EGet(t, srv.URL+"/api/jobs/scheduled")
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 1, got["count"], "the deferred job must be listed: %v", got)
	require.EqualValues(t, 0, got["due"], "a 2h-future job must not be due")

	// 4. the real claim path must refuse it: SCHEDULED is not claimable
	claimed, err := store.ClaimNext(ctx, "e2e-probe", 30*time.Second, []string{appjobs.TypeVideoCreate})
	require.NoError(t, err)
	require.Nil(t, claimed, "a SCHEDULED job must never be handed to a worker before promotion")

	// 5. replay the same submission: same active_key → the SAME job, no duplicate
	code, replay := schedE2EPost(t, srv.URL+"/api/jobs", body)
	require.Equal(t, http.StatusAccepted, code, "replay: %v", replay)
	require.Equal(t, jobID, replay["job_id"], "an idempotent replay must return the SAME (scheduled) job_id")
	_, got = schedE2EGet(t, srv.URL+"/api/jobs/scheduled")
	require.EqualValues(t, 1, got["count"], "the replay must not add a second scheduled job: %v", got)

	// ── 6. promotion: nothing happens before the due time ──
	sched, err := jobscheduling.NewScheduler(jobscheduling.SchedulerDeps{
		Store:      svc.ScheduleStore(),
		Policy:     jobscheduling.AdmissionPolicy{DailyQuota: 5000, MaxConcurrent: 1},
		Now:        func() time.Time { return time.Now() }, // real clock: not due yet
		BatchLimit: 10,
		Log:        log,
	})
	require.NoError(t, err)

	res, err := sched.Tick(ctx)
	require.NoError(t, err)
	require.Zero(t, res.Promoted, "the scheduler must not promote a job before its run_at (%+v)", res)

	// 7. and it is promoted once the clock passes run_at (injected clock stands
	//    in for the wall clock advancing; the store CAS is the real one)
	dueSched, err := jobscheduling.NewScheduler(jobscheduling.SchedulerDeps{
		Store:      svc.ScheduleStore(),
		Policy:     jobscheduling.AdmissionPolicy{DailyQuota: 5000, MaxConcurrent: 1},
		Now:        func() time.Time { return runAt.Add(time.Second) },
		BatchLimit: 10,
		Log:        log,
	})
	require.NoError(t, err)

	res, err = dueSched.Tick(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.Promoted, "the due job must be promoted exactly once (%+v)", res)

	// 8. QUEUED, event recorded, backlog drained
	require.Equal(t, string(job.StatusQueued), schedE2EJobStatus(t, srv.URL, jobID,
		func(s string) bool { return s != string(job.StatusScheduled) }))
	code, got = schedE2EGet(t, srv.URL+"/api/jobs/scheduled")
	require.Equal(t, http.StatusOK, code)
	require.EqualValues(t, 0, got["count"], "a promoted job leaves the scheduled backlog: %v", got)

	code, got = schedE2EGet(t, srv.URL+"/api/jobs/"+jobID+"/events")
	require.Equal(t, http.StatusOK, code)
	events, _ := json.Marshal(got)
	require.Contains(t, string(events), "job_queued", "promotion must be visible in the timeline: %s", events)

	// ── 9. the REAL worker claims and executes it ──
	runner := appjobs.NewRunner(store, dispatcher, log, appjobs.RunnerConfig{
		Workers:   1,
		JobTypes:  []string{appjobs.TypeVideoCreate},
		LeaseTTL:  30 * time.Second,
		PollEvery: 25 * time.Millisecond,
		// The real wake-on-enqueue port: the SQLite jobs plane implements
		// kernel/job.QueueNotifier, exactly as production wires it.
		Notifier: store,
	}).WithRegistry(registry)

	runCtx, cancelRun := context.WithCancel(ctx)
	t.Cleanup(cancelRun)
	go runner.Start(runCtx)

	require.Equal(t, string(job.StatusSucceeded), schedE2EJobStatus(t, srv.URL, jobID, func(s string) bool {
		return s == string(job.StatusSucceeded) || s == string(job.StatusFailed)
	}), "the worker must drive the promoted job to a terminal state")

	// ── 10. the stage table is populated, readable through the API ──
	code, got = schedE2EGet(t, srv.URL+"/api/jobs/"+jobID+"/stages")
	require.Equal(t, http.StatusOK, code, "GET stages: %v", got)
	require.EqualValues(t, 4, got["count"], "the handler's stage rows must be readable: %v", got)

	stages, ok := got["stages"].([]any)
	require.True(t, ok, "stages payload shape: %v", got)
	byStage := map[string]map[string]any{}
	for _, raw := range stages {
		row, ok := raw.(map[string]any)
		require.True(t, ok)
		name, _ := row["stage"].(string)
		byStage[name] = row
	}
	for _, want := range []job.StageName{job.StageScript, job.StageVoiceover, job.StageRender} {
		row, ok := byStage[string(want)]
		require.True(t, ok, "stage %s missing from the table: %v", want, byStage)
		require.Equal(t, string(job.StageRunning), row["status"])
	}
	require.Equal(t, string(job.StageCompleted), byStage[string(job.StageUpload)]["status"])
	require.EqualValues(t, 100, byStage[string(job.StageUpload)]["progress"])

	mu.Lock()
	gotStages := append([]job.StageName(nil), reported...)
	mu.Unlock()
	require.ElementsMatch(t, []job.StageName{job.StageScript, job.StageVoiceover, job.StageRender}, gotStages)
}
