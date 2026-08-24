// Package script — handler_run_full.go owns the REFERENCE-only
// enriched-job-status implementation (GetFullJobRun). The endpoint it
// was originally built for, GET /api/script/jobs/{id}/full, was
// RETIRED from the ScriptFlow module surface per WAVE-22-C2-E SSOT
// (architecture/ownership/modules.yaml): its canonical owner is the
// Jobs module under /api/jobs/:id/full. Mounting both copies would
// violate godlike/06 (one canonical owner per fact).
//
// WHY RETAINED (godlike/05 minimum-blast-radius): GetFullJobRun +
// the runRepo field + the supporting builder helpers
// (buildFullRunResponse, buildStageStatusMap, convertScenesToView,
// convertDocumentsToView, convertLanguageMap, deriveStageFromJobStatus)
// are kept here as a reference implementation. Re-mounting is
// trivial if SSOT policy changes (1-line addition to RegisterJobRoutes
// + handler_test.go update), and the wire shape already serves as the
// test fixture for sibling surfaces.
//
// RE-MOUNT CONDITIONS (governance gate): to re-mount this under
// /api/script, all of the following must hold atomically:
//  1. architecture/ownership/modules.yaml::ScriptFlow has been
//     updated to admit /jobs/:id/full (godlike/06 SSOT update), with
//     the Jobs entry for /api/jobs/:id/full explicitly retired to
//     avoid the dual-mount that triggered this retention.
//  2. handler_test.go::TestScriptRoutes_Compatibility expectedRoutes
//     map has been updated to admit the route (assert.Equal exact-match
//     gate would otherwise RED).
//  3. The PR passes `make regen-routes-yaml` and the AST walker
//     includes GET /api/script/jobs/:id/full as a ScriptFlow route
//     in architecture/ownership.generated.yaml (machine-consumed).
//
// Reference wire shape (matches /api/jobs/:id/full mesh):
//
//	{
//	  "ok": true,
//	  "job_id": "...",
//	  "job": { ... },
//	  "status": "RUNNING",
//	  "current_stage": "GENERATING_SCENE_TEXT",
//	  "stages": {
//	    "NORMALIZING": "completed",
//	    "GENERATING_SCENE_TEXT": "running",
//	    "TRANSLATING_SCENES": "pending",
//	    ...
//	  },
//	  "scenes": [ ... ],
//	  "documents": { "en": { "id": "...", "link": "..." } },
//	  "render_job": { "job_id": "...", "status": "..." },
//	  "word_count": 450,
//	  "error_code": "",
//	  "error_message": "",
//	  "failed_stage": "",
//	  "attempt_count": 0,
//	  "next_retry_at": null
//	}
package script

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// ── Per-stage status helpers ─────────────────────────────────────────

// stageStatus represents the execution status of a single pipeline stage.
type stageStatus string

const (
	stagePending   stageStatus = "pending"
	stageRunning   stageStatus = "running"
	stageCompleted stageStatus = "completed"
	stageFailed    stageStatus = "failed"
	stageSkipped   stageStatus = "skipped"
)

// ── Response types ───────────────────────────────────────────────────

// FullJobRunResponse is the canonical wire shape for
// GET /api/script/jobs/{id}/full.
//
// STATUS (WAVE-22-C2-E SSOT, July 2026): the canonical HTTP path
// that surfaces this shape is /api/jobs/:id/full (Jobs module).
// This type remains exported so test fixtures and any future wire
// can construct the same shape.
type FullJobRunResponse struct {
	OK     bool   `json:"ok"`
	JobID  string `json:"job_id"`
	Job    any    `json:"job,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Result any    `json:"result,omitempty"`

	// Pipeline state.
	CurrentStage string                 `json:"current_stage"`
	Stages       map[string]stageStatus `json:"stages"`

	// Generation artifacts.
	Scenes    []SceneView             `json:"scenes,omitempty"`
	Documents map[string]DocumentView `json:"documents,omitempty"`
	WordCount int                     `json:"word_count,omitempty"`

	// Failure metadata.
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	FailedStage  string     `json:"failed_stage,omitempty"`
	AttemptCount int        `json:"attempt_count"`
	NextRetryAt  *time.Time `json:"next_retry_at,omitempty"`
}

// SceneView is the public wire representation of a scene.
type SceneView struct {
	ID        string                   `json:"id"`
	Index     int                      `json:"index"`
	Text      map[string]string        `json:"text"`
	Clip      *scriptgen.ClipReference `json:"clip,omitempty"`
	Voiceover map[string]VoiceoverView `json:"voiceover,omitempty"`
}

// VoiceoverView is the public wire representation of a voiceover.
type VoiceoverView struct {
	ID       string  `json:"id"`
	URL      string  `json:"url,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

// DocumentView is the public wire representation of a document.
type DocumentView struct {
	ID   string `json:"id"`
	Link string `json:"link"`
}

// ── Enriched GetFullJobRun handler (reference-only, unmounted per SSOT) ─

// GetFullJobRun handles GET /api/script/jobs/{id}/full.
//
// STATUS (WAVE-22-C2-E SSOT, July 2026): INTENTIONALLY UNMOUNTED from
// the ScriptFlow module surface — see handler_run_full.go header doc
// for the full audit narrative. The canonical owner of /jobs/:id/full
// is the Jobs module under /api/jobs/:id/full; the 4-way SSOT lock
// (architecture/ownership/modules.yaml + handler_test.go exact-match
// + routes.yaml AST regen) gates the mount. GetFullJobRun is retained
// as a reference implementation only; future wires that NEED this
// shape (e.g. a per-pipeline enriched view) should import it via
// `pkg/apiutil` or similar promoted-out helper, NOT by re-mounting
// the same path twice.
//
// Returns enriched job status including scenes, translations,
// voiceovers, documents, render data, and per-stage status.
//
// The handler reads the kernel job from the jobs service and the
// GenerationRun from the run repository (when available). The two
// are correlated by JobID.
func (jh *JobsHandler) GetFullJobRun(c *gin.Context) {
	if jh.jobsSvc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "jobs service not initialized")
		return
	}

	jobID := strings.TrimSpace(c.Param("id"))
	if jobID == "" {
		apiutil.BadRequest(c, "job id is required")
		return
	}

	// Read the kernel job.
	j, err := jh.jobsSvc.Get(c.Request.Context(), jobID)
	if err != nil {
		apiutil.NotFound(c, fmt.Sprintf("job not found: %v", err))
		return
	}

	// Read the GenerationRun from the run repository (correlated by JobID).
	var run *scriptgen.GenerationRun
	if jh.runRepo != nil {
		run, _ = jh.runRepo.GetByJobID(c.Request.Context(), jobID)
	}

	// Build response.
	resp := buildFullRunResponse(j, run)
	c.JSON(http.StatusOK, resp)
}

// buildFullRunResponse assembles the enriched response from the kernel
// job and the optional GenerationRun.
func buildFullRunResponse(j *job.Job, run *scriptgen.GenerationRun) FullJobRunResponse {
	resp := FullJobRunResponse{
		OK:     true,
		JobID:  j.ID,
		Status: string(j.Status),
	}

	if j != nil {
		resp.Job = gin.H{
			"id":     j.ID,
			"type":   j.Type,
			"status": j.Status,
		}
	}
	if j.Error != "" {
		resp.Error = j.Error
	}
	if j.Result != nil {
		resp.Result = j.Result
	}

	// Populate pipeline state from the GenerationRun.
	if run != nil {
		resp.CurrentStage = string(run.CurrentStage)
		resp.Stages = buildStageStatusMap(run)
		resp.ErrorCode = run.ErrorCode
		resp.ErrorMessage = run.ErrorMessage
		resp.FailedStage = string(run.FailedStage)
		resp.AttemptCount = run.AttemptCount
		resp.NextRetryAt = run.NextRetryAt

		// Populate generation artifacts from the run result.
		if run.Result != nil {
			resp.Scenes = convertScenesToView(run.Result.Scenes)
			resp.Documents = convertDocumentsToView(run.Result.Documents)
			resp.WordCount = run.Result.WordCount
		}
	} else {
		// No run found — derive current_stage from job status.
		resp.CurrentStage = deriveStageFromJobStatus(j)
		resp.Stages = map[string]stageStatus{
			"SUBMITTED":      stageCompleted,
			string(j.Status): stageRunning,
		}
	}

	return resp
}

// buildStageStatusMap computes per-stage status from the run's
// current stage and failure info. Stages before the current stage
// are marked completed; the current stage is running; stages after
// it are pending. If the run failed, the failed stage is marked
// failed and subsequent stages are skipped.
func buildStageStatusMap(run *scriptgen.GenerationRun) map[string]stageStatus {
	// Order: all pipeline stages in canonical execution order.
	allStages := []scriptgen.Stage{
		scriptgen.StageNormalizing,
		scriptgen.StageGeneratingSceneText,
		scriptgen.StageTranslatingScenes,
		scriptgen.StageGeneratingVoiceovers,
		scriptgen.StageCompilingAudio,
		scriptgen.StagePublishingDocuments,
	}

	stages := make(map[string]stageStatus, len(allStages)+2)

	if run.Status == scriptgen.RunStatusCompleted {
		// All stages completed.
		for _, s := range allStages {
			stages[string(s)] = stageCompleted
		}
		return stages
	}

	stageIndex := func(stage scriptgen.Stage) int {
		for i, s := range allStages {
			if s == stage {
				return i
			}
		}
		return -1
	}

	currentIdx := stageIndex(run.CurrentStage)
	failedIdx := -1
	if run.FailedStage != "" {
		failedIdx = stageIndex(run.FailedStage)
	}

	for i, s := range allStages {
		stageStr := string(s)
		if run.Status == scriptgen.RunStatusFailed && failedIdx >= 0 {
			if i < failedIdx {
				stages[stageStr] = stageCompleted
			} else if i == failedIdx {
				stages[stageStr] = stageFailed
			} else {
				stages[stageStr] = stageSkipped
			}
		} else {
			if i < currentIdx {
				stages[stageStr] = stageCompleted
			} else if i == currentIdx {
				stages[stageStr] = stageRunning
			} else {
				stages[stageStr] = stagePending
			}
		}
	}

	return stages
}

// convertScenesToView converts internal Scene types to public views.
// Each scene's Text map is converted to string keys, and Voiceover
// map is enriched with the audio reference data.
func convertScenesToView(scenes []scriptgen.Scene) []SceneView {
	if len(scenes) == 0 {
		return nil
	}

	views := make([]SceneView, len(scenes))
	for i, s := range scenes {
		v := SceneView{
			ID:    s.ID,
			Index: s.Index,
			Text:  convertLanguageMap(s.Text),
		}
		if s.Clip != nil {
			v.Clip = s.Clip
		}
		if len(s.Voiceover) > 0 {
			v.Voiceover = make(map[string]VoiceoverView, len(s.Voiceover))
			for lang, ar := range s.Voiceover {
				v.Voiceover[string(lang)] = VoiceoverView{
					ID:       ar.ID,
					URL:      ar.URL,
					Duration: ar.Duration,
				}
			}
		}
		views[i] = v
	}
	return views
}

// convertDocumentsToView converts the internal Documents map to
// public wire-safe DocumentView values.
func convertDocumentsToView(docs map[scriptgen.Language]scriptgen.DocumentReference) map[string]DocumentView {
	if len(docs) == 0 {
		return nil
	}

	views := make(map[string]DocumentView, len(docs))
	for lang, doc := range docs {
		views[string(lang)] = DocumentView{
			ID:   doc.ID,
			Link: doc.Link,
		}
	}
	return views
}

// convertLanguageMap converts map[Language]string to map[string]string
// for JSON serialization.
func convertLanguageMap(m map[scriptgen.Language]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}

// deriveStageFromJobStatus maps a job status string to a readable
// stage name when no GenerationRun is available.
func deriveStageFromJobStatus(j *job.Job) string {
	switch j.Status {
	case job.StatusQueued:
		return "QUEUED"
	case job.StatusLeased:
		return "RUNNING"
	case job.StatusRunning:
		return "RUNNING"
	case job.StatusWaitingChildren:
		return "WAITING_CHILDREN"
	case job.StatusFinalizing:
		return "FINALIZING"
	case job.StatusSucceeded:
		return "COMPLETED"
	case job.StatusPartiallySucceeded:
		return "PARTIALLY_SUCCEEDED"
	case job.StatusFailed:
		return "FAILED"
	case job.StatusCancelled:
		return "CANCELLED"
	default:
		return string(j.Status)
	}
}
