package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// DefaultLeaseTTL is the canonical lease TTL used by the runner's
// renewing loop. Cadence (DefaultRenewInterval) is half of this so
// a single renewal failure remains non-fatal — the lease still has
// TTL/2 of slack before expiry.
//
// Tuning notes:
//   - 60s is conservative for the W1 spec; long-running handlers
//     (media.artlist / extract / batch) routinely run multi-minute.
//   - The HTTP broker round-trip is sub-second under steady state;
//     TTL/2 = 30s gives ample room for a transient retry even on
//     a degraded link.
//   - Smaller TTLs are possible but a flame-detection loop at 5s
//     cadence quickly drowns the broker in Renew traffic.
const DefaultLeaseTTL = 60 * time.Second

// DefaultRenewInterval is the cadence at which the runner ticks a
// Renew call inside runLease. Equal to DefaultLeaseTTL/2 (see
// rationale on DefaultLeaseTTL).
const DefaultRenewInterval = DefaultLeaseTTL / 2

// minRenewInterval bounds the lower edge of a configurable cadence
// to prevent a misconfigured test (or production override) from
// re-entering the renewal loop faster than the broker can answer.
const minRenewInterval = 50 * time.Millisecond

type Runner struct {
	broker        appjobs.Broker
	registry      *Registry
	workspace     *Workspace
	assetClient   AssetClient
	log           *zap.Logger
	workerID      string
	sessionID     string
	caps          []string
	renewInterval time.Duration // 0 → DefaultRenewInterval; clamped to >= minRenewInterval
}

// NewRunner constructs a Runner with the default renewal cadence
// (DefaultRenewInterval). Production callers should not need to
// override it; the W1 Phase 7 test suite injects a faster cadence
// to exercise the renewal protocol end-to-end without a 30s wait.
func NewRunner(broker appjobs.Broker, registry *Registry, workspace *Workspace, assetClient AssetClient, log *zap.Logger, workerID, sessionID string, caps []string) *Runner {
	return &Runner{
		broker:        broker,
		registry:      registry,
		workspace:     workspace,
		assetClient:   assetClient,
		log:           log,
		workerID:      workerID,
		sessionID:     sessionID,
		caps:          caps,
		renewInterval: DefaultRenewInterval,
	}
}

// SetRenewInterval overrides the renewal cadence. Returns the
// receiver for chaining. Zero / negative / sub-minRenewInterval
// values are clamped to DefaultRenewInterval or minRenewInterval
// respectively so a misconfigured test cannot re-enter the renewal
// loop faster than the broker can answer (which would surface as
// broker-side TCP pressure).
func (r *Runner) SetRenewInterval(d time.Duration) *Runner {
	switch {
	case d <= 0:
		r.renewInterval = DefaultRenewInterval
	case d < minRenewInterval:
		r.renewInterval = minRenewInterval
	default:
		r.renewInterval = d
	}
	return r
}

// effectiveRenewInterval returns the cadence actually used inside
// runLease. Falls back to DefaultRenewInterval when not configured.
func (r *Runner) effectiveRenewInterval() time.Duration {
	if r.renewInterval <= 0 {
		return DefaultRenewInterval
	}
	return r.renewInterval
}

func (r *Runner) Run(ctx context.Context) error {
	if r.registry == nil {
		r.registry = NewRegistry()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		lease, err := r.broker.Claim(ctx, job.ClaimCommand{
			WorkerID:        r.workerID,
			WorkerSessionID: r.sessionID,
			Capabilities:    r.caps,
			WaitSeconds:     20,
		})
		if err != nil {
			// W1 Phase 5: ErrNoWorkerCapabilities is a STARTUP misconfiguration,
			// not a transient broker failure. Retrying in a 2s loop would spam
			// logs forever; instead surface a single loud error and exit so the
			// process supervisor restarts with fresh registered caps.
			if errors.Is(err, appjobs.ErrNoWorkerCapabilities) {
				r.log.Error("worker has no advertised capabilities — refusing to retry",
					zap.String("reason", "registered types did not survive parse+dedup; check VELOX_WORKER_CAPABILITIES and cmd/worker startup"))
				return err
			}
			r.log.Warn("claim failed", zap.Error(err))
			time.Sleep(2 * time.Second)
			continue
		}
		if lease == nil || lease.Job == nil {
			continue
		}
		if err := r.runLease(ctx, lease); err != nil {
			r.log.Warn("job failed", zap.String("job_id", lease.Job.ID), zap.Error(err))
		}
	}
}

func (r *Runner) runLease(parent context.Context, lease *appjobs.Lease) error {
	job := lease.Job

	// Defensive: the claim filter should prevent this, but verify the
	// claimed job type is actually supported before doing any work.
	// Called BEFORE tools is constructed so we still use r.fail here
	// (which builds FailCommand from lease.Job.Revision — correct
	// because no renewal has happened yet at this point).
	if !r.registry.Has(job.Type) {
		r.log.Error("claimed unsupported job type — releasing",
			zap.String("job_type", job.Type),
			zap.String("job_id", job.ID),
		)
		return r.fail(parent, lease, fmt.Errorf("%w: %s", ErrHandlerNotRegistered, job.Type))
	}

	jobCtx, cancel := context.WithCancel(parent)
	defer cancel()

	jobDir, err := r.workspace.Prepare(lease.Job.ID)
	if err != nil {
		return r.fail(jobCtx, lease, err)
	}
	defer func() {
		_ = r.workspace.Cleanup(lease.Job.ID)
	}()

	// Tools is the canonical broker-facing facade for this job. After
	// construction, leaks the broker through Post-renewal methods
	// (Tools.Complete, Tools.Fail) so any subsequent broker call in
	// this runLease uses the post- Renew ExpectedRevision.
	tools := NewTools(r.broker, r.workerID, r.sessionID, lease.Job, jobDir, r.assetClient)

	// Start lease renewal loop (W1 Phase 7). The goroutine sends one
	// error (if any) on renewErrs before exiting; the runner's
	// checkRenew() helper drains it before each fail path so a
	// silent renewal-loop death never produces a double-Complete.
	renewCtx, renewCancel := context.WithCancel(jobCtx)
	defer renewCancel()
	renewErrs := make(chan error, 1)
	go r.renewLoop(renewCtx, tools, job.ID, renewErrs)

	// checkRenew non-blockingly reads any error that the renewal
	// goroutine has already emitted. Returns nil if the goroutine
	// is still happily ticking. The runner invokes this between
	// pipeline stages so a failed renewal (typically ErrLeaseLost
	// — another worker took the job) surfaces as broker.Fail BEFORE
	// any subsequent broker.Progress/Complete call that would
	// otherwise double-write the row.
	checkRenew := func() error {
		select {
		case err := <-renewErrs:
			if err != nil {
				r.log.Warn("lease renewal failed — failing the job",
					zap.String("job_id", job.ID),
					zap.Error(err))
			}
			return err
		default:
			return nil
		}
	}

	if err := checkRenew(); err != nil {
		return tools.Fail(jobCtx, err.Error())
	}

	if assets := ParseInputAssets(lease.Job.Payload); len(assets) > 0 {
		for i, assetID := range assets {
			if _, err := tools.DownloadAsset(jobCtx, assetID); err != nil {
				return tools.Fail(jobCtx, fmt.Errorf("download asset %d (%s): %w", i, assetID, err).Error())
			}
			_ = tools.Progress(jobCtx, 5+i, "staged input asset")
			if err := checkRenew(); err != nil {
				return tools.Fail(jobCtx, err.Error())
			}
		}
	}

	handlerResult, err := r.registry.Dispatch(jobCtx, lease.Job, tools)
	if err != nil {
		return tools.Fail(jobCtx, err.Error())
	}
	if err := checkRenew(); err != nil {
		return tools.Fail(jobCtx, err.Error())
	}

	resultJSON, err := json.Marshal(handlerResult)
	if err != nil {
		return tools.Fail(jobCtx, err.Error())
	}
	if err := r.uploadOutputs(jobCtx, lease.Job.ID, handlerResult); err != nil {
		return tools.Fail(jobCtx, err.Error())
	}
	if err := checkRenew(); err != nil {
		return tools.Fail(jobCtx, err.Error())
	}

	// Stop renewal BEFORE Complete so a final tick that lands while
	// Complete is mid-flight doesn't try to flip a job we just
	// terminal-reported.
	renewCancel()
	select {
	case <-renewErrs:
		// The loop already sent an error; we'll surface it as the
		// Complete return value instead of as a Fail command
		// (a Fail after Complete would be a no-op, but surfacing
		// the read order matters to the test assertions).
	case <-time.After(200 * time.Millisecond):
		// Loop is still alive; it will exit on its own when renewCtx
		// is observed on the next tick (or immediately, since cancel
		// closes the channel and we just observed-cancelled it via
		// renewCancel() above).
	}

	return tools.Complete(jobCtx, resultJSON)
}

// renewLoop ticks every r.effectiveRenewInterval() and calls
// tools.Renew with the canonical DefaultLeaseTTL. On any error
// (ErrLeaseLost in particular — the broker has reassigned the job
// to another worker — or a transient broker round-trip failure),
// the error is sent once on errs and the goroutine returns.
//
// Lifecycle: the goroutine exits when ANY of these happens first:
//   - renewCtx is cancelled (handler returned; parent ctx done)
//   - the ticker fires and tools.Renew returns an error
//
// Channel semantics: errs has capacity 1 so the goroutine never
// blocks on send. The runner's checkRenew helper drains it between
// phases. The cap of 1 is sufficient because the goroutine returns
// immediately after the first send.
func (r *Runner) renewLoop(ctx context.Context, tools *Tools, jobID string, errs chan<- error) {
	ticker := time.NewTicker(r.effectiveRenewInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := tools.Renew(ctx, DefaultLeaseTTL); err != nil {
				r.log.Warn("lease renew failed",
					zap.String("job_id", jobID),
					zap.Error(err))
				errs <- err
				return
			}
			r.log.Debug("lease renewed",
				zap.String("job_id", jobID),
				zap.Duration("ttl", DefaultLeaseTTL))
		}
	}
}

// fail is the pre-tools fail path used BEFORE tools is constructed.
// DEPRECATED since Phase 7 — only valid in the unsupported-job-type
// branch (runLease → !r.registry.Has check), which runs before
// `tools := NewTools(...)`. After Tools is constructed in runLease,
// all fail paths MUST use tools.Fail (which carries the
// post-renewal revision via the Tools atomic revision). Do NOT
// reach for r.fail from any other fail path; doing so silently
// regresses the revision-drift fix.
func (r *Runner) fail(ctx context.Context, lease *appjobs.Lease, err error) error {
	return r.broker.Fail(ctx, job.FailCommand{
		WorkerID:         r.workerID,
		WorkerSessionID:  r.sessionID,
		JobID:            lease.Job.ID,
		LeaseID:          lease.LeaseID,
		ExpectedRevision: lease.Job.Revision,
		Error:            err.Error(),
	})
}

func (r *Runner) uploadOutputs(ctx context.Context, jobID string, handlerResult map[string]any) error {
	if r.assetClient == nil || len(handlerResult) == 0 {
		return nil
	}
	type outputFile struct {
		assetID string
		path    string
	}
	var files []outputFile
	seen := make(map[string]struct{})
	add := func(assetID, path string) {
		assetID = strings.TrimSpace(assetID)
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if assetID == "" {
			assetID = jobID + ":" + filepath.Base(path)
		}
		key := assetID + "|" + path
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		files = append(files, outputFile{assetID: assetID, path: path})
	}

	for _, key := range []string{"output_path", "pdf_path", "markdown_path"} {
		if v, ok := handlerResult[key].(string); ok {
			add(jobID+":"+key, v)
		}
	}

	if raw, ok := handlerResult["output_files"]; ok {
		switch list := raw.(type) {
		case []string:
			for _, path := range list {
				add("", path)
			}
		case []any:
			for i, item := range list {
				switch v := item.(type) {
				case string:
					add("", v)
				case map[string]any:
					path, _ := v["path"].(string)
					assetID, _ := v["asset_id"].(string)
					if assetID == "" {
						assetID = fmt.Sprintf("%s:output_files:%d", jobID, i)
					}
					add(assetID, path)
				}
			}
		}
	}

	for _, file := range files {
		if _, err := os.Stat(file.path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := r.assetClient.UploadFile(ctx, file.assetID, file.path); err != nil {
			return fmt.Errorf("upload output %s: %w", file.path, err)
		}
	}
	return nil
}
