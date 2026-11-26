package script

// remote_final_job_split.go owns the adapter half of the split handoff:
// an early base PREPARE that can run during the local overlay render, and the
// late FINALIZE that adds the certified overlay projection. The serial
// PREPARE→FINALIZE path in remote_final_job.go is untouched.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/knobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
)

// finalJobSplitPrepareEnabled is the deployment gate. The split is opt-in
// until a Master advertising FinalJobOverlayProjectionCapability is deployed:
// sending a base-only PREPARE to a serial-only Master would leave it waiting
// for overlay windows that can never arrive in FINALIZE.
func finalJobSplitPrepareEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(knobs.String("VELOX_FINAL_JOB_SPLIT_PREPARE"))) {
	case "enabled", "on", "true", "1":
		return true
	default:
		return false
	}
}

// FinalJobSplitEnabled reports whether this adapter can defer overlays. The
// runner type-asserts the interface before starting the early PREPARE.
func (a *RemoteFinalJobAdapter) FinalJobSplitEnabled() bool {
	return a != nil && a.client != nil && a.segments != nil && finalJobSplitPrepareEnabled()
}

// PrepareFinalJobSource builds the base plan and sends PREPARE. It deliberately
// never waits for the overlay render: the caller starts it while the render is
// in flight and joins it at the render boundary.
func (a *RemoteFinalJobAdapter) PrepareFinalJobSource(ctx context.Context, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || a.client == nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(fmt.Errorf("remote final-job adapter is not configured"))
	}
	if a.segments == nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(fmt.Errorf("remote final-job split requires the non-stock segment certifier"))
	}
	if !a.FinalJobSplitEnabled() {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(fmt.Errorf("remote final-job split is disabled"))
	}
	if err := a.client.CheckCapabilities(ctx, scriptgen.FinalJobOverlayProjectionCapability); err != nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(fmt.Errorf("remote final-job split requires capability %q: %w", scriptgen.FinalJobOverlayProjectionCapability, err))
	}
	started := time.Now()
	a.beginHandoffCache(runID)
	a.resolverNS.Store(0)
	tracer, _ := a.segments.(finalJobSegmentHandoffTracer)
	var scope uint64
	if tracer != nil {
		// The scope must exist before the builder asks the preparer for base
		// fragments, exactly like the serial StartFinalJob path.
		ctx, scope = tracer.BeginHandoff(ctx)
	}
	handoff, err := scriptgen.BuildFinalJobSourceHandoff(ctx, runID, req, result, a, a.segments)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(err)
	}
	plan, err := finalJobSplitPlan(handoff.Prepare)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(err)
	}
	if a.preflight != nil {
		if err := a.preflightFinalJob(ctx, plan); err != nil {
			return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(err)
		}
	}
	payloadBuiltAt := time.Now()
	workerID, err := finalJobWorkerPin()
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(err)
	}
	if workerID != "" {
		handoff.Prepare["placement_pin_worker_id"] = workerID
	}
	envelope, err := scriptgen.BuildFinalJobSplitEnvelopeV1(runID, handoff, handoff.Finalize)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(err)
	}
	pre, baseFinalize, err := envelope.MasterPayloads()
	if err != nil {
		return scriptgen.RemoteFinalJobResult{}, splitPrepareNotSent(fmt.Errorf("serialize split final-job envelope: %w", err))
	}
	keepFinalJobEnvelopes(runID, pre, baseFinalize)

	prepareStarted := time.Now()
	prepared, err := a.client.PrepareJob(ctx, pre)
	prepareMS := time.Since(prepareStarted).Milliseconds()
	timing := &scriptgen.RemoteFinalJobTiming{
		HandoffMS:        time.Since(started).Milliseconds(),
		HandoffResolveMS: a.resolverNS.Load() / int64(time.Millisecond),
		PayloadMS:        payloadBuiltAt.Sub(started).Milliseconds(),
		PrepareMS:        prepareMS,
		SplitPrepareMS:   time.Since(started).Milliseconds(),
	}
	if tracer != nil && scope != 0 {
		if prep := tracer.Timing(scope); prep.Segments > 0 || prep.Bytes > 0 {
			timing.SegmentPrep = &prep
		}
	}
	if err != nil {
		err = finalJobCapabilityError(err)
		receipt := scriptgen.RemoteFinalJobResult{JobID: prepared.JobID, ReportOwner: scriptgen.FinalJobReportOwner, Split: true, Timing: timing}
		if prepared.JobID != "" {
			receipt.Status = staleReceiptStatus(prepared.Status)
		}
		if errors.Is(err, remotejob.ErrRemoteStaleJob) {
			return receipt, fmt.Errorf("remote split preparation %s is stale and must be replaced: %w: %w", prepared.JobID, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return receipt, err
	}
	a.storeFinalJobSplitHandoff(prepared.JobID, handoff)
	status := "PREPARED"
	if remoteJobInFlight(prepared.Status) {
		// An idempotent replay of a job that a previous attempt already
		// finalized returns RUNNING: the next step is ATTACH, not FINALIZE.
		status = prepared.Status
	}
	attempt := 1
	if result != nil && result.RemoteFinalJob != nil && result.RemoteFinalJob.Attempt > 0 {
		attempt = result.RemoteFinalJob.Attempt
	}
	return scriptgen.RemoteFinalJobResult{
		JobID: prepared.JobID, Status: status, Attempt: attempt,
		ReportOwner: scriptgen.FinalJobReportOwner, Split: true, Timing: timing,
	}, nil
}

// FinalizeFinalJobSource attaches the certified overlay projection to the base
// job. It never PREPAREs: the job identity already exists and a second PREPARE
// would ask the Master for a second render.
func (a *RemoteFinalJobAdapter) FinalizeFinalJobSource(ctx context.Context, jobID, runID string, req scriptgen.GenerateRequest, result *scriptgen.GenerateResult) (scriptgen.RemoteFinalJobResult, error) {
	if a == nil || a.client == nil {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote final-job adapter is not configured")
	}
	id := strings.TrimSpace(jobID)
	if id == "" {
		return scriptgen.RemoteFinalJobResult{}, fmt.Errorf("remote split FINALIZE requires a job id")
	}
	started := time.Now()
	handoff, ok := a.finalJobSplitHandoff(id)
	if !ok {
		// The cache is process-local. A resumed process rebuilds the same
		// deterministic base handoff from the durable result; the base plan
		// digest is part of the FINALIZE identity, so it must match exactly.
		var err error
		handoff, err = scriptgen.BuildFinalJobSourceHandoff(ctx, runID, req, result, a, a.segments)
		if err != nil {
			return scriptgen.RemoteFinalJobResult{JobID: id, Status: "PREPARED", ReportOwner: scriptgen.FinalJobReportOwner, Split: true},
				fmt.Errorf("rebuild split final-job base handoff: %w", err)
		}
	}
	finalize, err := scriptgen.BuildFinalJobOverlayFinalize(ctx, runID, req, result, handoff)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{JobID: id, Status: "PREPARED", ReportOwner: scriptgen.FinalJobReportOwner, Split: true}, err
	}
	payloadBuiltAt := time.Now()
	envelope, err := scriptgen.BuildFinalJobSplitEnvelopeV1(runID, handoff, finalize)
	if err != nil {
		return scriptgen.RemoteFinalJobResult{JobID: id, Status: "PREPARED", ReportOwner: scriptgen.FinalJobReportOwner, Split: true}, err
	}
	_, finalizeMap, err := envelope.MasterPayloads()
	if err != nil {
		return scriptgen.RemoteFinalJobResult{JobID: id, Status: "PREPARED", ReportOwner: scriptgen.FinalJobReportOwner, Split: true}, fmt.Errorf("serialize split FINALIZE: %w", err)
	}
	keepFinalJobEnvelopes(runID, handoff.Prepare, finalizeMap)
	finalizeStarted := time.Now()
	err = a.client.Finalize(ctx, id, finalizeMap)
	timing := &scriptgen.RemoteFinalJobTiming{
		FinalizeMS:        time.Since(finalizeStarted).Milliseconds(),
		FinalizePayloadMS: payloadBuiltAt.Sub(started).Milliseconds(),
	}
	if err != nil {
		err = finalJobCapabilityError(err)
		receipt := scriptgen.RemoteFinalJobResult{JobID: id, Status: "PREPARED", ReportOwner: scriptgen.FinalJobReportOwner, Split: true, Timing: timing}
		if errors.Is(err, remotejob.ErrRemoteStaleJob) {
			return receipt, fmt.Errorf("remote split finalize of %s is stale and must be replaced: %w: %w", id, scriptgen.ErrFinalJobStaleReceipt, err)
		}
		return receipt, err
	}
	return scriptgen.RemoteFinalJobResult{JobID: id, Status: "RUNNING", ReportOwner: scriptgen.FinalJobReportOwner, Split: true, Timing: timing}, nil
}

// splitPrepareNotSent tags a failure that happened before PrepareJob could
// cross the transport boundary. Only these may discard the split intent; an
// untagged PrepareJob failure is ambiguous and must be replayed with the same
// base plan/key on the next attempt.
func splitPrepareNotSent(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", scriptgen.ErrFinalJobSplitPrepareNotSent, err)
}

func finalJobSplitPlan(prepare map[string]any) (scriptgen.FinalJobCompiledPlanV2, error) {
	raw, _ := prepare["compiled_render_plan_json"].(string)
	if strings.TrimSpace(raw) == "" {
		return scriptgen.FinalJobCompiledPlanV2{}, fmt.Errorf("split final-job PREPARE has no compiled plan")
	}
	var plan scriptgen.FinalJobCompiledPlanV2
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return scriptgen.FinalJobCompiledPlanV2{}, fmt.Errorf("decode split final-job compiled plan: %w", err)
	}
	return plan, nil
}

func (a *RemoteFinalJobAdapter) storeFinalJobSplitHandoff(jobID string, handoff scriptgen.FinalJobSourceHandoff) {
	id := strings.TrimSpace(jobID)
	if a == nil || id == "" {
		return
	}
	a.splitMu.Lock()
	if a.splitHandoffs == nil {
		a.splitHandoffs = make(map[string]finalJobSplitCache)
	}
	a.splitHandoffs[id] = finalJobSplitCache{handoff: handoff}
	a.splitMu.Unlock()
}

func (a *RemoteFinalJobAdapter) finalJobSplitHandoff(jobID string) (scriptgen.FinalJobSourceHandoff, bool) {
	if a == nil {
		return scriptgen.FinalJobSourceHandoff{}, false
	}
	a.splitMu.Lock()
	defer a.splitMu.Unlock()
	entry, ok := a.splitHandoffs[strings.TrimSpace(jobID)]
	return entry.handoff, ok
}
