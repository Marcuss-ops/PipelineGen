package videocreate

import (
	"context"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Per-stage sub-status emission (the durable stage table) ────────────
//
// The workflow ladder (model.go::WorkflowSteps) is the operator-facing
// stage machine; the kernel owns the CANONICAL stage vocabulary
// (internal/kernel/job/stage_progress.go: script | clips | stock |
// translation | voiceover | overlay | render | upload | persistence).
//
// Until now only the HTTP surface (PATCH /api/jobs/:id/stages/:stage) wrote
// the durable projection (job.JobStageStatusStore), so the stage table was
// populated by CALLERS, never by the work itself. This file makes
// video.create a PRODUCER of that table: every step transition the
// coordinator performs emits exactly one canonical stage row, so
// `GET /api/jobs/{id}/stages` reflects the workflow's real progress even when
// nobody reports it by hand.
//
// Emission is FUNNELED through progress.go's four transition helpers
// (stageStarted / stageSucceeded / stageSkipped / stageFailed), which are the
// single choke point coordinator.go calls — a new step cannot silently skip
// the durable report.

// stepCanonicalStage is the ONE step → canonical-stage mapping.
//
// It is MANY-TO-ONE on purpose: the kernel vocabulary is coarser than the
// workflow ladder, so several steps legitimately belong to the same canonical
// stage and share its row (the projection is an upsert on (job_id, stage), so
// the row always shows the LATEST producing step). The mapping is explicit and
// total — a reviewer can read off exactly which canonical stage each step is
// accountable for, and CanonicalStage returns false for anything unmapped so
// an unnamed stage can never be advertised.
//
// Rationale per row (why THIS canonical stage owns THIS step):
//
//	01_script        script      the script + scene plan IS the script stage
//	02_media_search  stock       media discovery searches the stock/asset plane
//	03_media_acquire clips       download + cut + normalize + register = clips
//	04_voiceover     voiceover   the voiceover stage proper
//	05_audio_master  voiceover   the audio master belongs to the voiceover stage
//	06_overlay_plan  overlay     the overlay layer plan
//	07_render        render      localized render fan-out
//	08_assemble      render      assembly completes the rendered artifact
//	09_audio_mux     voiceover   the final audio mux is the audio conclusion
//	10_verify        persistence ffprobe + SHA-256 = the artifact's durable proof
//	11_publish       upload      Drive publication + media-registry registration
//
// `translation` is deliberately ABSENT: the workflow ladder has no
// translation step (translation is a child-job projection owned by the
// script capability), and godlike/07 no-fake-availability forbids a stage row
// for work this workflow does not perform.
var stepCanonicalStage = map[Stage]job.StageName{
	StageScripting:    job.StageScript,
	StageMediaSearch:  job.StageStock,
	StageMediaAcquire: job.StageClips,
	StageVoiceover:    job.StageVoiceover,
	StageAudioMaster:  job.StageVoiceover,
	StageOverlayPlan:  job.StageOverlay,
	StageRendering:    job.StageRender,
	StageAssembling:   job.StageRender,
	StageAudioFinal:   job.StageVoiceover,
	StageVerifying:    job.StagePersistence,
	StageFinalizing:   job.StageUpload,
}

// CanonicalStage maps a workflow stage to the canonical kernel stage that owns
// its sub-status row, reporting false when the workflow stage has no canonical
// counterpart (in which case NO row is written — a stage nobody produces must
// not appear in the table).
func CanonicalStage(stage Stage) (job.StageName, bool) {
	canonical, ok := stepCanonicalStage[stage]
	if !ok || !canonical.Valid() {
		return "", false
	}
	return canonical, true
}

// stageStatusReporter writes the durable per-(job, stage) projection for one
// run. It is optional infrastructure: a nil reporter (no store wired, or a
// job-less test run) makes every method a no-op.
type stageStatusReporter struct {
	store job.JobStageStatusStore
	jobID string
	log   *zap.Logger
	now   func() time.Time
}

// newStageStatusReporter returns nil when there is nothing to report to, which
// is what keeps the emission path nil-safe instead of conditional at every
// call site.
func newStageStatusReporter(store job.JobStageStatusStore, jobID string, log *zap.Logger) *stageStatusReporter {
	if store == nil || jobID == "" {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &stageStatusReporter{store: store, jobID: jobID, log: log, now: time.Now}
}

// report upserts one canonical stage row.
//
// FAIL-SOFT is a contract, not a convenience: the stage projection is
// observability, so a store failure (locked database, cancelled context, a
// rejected row) must never fail or abort a render. Every failure path logs and
// returns. The same holds for a workflow stage with no canonical counterpart:
// it is skipped silently on purpose, because inventing a row for it would
// break the "one owner per fact" rule the vocabulary encodes.
func (r *stageStatusReporter) report(ctx context.Context, spec StepSpec, status job.StageStatus, progress int, detail string) {
	if r == nil || r.store == nil {
		return
	}
	stage, ok := CanonicalStage(spec.Stage)
	if !ok {
		return
	}
	rec := job.JobStageStatus{
		JobID:     r.jobID,
		Stage:     stage,
		Status:    status,
		Progress:  progress,
		Detail:    detail,
		UpdatedAt: r.now().UTC(),
	}
	if err := rec.Validate(); err != nil {
		r.log.Warn("videocreate stage status rejected",
			zap.String("job_id", r.jobID),
			zap.String("step", spec.StepKey),
			zap.String("stage", string(stage)),
			zap.Error(err))
		return
	}
	if err := r.store.UpsertJobStageStatus(ctx, rec); err != nil {
		r.log.Warn("videocreate stage status not persisted",
			zap.String("job_id", r.jobID),
			zap.String("step", spec.StepKey),
			zap.String("stage", string(stage)),
			zap.Error(err))
	}
}
