package cliprender

// worker.go is the canonical Master job handler for clip.render.
//
// Pipeline:
//
//	decode payload → Normalize + Validate → parallel preparation
//	(Preparer) → compile ASS artifact (when subtitles enabled) →
//	compile + seal ClipRenderPlanV1 → RenderingGen/Chronon render.
//
// The renderer and publisher remain mandatory at execution time: a plan
// that is only sealed or only rendered locally is never reported as a
// successful clip.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// ErrRenderPhaseNotImplemented is retained for the fail-closed case where a
// composition root exposes the job without attaching a render executor.
var ErrRenderPhaseNotImplemented = errors.New("clip.render: render executor is not wired")

// ErrInvalidJobPayload is the typed sentinel for an undecodable job payload.
// Terminal: retrying the same payload can never succeed.
var ErrInvalidJobPayload = errors.New("clip.render: invalid job payload")

// Worker is the canonical clip.render job handler. It is constructed with
// the Preparer and bound to the Master via
// job.Service.RegisterHandler(TypeClipRender, job.HandlerFunc(worker.Handle)).
type Worker struct {
	preparer             *Preparer
	workspaceDir         string
	subtitles            SubtitleCompiler          // optional until the ASS-compiler step wires it
	renderer             RenderExecutor            // optional until the render-phase step consumes it
	asyncCompletion      bool                      // enabled when durable Submit/Settle wiring is attached
	continuationStore    ContinuationStore         // required for the async submit/settle path
	continuationEnqueuer ContinuationEnqueuer      // required for the async submit/settle path
	publisher            RenderPublisher           // optional in unit tests; required by production wiring
	folderResolver       DestinationFolderResolver // optional: required only when a request carries destination.subfolder_name
	overlayResolver      OverlaySegmentResolver    // required when a request declares an overlay
	outputProber         OutputProber              // probes actual bytes for exact contract validation
	log                  *zap.Logger
}

// NewWorker constructs the canonical worker. Fail-closed: preparer and log
// are mandatory; workspaceDir is the scratch root for run artifacts
// (rendered-clip.mp4 + subtitles.ass land under workspaceDir/runs/<run-id>/).
func NewWorker(preparer *Preparer, workspaceDir string, log *zap.Logger) (*Worker, error) {
	if preparer == nil {
		return nil, fmt.Errorf("cliprender.NewWorker: Preparer is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Worker{preparer: preparer, workspaceDir: workspaceDir, log: log}, nil
}

func (w *Worker) Handle(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (job.Result, error) {
	progress := safeProgress(tools)
	emit := safeEvent(tools)

	progress(0, "clip.render started")
	jobStart := time.Now()
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseSubmitted, jobStart, jobStart, kernobs.StageStatusCompleted, nil)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseClaimed, jobStart, jobStart, kernobs.StageStatusCompleted, nil)
	// Per-phase progress is a Debug trace: the canonical operator events for a
	// clip are accepted (clip.render.job.start), completed
	// (clip.render.job.completed) and failed. Emitting one Info line per phase
	// here cost ~6 lines per clip for facts the RunReport already owns.
	w.log.Debug("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "start"),
		zap.String("job_id", j.ID),
	)

	phase, continuation, err := decodeContinuationPayload(j.Payload)
	if err != nil {
		return nil, err
	}
	var (
		req               RenderRequest
		prepared          *Prepared
		plan              ClipRenderPlanV1
		subtitleArtifact  *SubtitleArtifact
		publishFolderID   string
		subtitleCompileMS int64 = -1
	)
	if phase == RenderPhaseSettle {
		if w.continuationStore == nil {
			return nil, fmt.Errorf("clip.render: settle phase requires a ContinuationStore")
		}
		doc, loadErr := w.continuationStore.GetResumeDocument(ctx, continuation.Resume)
		if loadErr != nil {
			return nil, fmt.Errorf("clip.render: load continuation: %w", loadErr)
		}
		if attrErr := doc.Attributes(continuation.Submission.RenderJobID, continuation.Submission.PlanSHA256); attrErr != nil {
			return nil, fmt.Errorf("clip.render: continuation binding: %w", attrErr)
		}
		req = doc.Request
		req.Normalize()
		if err := req.Validate(); err != nil {
			return nil, fmt.Errorf("%w: resume request: %v", ErrInvalidJobPayload, err)
		}
		plan = doc.Plan
		subtitleArtifact = doc.Subtitles
		publishFolderID = doc.PublishFolderID
		prepared = preparedFromResume(doc)
	} else {
		if err := json.Unmarshal(j.Payload, &req); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
		}
		req.Normalize()
		if err := req.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJobPayload, err)
		}
	}

	w.log.Info("clip.render.job.start",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("job_id", j.ID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.String("destination_folder_id", req.Destination.DriveFolderID),
		zap.Bool("subtitles_enabled", req.Subtitles.Enabled),
		zap.Bool("watermark_requested", req.Watermark != nil),
		zap.Bool("background_mode", req.Background.Mode != ""),
		zap.Bool("overlay_requested", req.Overlay != nil),
		zap.Bool("require_gpu", req.Execution.RequireGPU),
		zap.Bool("require_zero_copy", req.Execution.RequireZeroCopy),
	)
	// ── Destination folder resolution (ONCE per job, never in the publisher) ──
	// When the request carries destination.subfolder_name (a script/batch
	// identity all its clips share), the worker resolves the leaf folder
	// create-or-reuse through the canonical DestinationFolderResolver and
	// hands the publisher the fully-resolved leaf ID. The publisher is dumb
	// by contract: it never creates folders. Clips of the same batch carry
	// the same subfolder_name, so each job resolves the same folder and they
	// converge on one shared Drive directory. Without a subfolder_name the
	// request's destination.drive_folder_id is already the resolved leaf and
	// is passed through verbatim.
	if phase == RenderPhaseSettle {
		// Destination resolution and all preparation already happened before
		// Submit. Repeating it here would re-download assets and can select a
		// different Drive folder after a restart.
		publishFolderID = continuationResumeFolder(publishFolderID, req.Destination.DriveFolderID)
	} else {
		publishFolderID = req.Destination.DriveFolderID
		if strings.TrimSpace(req.Destination.SubfolderName) != "" {
			if w.folderResolver == nil {
				return nil, fmt.Errorf("clip.render: destination.subfolder_name=%q declared but no DestinationFolderResolver is wired (the publisher must never create folders)", req.Destination.SubfolderName)
			}
			resolveStart := time.Now()
			resolvedID, resolveErr := w.folderResolver.ResolveDestinationFolder(ctx, DestinationFolderResolveInput{
				RootFolderID:  req.Destination.DriveFolderID,
				SubfolderName: req.Destination.SubfolderName,
			})
			kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipDestinationResolve}, resolveStart, time.Now(), resolveErr)
			if resolveErr != nil {
				return nil, fmt.Errorf("clip.render: resolve destination subfolder %q: %w", req.Destination.SubfolderName, resolveErr)
			}
			if strings.TrimSpace(resolvedID) == "" {
				return nil, fmt.Errorf("clip.render: destination subfolder %q resolved to an empty folder ID", req.Destination.SubfolderName)
			}
			publishFolderID = resolvedID
			w.log.Debug("clip.render.job.destination_resolved",
				zap.String("subsystem", "clip_render_worker"),
				zap.String("job_id", j.ID),
				zap.String("root_folder_id", req.Destination.DriveFolderID),
				zap.String("subfolder_name", req.Destination.SubfolderName),
				zap.String("resolved_folder_id", resolvedID),
			)
		}
	}
	if phase == RenderPhaseSettle {
		progress(90, "remote render submitted; settling certified artifact")
	} else {
		progress(10, "request validated; running parallel preparation")
		prepared, plan, subtitleArtifact, subtitleCompileMS, err = w.preparePlan(ctx, &req, j.ID, emit)
		if err != nil {
			return nil, err
		}
	}

	emit("clip.render.plan.sealed", "ClipRenderPlanV1 sealed — fully resolved before Chronon", map[string]any{
		"plan_version": plan.Version,
		"plan_sha256":  plan.PlanSHA256,
		"output_path":  plan.OutputPath,
		"source":       plan.Source.Path,
		"subtitles":    plan.Subtitles != nil,
		"watermark":    plan.Watermark != nil,
		"background":   plan.Background.Mode,
	})
	if w.renderer == nil {
		result := renderedResult(j, &req, prepared, plan, subtitleArtifact, nil, nil)
		result["phase"] = "plan_sealed"
		return result, fmt.Errorf(
			"%w: job_id=%s source_asset_id=%s plan_sha256=%s",
			ErrRenderPhaseNotImplemented, j.ID, req.SourceAssetID, plan.PlanSHA256)
	}
	if phase == RenderPhaseSubmit {
		asyncRenderer, asyncOK := w.renderer.(AsyncRenderExecutor)
		if asyncOK {
			if w.continuationStore == nil || w.continuationEnqueuer == nil {
				return nil, fmt.Errorf("clip.render: asynchronous executor is wired but continuation store/enqueuer is missing")
			}
			doc := ResumeDocument{
				Plan:            plan,
				Request:         req,
				PublishFolderID: publishFolderID,
				SourceTitle:     prepared.Source.Title,
				SourceSizeBytes: prepared.Source.SizeBytes,
				Contract:        prepared.Contract,
				Transcript:      prepared.Transcript,
				Subtitles:       subtitleArtifact,
			}
			resumeRef, storeErr := w.continuationStore.PutResumeDocument(ctx, doc)
			if storeErr != nil {
				return nil, fmt.Errorf("clip.render: persist continuation: %w", storeErr)
			}
			submission := Submission{
				RenderJobID:   plan.RunID,
				PlanSHA256:    plan.PlanSHA256,
				CorrelationID: j.CorrelationID,
				State:         RemoteRenderSubmitted,
				Attempt:       1,
			}
			if submitErr := asyncRenderer.Submit(ctx, plan); submitErr != nil {
				return nil, fmt.Errorf("clip.render: submit remote render: %w", submitErr)
			}
			continuation := Continuation{Submission: submission, Resume: resumeRef}
			childID, enqueueErr := w.continuationEnqueuer.EnqueueContinuation(ctx, ContinuationRequest{
				ParentJobID:  j.ID,
				ParentRunID:  j.ID,
				ActiveKey:    ActiveKeyFor(plan.RunID, submission.Attempt),
				Continuation: continuation,
			})
			if enqueueErr != nil {
				return nil, fmt.Errorf("clip.render: enqueue settle continuation: %w", enqueueErr)
			}
			result := renderedResult(j, &req, prepared, plan, subtitleArtifact, nil, nil)
			result["phase"] = "submitted"
			result["parent_state"] = ParentStateWaitingChildren
			result["child_job_id"] = childID
			result["render_submission"] = submission
			result["continuation"] = continuation
			progress(100, "remote render submitted; Master slot released")
			return result, nil
		}
	}
	if phase == RenderPhaseSettle {
		if _, ok := w.renderer.(AsyncRenderExecutor); !ok {
			return nil, fmt.Errorf("clip.render: settle phase requires an AsyncRenderExecutor")
		}
	}

	progress(90, "plan sealed; rendering with Chronon")
	// render_slot is the interval this Master job HOLDS its render slot. It is
	// measured from the renderer invocation to the materialized artifact, i.e.
	// the real occupancy the slot pool/gate bounds — never a zero-width marker
	// (the previous start==end form measured nothing and reported 0 ms of slot
	// wait for every render).
	renderSlotStart := time.Now()
	renderStart := time.Now()
	w.log.Debug("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "render_start"),
		zap.String("job_id", j.ID),
		zap.String("plan_sha256", plan.PlanSHA256),
		zap.String("output_path", plan.OutputPath),
	)
	// The render boundary is both a stage (wall, owner-measured anchors) and
	// an operation (chronon.render_clip accumulated work) on the RunReport, so
	// the benchmark can compare render WALL against render WORK exactly like
	// the script.generate phases.
	outcome, err := func() (o *RenderOutcome, e error) {
		e = kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     StageClipRender,
			Component: kernobs.ComponentName("chronon"),
			Operation: kernobs.OperationName("render_clip"),
		}, func(opCtx context.Context) error {
			var rErr error
			if phase == RenderPhaseSettle {
				o, rErr = w.renderer.(AsyncRenderExecutor).Settle(opCtx, plan)
			} else {
				o, rErr = w.renderer.Render(opCtx, plan)
			}
			return rErr
		})
		return o, e
	}()
	renderEnd := time.Now()
	renderMS := renderEnd.Sub(renderStart).Milliseconds()
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseRenderSlot, renderSlotStart, renderEnd, kernobs.StageStatusCompleted, nil)
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipRender}, renderStart, renderEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseFFmpeg, renderStart, renderEnd, kernobs.StageStatusCompleted, err)
	if err != nil {
		w.log.Error("clip.render.job.render_failed",
			zap.String("job_id", j.ID),
			zap.Int64("duration_ms", renderMS),
			zap.Error(err),
		)
		return nil, fmt.Errorf("clip.render: render plan: %w", err)
	}
	w.log.Debug("clip.render.job.phase",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("phase", "render_done"),
		zap.String("job_id", j.ID),
		zap.String("backend", string(outcome.Backend)),
		zap.Int64("duration_ms", renderMS),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.Int64("size_bytes", outcome.SizeBytes),
		zap.String("output_path", outcome.OutputPath),
	)
	if outcome == nil || outcome.OutputPath == "" || outcome.SizeBytes <= 0 {
		return nil, fmt.Errorf("clip.render: renderer returned an invalid output")
	}
	// Fail-closed GPU gate: a request that demands GPU must never be silently
	// served by the software fallback. The only GPU backend is Chronon (only
	// when certified by the host gate); the PATH B CUDA hybrid was removed —
	// GPU compositing belongs exclusively to the Chronon executor.
	// ExecutionSpec.RequireGPU is enforced here as its documented contract
	// (RenderBackend.IsGPUBackend is the single authority of "GPU-ness"),
	// checked BEFORE the unconditional Chronon-only gate so a non-GPU outcome
	// on a require_gpu request reports the specific violation.
	if req.Execution.RequireGPU && !outcome.Backend.IsGPUBackend() {
		return nil, fmt.Errorf("clip.render: execution.require_gpu=true but resolved backend %q is not a GPU backend", outcome.Backend)
	}
	if outcome.Backend != BackendChrononVulkan {
		return nil, fmt.Errorf("clip.render: backend resolved to %q; only Chronon (%s) is permitted", outcome.Backend, BackendChrononVulkan)
	}
	// RequireZeroCopy is fail-closed by construction: no backend certifies
	// video_zero_copy anymore (the hybrid that reported it was removed, and
	// the RenderingGen/Chronon artifact never certifies it over this
	// transport). A caller that demands it gets a typed error — never a
	// silent downgrade.
	if req.Execution.RequireZeroCopy {
		return nil, fmt.Errorf("clip.render: execution.require_zero_copy=true is unsatisfiable: no backend certifies video_zero_copy (GPU compositing is Chronon-only via the RenderingGen queue)")
	}
	// Fold the worker-measured phases into the adapter's V2 report (real
	// instrumentation only — a disabled phase stays NOT_INSTRUMENTED). The
	// job-level total is set at the end of the run, where the final wall time
	// is known, so unaccounted_ms spans preparation + selection + render +
	// publish exactly like the benchmark report.
	if outcome.Metrics == nil {
		outcome.Metrics = NewRenderMetricsV2()
	}
	// render_wall_ms: the worker's own wall around the render port call
	// (backend selection + execution). This is the honest render WALL the
	// benchmark needs to compare against the render WORK (the summed
	// startup/composite/encode phases): TotalMS is later overwritten with
	// the job-level total, so without this field the render wall would be
	// lost and the wall-vs-work distinction would be unanswerable.
	outcome.Metrics.RenderWallMS = Metric(renderMS)
	if subtitleCompileMS >= 0 {
		outcome.Metrics.SubtitleCompileMS = Metric(subtitleCompileMS)
	}
	// asset_materialize_ms: the preparer already tracks every materialize
	// phase (materialize_source/watermark/background) with real wall times;
	// fold their sum into the report so the benchmark can attribute the
	// "bring the assets to disk" cost (Drive downloads) instead of leaving it
	// in the unaccounted gap. No materialize phase recorded → stays
	// NOT_INSTRUMENTED.
	if assetMS := materializeWallMS(prepared.Timings); assetMS >= 0 {
		outcome.Metrics.AssetMaterializeMS = Metric(assetMS)
	}
	// The adapter normally derives frames from the outcome's media facts; a
	// boundary that returns a report without frames still gets the count
	// derived here from the same sealed facts (never a fake number).
	if outcome.Metrics.Frames == 0 && outcome.FPSNum > 0 && outcome.FPSDen > 0 {
		outcome.Metrics.Frames = int(math.Round(outcome.DurationSec * float64(outcome.FPSNum) / float64(outcome.FPSDen)))
	}

	// ── Canonical projection of the renderer-owned phase timings ────────
	// Chronon measured every render phase in
	// the V2 report; project them onto the Run as owner-measured operations
	// (typed projection, never a second timer) so the benchmark can answer
	// "where did the render seconds go" from the canonical run — the same
	// single source the report already owns. Phases that were not measured
	// stay absent: no fake zeros. This is the projection half of the
	// one-boundary-one-timer rule: the rust.render_clip operation above is
	// the render WALL (worker-owned), these operations are the render WORK
	// (engine-owned), and neither re-times the other's boundary.
	projectRendererPhases(ctx, outcome.Backend, outcome.Metrics)

	// ── Post-render byte certification (exact contract) ──────────────────
	if w.outputProber != nil {
		probeStart := time.Now()
		probe, err := w.outputProber.ProbeOutput(ctx, outcome.OutputPath)
		probeEnd := time.Now()
		kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipProbe}, probeStart, probeEnd, err)
		kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseHashProbe, probeStart, probeEnd, kernobs.StageStatusCompleted, err)
		if err != nil {
			return nil, fmt.Errorf("clip.render: probe rendered output: %w", err)
		}
		if err := ValidateContract(prepared.Contract, probe); err != nil {
			return nil, fmt.Errorf("clip.render: rendered output violates contract: %w", err)
		}
		emit("clip.render.probe.certified", "rendered bytes certified exact", map[string]any{
			"output_path": outcome.OutputPath,
			"fps_num":     probe.FPSNum,
			"fps_den":     probe.FPSDen,
			"width":       probe.Width,
			"height":      probe.Height,
		})
	}

	// The rendered artifact IS the published artifact: the overlay was
	// composited inside the Chronon pass, so there is no second encode and no
	// intermediate file to certify separately.
	publishPath := outcome.OutputPath

	if w.publisher == nil {
		// Rendering and publication are separate boundaries. A local render
		// executor may be used by benchmarks and preparation tests without a
		// publication port; return the canonical render facts and leave the
		// publication projection absent.
		emit("clip.render.completed", "Chronon render completed without publication", map[string]any{
			"output_path": outcome.OutputPath, "size_bytes": outcome.SizeBytes,
			"duration_sec": outcome.DurationSec, "ffmpeg_ms": outcome.FFmpegMS,
			"backend": outcome.Backend,
		})
		progress(100, "clip.render completed")
		finalizeMetrics(outcome.Metrics, time.Since(jobStart).Milliseconds(), outcome.DurationSec)
		return renderedResult(j, &req, prepared, plan, subtitleArtifact, outcome, nil), nil
	}
	// The publish stage is the true publisher boundary (Drive upload + asset
	// commit), distinct from the render-side probe/overlay stages — so the
	// RunReport critical path separates the clip.render "drive" phase from
	// the render chain. Publication metrics come exclusively from the
	// publisher-owned report; no worker chronometer is copied into a V2 field.
	// upload_slot is the real publication-slot occupancy: the wall the worker
	// spends inside the publisher (hash-free certified commit + Drive hand-off
	// or upload). Recorded after the publish call with measured anchors — the
	// previous start==end marker reported a fake 0 ms.
	uploadSlotStart := time.Now()
	publishStart := uploadSlotStart
	// The certified digest published is the render boundary's own digest of the
	// EXACT bytes at publishPath (computed while the artifact was streamed to
	// disk and verified against the queue's expected digest). The overlay was
	// composited inside that same render, so no second digest exists.
	certifiedSHA, certifiedSize := outcome.SHA256, outcome.SizeBytes
	publication, err := w.publisher.Publish(ctx, RenderPublishInput{
		RunID:              plan.RunID,
		SourceAssetID:      req.SourceAssetID,
		SourceTitle:        prepared.Source.Title,
		OutputPath:         publishPath,
		Outcome:            outcome,
		Contract:           prepared.Contract,
		Transcript:         prepared.Transcript,
		Subtitles:          subtitleArtifact,
		DriveFolderID:      publishFolderID,
		CertifiedSHA256:    certifiedSHA,
		CertifiedSizeBytes: certifiedSize,
	})
	publishEnd := time.Now()
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseUploadSlot, uploadSlotStart, publishEnd, kernobs.StageStatusCompleted, err)
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageClipPublish}, publishStart, publishEnd, err)
	kernobs.RecordClipPhase(ctx, kernobs.ClipPhaseDrive, publishStart, publishEnd, kernobs.StageStatusCompleted, err)
	if err != nil {
		return nil, fmt.Errorf("clip.render: publish result: %w", err)
	}
	// Publication metrics have ONE chronometer owner: the publisher. When it
	// reports its measured walls they are projected into the canonical V2
	// report as-is — the worker never re-times publication with a second
	// chronometer:
	//   publication_total_ms = publisher total wall
	//   artifact_publish_ms  = hash + taxonomy + commit (local artifact work)
	//   drive_upload_ms      = max(video, sidecar upload) — the uploads run
	//                          concurrently, so the phase wall is the max,
	//                          never the sum.
	// The renderer finalize timing (Chronon publish_ms → renderer_finalize_ms)
	// was recorded by the Rust adapter and is never overwritten here. The
	// publisher owns publication_total_ms, artifact_publish_ms and
	// drive_upload_ms. If it does not provide a report, those fields remain
	// NOT_INSTRUMENTED rather than being populated with a second worker timer.
	var logPublishMS int64 = NotInstrumented
	if outcome.Metrics != nil {
		if pm := publication.Publish; pm != nil {
			outcome.Metrics.PublicationTotalMS = Metric(pm.TotalMS)
			outcome.Metrics.ArtifactPublishMS = Metric(pm.HashMS + pm.TaxonomyResolveMS + pm.AssetCommitMS)
			driveMS := pm.VideoUploadMS
			if pm.SidecarUploadMS > driveMS {
				driveMS = pm.SidecarUploadMS
			}
			outcome.Metrics.DriveUploadMS = Metric(driveMS)
			logPublishMS = pm.TotalMS
		}
	}
	if publication == nil || publication.AssetID == "" ||
		(!publication.DrivePending && publication.DriveFileID == "") {
		return nil, fmt.Errorf("clip.render: publisher returned an invalid publication")
	}
	emit("clip.render.completed", "Chronon render completed", map[string]any{
		"output_path":  outcome.OutputPath,
		"size_bytes":   outcome.SizeBytes,
		"duration_sec": outcome.DurationSec,
		"ffmpeg_ms":    outcome.FFmpegMS,
		"backend":      outcome.Backend,
	})
	totalMS := time.Since(jobStart).Milliseconds()
	finalizeMetrics(outcome.Metrics, totalMS, outcome.DurationSec)
	w.log.Info("clip.render.job.completed",
		zap.String("subsystem", "clip_render_worker"),
		zap.String("job_id", j.ID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.String("asset_id", publication.AssetID),
		zap.String("drive_file_id", publication.DriveFileID),
		zap.String("drive_link", publication.DriveLink),
		zap.Bool("drive_pending", publication.DrivePending),
		zap.String("backend", string(outcome.Backend)),
		zap.Int64("total_ms", totalMS),
		zap.Int64("render_ms", renderMS),
		// Publication wall: the publisher-owned total when reported, else the
		// worker boundary wall (publishers without a metrics report).
		zap.Int64("renderer_finalize_ms", logPublishMS),
		zap.Int64("ffmpeg_ms", outcome.FFmpegMS),
		zap.Int64("size_bytes", outcome.SizeBytes),
	)
	progress(100, "clip.render completed")

	return renderedResult(j, &req, prepared, plan, subtitleArtifact, outcome, publication), nil
}
