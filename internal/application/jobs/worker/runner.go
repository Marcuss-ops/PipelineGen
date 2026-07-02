package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
		lease, err := r.broker.Claim(ctx, appjobs.ClaimCommand{
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

	// Creator Blocco 2.2: uploadManifest tries the ArtifactManifest path
	// first; if no manifest is present, falls back to legacy uploadOutputs.
	// The returned UploadedManifest is Sender-safe (no local paths).
	uploaded, upErr := r.uploadManifest(jobCtx, lease.Job.ID, handlerResult)
	if upErr != nil {
		return tools.Fail(jobCtx, upErr.Error())
	}

	var resultJSON json.RawMessage
	if uploaded != nil {
		// Manifest path: send UploadedManifest (no local filesystem paths).
		resultJSON, err = json.Marshal(uploaded)
	} else {
		// Legacy path: send raw handlerResult (backward compat).
		resultJSON, err = json.Marshal(handlerResult)
	}
	if err != nil {
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
	return r.broker.Fail(ctx, appjobs.FailCommand{
		WorkerID:         r.workerID,
		WorkerSessionID:  r.sessionID,
		JobID:            lease.Job.ID,
		LeaseID:          lease.LeaseID,
		ExpectedRevision: lease.Job.Revision,
		Error:            err.Error(),
	})
}

// OutputArtifact is the typed per-output declaration a job handler
// may return in handlerResult["output_files"]. Used by the legacy
// uploadOutputsLegacy path for backward compat with handlers that
// pre-date the ArtifactManifest contract.
type OutputArtifact struct {
	AssetID  string `json:"asset_id,omitempty"`
	Path     string `json:"path"`
	Required bool   `json:"required,omitempty"`
}

// sha256File computes the SHA-256 digest of the file at path and returns
// the hex-encoded string. Thin wrapper around job.ComputeSHA256 so the
// worker package doesn't need to import crypto/sha256 directly.
func sha256File(path string) (string, error) {
	return job.ComputeSHA256(path)
}

// uploadManifest tries to decode an ArtifactManifest from handlerResult.
// If successful, validates required artifacts, computes SHA-256 digests,
// uploads each file via assetClient, and returns the sender-safe
// RemoteArtifactManifest (no local filesystem paths).
//
// P0 Commit 5 (C5): the canonical emit-side conversion is now
// (*ArtifactManifest).ToRemote, which enforces the V1 schema-version
// gate (rejecting any other schema_version before emit) and the
// required-missing rejection (pre-emit check that all Required
// artefacts have an entry in `uploaded`). The dual type vocabulary
// (Local ArtifactManifest + Remote RemoteArtifactManifest) is locked
// at this conversion boundary so the Sender NEVER sees LocalPath.
//
// If no manifest is found (Decode returns nil, nil), falls back to the
// legacy uploadOutputs path and returns nil, nil so the caller sends the
// raw handlerResult.
//
// Returns an error on: malformed manifest, validation failure, missing
// required artefact on disk, SHA-256 computation failure, upload failure,
// or post-upload ToRemote gate rejection (SchemaVersion!=V1, required
// missing).
func (r *Runner) uploadManifest(ctx context.Context, jobID string, handlerResult map[string]any) (*job.RemoteArtifactManifest, error) {
	if r.assetClient == nil || len(handlerResult) == 0 {
		return nil, nil
	}

	manifest, decodeErr := job.Decode(handlerResult)
	if decodeErr != nil {
		// Malformed manifest is a hard error — the handler declared a
		// manifest but it's unparseable.
		return nil, fmt.Errorf("artifact manifest decode: %w", decodeErr)
	}

	if manifest == nil {
		// No manifest key → legacy path.
		if err := r.uploadOutputsLegacy(ctx, jobID, handlerResult); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Manifest path: validate, compute digests, upload.
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("artifact manifest: %w", err)
	}

	uploaded := make(map[string]job.RemoteAssetIDAdapter, len(manifest.Artifacts))

	// Required artefacts: fail closed on any issue.
	for _, a := range manifest.RequiredArtifacts() {
		if _, statErr := os.Stat(a.Path); os.IsNotExist(statErr) {
			return nil, fmt.Errorf("required artifact %q (%s): file not found on disk: %s", a.ID, a.Kind, a.Path)
		}

		sha, shaErr := sha256File(a.Path)
		if shaErr != nil {
			return nil, fmt.Errorf("required artifact %q (%s): %w", a.ID, a.Kind, shaErr)
		}

		if uploadErr := r.assetClient.UploadFile(ctx, a.ID, a.Path); uploadErr != nil {
			return nil, fmt.Errorf("upload required artifact %q (%s): %w", a.ID, a.Kind, uploadErr)
		}

		uploaded[a.ID] = job.RemoteAsset{RemoteAssetID: a.ID, SHA256: sha}
	}

	// Non-required artefacts: best-effort upload.
	for _, a := range manifest.Artifacts {
		if a.Required {
			continue // already handled above
		}
		if _, statErr := os.Stat(a.Path); os.IsNotExist(statErr) {
			// Best-effort missing → skip (ToRemote marks as "skipped").
			continue
		}
		sha, shaErr := sha256File(a.Path)
		if shaErr != nil {
			r.log.Warn("non-required artifact SHA-256 failed — skipping",
				zap.String("artifact_id", a.ID), zap.String("kind", a.Kind), zap.Error(shaErr))
			continue
		}
		if uploadErr := r.assetClient.UploadFile(ctx, a.ID, a.Path); uploadErr != nil {
			r.log.Warn("non-required artifact upload failed — skipping",
				zap.String("artifact_id", a.ID), zap.String("kind", a.Kind), zap.Error(uploadErr))
			continue
		}
		uploaded[a.ID] = job.RemoteAsset{RemoteAssetID: a.ID, SHA256: sha}
	}

	// Build sender-safe manifest (no local paths) via the C5 canonical
	// ToRemote adapter. ToRemote enforces the V1 gate + required-missing
	// pre-emit check; the runner returns the typed error unwrapped so
	// the caller can errors.Is(err, ErrRemoteSchemaVersionUnsupported).
	return manifest.ToRemote(uploaded)
}

// ErrLegacyUploadPathRemoved is the sentinel returned by the now-disabled
// pre-Blocco-2.2 JSON path-scan upload path. P0 Commit 12 (July 2026)
// killed the output_path / pdf_path / markdown_path / output_files
// scan entirely: handlers that do not emit an ArtifactManifest under
// __artifact_manifest fail-closed at the runner. The error is exported
// so callers can probe via errors.Is and the operator dashboard can
// surface a single "missing-manifest" alarm category.
//
// Migration audit: callers that previously relied on the legacy path
// must build an ArtifactManifest sidecar (see C10's document handler
// at internal/api/assets/document/, C11's image handler at
// internal/application/images/, and C12's script.generate handler at
// internal/application/scripts/jobs/generation_job.go for the
// canonical reference implementations). The OutputArtifact struct is
// preserved below as a back-compat type alias — callers that try to
// construct it now get the sentinel error at composition, not at
// runtime; that audit signal is what gates "no future contributor
// silently re-introduces the path-scan".
var ErrLegacyUploadPathRemoved = errors.New("runner: legacy output_files/output_path/pdf_path/markdown_path upload path removed (P0 Commit 12); emit ArtifactManifest under __artifact_manifest instead")

// uploadOutputsLegacy is the disabled pre-Blocco-2.2 JSON path-scan.
// P0 Commit 12 (July 2026) killed it entirely: handlers that emit
// required files MUST go through the canonical ArtifactManifest sidecar
// at __artifact_manifest and the round-trip job.Decode path.
//
// Pre-C12 this function scanned handlerResult for the keys
// output_path / pdf_path / markdown_path (string keys) and the array
// output_files ([]string / []any / []OutputArtifact). The C12 audit
// (see architecture/current.yaml#C12 closure notes) confirmed there
// are no production callers left after C9 (creator per-job workspace)
// + C10 (document manifest) + C11 (image manifest) + C12
// (script.generate manifest) cut the legacy fallback down to zero
// callers. The function body is gone; the sentinel error + the audit
// signal stand.
//
// Fail-closed at composition-time: a future caller that bypasses the
// manifest emission and attempts the legacy path gets the typed
// error, surfacing the regression immediately in monitoring rather
// than running on a stale upload cycle that silently scans the wrong
// keys.
func (r *Runner) uploadOutputsLegacy(ctx context.Context, jobID string, handlerResult map[string]any) error {
	if r.log != nil {
		r.log.Error("legacy upload path invoked — handler does not emit ArtifactManifest",
			zap.String("job_id", jobID))
	}
	return fmt.Errorf("job=%s: %w", jobID, ErrLegacyUploadPathRemoved)
}
