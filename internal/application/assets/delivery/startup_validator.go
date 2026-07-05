// Package delivery — startup_validator.go (P1.3, July 2026).
//
// StartupDriveRootsValidator is the composition-time fail-closed check
// on DestinationRegistry.RootFolderID reachability. It iterates every
// registered DestinationKey, probes the configured rootFolderID on
// Drive via FolderManagerPort.ProbeFolderAccess, and reports a
// structured per-destination outcome.
//
// Why this exists: pre-P1.3, the composition root started with a
// DriveClient-typed-nil Publisher that surfaced its first failure at
// first Publish call site. Operators discovered "voiceover root is
// missing" the first time a voiceover job ran, not the first time the
// server booted. P1.3 promotes the reach-check to a composition-time
// barrier: the validator probes ALL registered destination roots
// sequentially at boot, returns the typed sentinel ErrDriveStartupValidationFailed
// if ANY reachable root fails, and the composition root gates boot on
// that sentinel in strict mode (default).
//
// Layering:
//   - The validator consumes delivery.DestinationRegistry + the
//     narrow FolderManagerPort. No I/O or wall-clock dependencies
//     beside the probe (retry uses pkg/retry.IsTransient).
//   - The concrete adapter (delivery.DriveRootsValidator) lives in
//     this file alongside the typed port, matching the
//     internal/domain/job/startup_validator.go P0 Commit 3 pattern.
//   - Capability-disable infrastructure is deferred to a follow-up
//     wave; in P1.3, the composition root read of the report either
//     halts the process (strict mode, default) or logs the failures
//     and continues (soft mode, opt-in via cfg.Drive.StrictStartupValidation).
//
// State machine:
//
//	Reachability per destination is binary (reachable | unreachable).
//	Empty RootFolderIDs are SKIPPED, not failed — operators may
//	intentionally leave sub-destinations unconfigured while the
//	corresponding capability remains off. The report carries a
//	Skipped list separately so the audit log distinguishes
//	"intentionally unset" from "configured but broken via probe failure".
//
// On error: returns the typed sentinel wrapped via fmt.Errorf (so
// errors.Is(err, ErrDriveStartupValidationFailed) detects the umbrella
// and the per-destination findings are joined via errors.Join for
// callers that want the burst view).
package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ErrDriveStartupValidationFailed is the typed umbrella sentinel
// returned by DriveRootsValidator.ValidateDriveRoots when ANY
// reachable RootFolderID fails the ProbeFolderAccess check. The
// per-destination findings are joined underneath via errors.Join.
// The composition root gates process boot on the typed-NIL-safe
// errors.Is(err, ErrDriveStartupValidationFailed) check.
var ErrDriveStartupValidationFailed = errors.New("delivery: StartupDriveRootsValidator detected unreachable root(s)")

// StartupValidationReport is the structured outcome of
// ValidateDriveRoots. Per-destination status preserved for both
// strict-fail and soft-disable modes — the composition root reads
// this regardless of the error path so it can log burst-mode failure
// detail and (in a follow-up wave) selectively disable capabilities.
type StartupValidationReport struct {
	// PerDestination is the per-key outcome (success OR error).
	// Empty RootFolderIDs are NOT included — see Skipped.
	PerDestination []DestinationValidationResult

	// Skipped lists DestinationKeys whose RootFolderID is empty.
	// Skipped destinations are NOT failures: operators may legitimately
	// leave sub-destinations unset while their capability stays off.
	// The validator logs a Warning per Skipped entry so the audit
	// trail can spot accidentally-missing config.
	Skipped []DestinationKey
}

// HasFailures reports whether the report carries at least one
// non-success PerDestination entry. Convenience for composition-root
// branching that needs the binary outcome without iterating the
// slice.
func (r *StartupValidationReport) HasFailures() bool {
	if r == nil {
		return false
	}
	for _, p := range r.PerDestination {
		if p.Err != nil {
			return true
		}
	}
	return false
}

// FailedDestinations returns the subset of PerDestination with a
// non-nil Err. Returned as []DestinationKey (not the full result
// struct) so callers can log burst-mode "X of Y failed" without
// re-walking the error chain.
func (r *StartupValidationReport) FailedDestinations() []DestinationKey {
	if r == nil {
		return nil
	}
	var failed []DestinationKey
	for _, p := range r.PerDestination {
		if p.Err != nil {
			failed = append(failed, p.Destination)
		}
	}
	return failed
}

// DestinationValidationResult is one row of the report — the
// per-destination probe outcome. Carries the error verbatim from
// ProbeFolderAccess so the composition root can log the typed
// sentinel chain (e.g. ErrAmbiguousDriveFile / ErrDriveServerError)
// without flattening.
type DestinationValidationResult struct {
	Destination  DestinationKey
	RootFolderID string
	Err          error // nil = reachable
	Elapsed      time.Duration
}

// ── Port (typed contract) ─────────────────────────────────────────────

// StartupDriveRootsValidator is the typed port the composition root
// invokes to enforce Drive root reachability at startup.
//
// The interface is intentionally single-method per AGENTS.md Pattern 0
// (port abstraction layer). Implementations MUST return nil on a
// clean graph and a non-nil error wrapping ErrDriveStartupValidationFailed
// when ANY reachable RootFolderID fails validation — even if other
// destinations passed. The partial-success case is preserved in the
// returned *StartupValidationReport so callers can log per-destination
// outcomes regardless of the binary outcome.
type StartupDriveRootsValidator interface {
	ValidateDriveRoots(ctx context.Context) (*StartupValidationReport, error)
}

// ── Constructor & default impl ───────────────────────────────────────

// ErrMissingDestinationRegistry is the fail-typed sentinel when the
// composition root hands a nil DestinationRegistry to
// NewDriveRootsValidator. Same composition-time fail-fast pattern as
// drive.ErrMissingDestinationRegistry; mirrored here so the
// validator constructor has its own typed-NIL surface to errors.Is.
//
// (Different package-level error name on purpose: it documents the
// constructor site, not the publisher seam.)
var ErrMissingStartupValidatorRegistry = errors.New("delivery: NewDriveRootsValidator: DestinationRegistry dependency is required (composition-time fail-fast)")

// StartupRootsProbe is the narrow port the validator consumes. The
// drive.DriveFolderManagerAdapter (and any adapter that exposes a
// side-effect-free reachability check) satisfies it structurally via
// Go interface compatibility — no explicit declaration required at
// the adapter site.
//
// Why a separate narrow port (vs consuming drive.FolderManagerPort):
//   - drive is an infrastructure package; delivery cannot import it
//     without a layering cycle (drive.publisher consumes delivery.Publisher
//     to surface its concrete impl).
//   - The validator's only need is the read-only probe — it does NOT
//     create folders, only verifies reachability. A wider port would
//     carry EnsureFolder as dead surface from the validator's POV.
//   - Future validators that need the wider surface (e.g. concurrent
//     probe + ensure-on-missing) should consume the wider
//     drive.FolderManagerPort via a composition-root adapter, not
//     expand this narrow port.
type StartupRootsProbe interface {
	ProbeFolderAccess(ctx context.Context, rootID string) error
}

// ErrMissingStartupValidatorFolders is the fail-typed sentinel for a
// nil StartupRootsProbe dependency. Same pattern — the validator
// needs the port to probe reachability, and a nil port would
// surface as a nil-deref panic at the first ValidateDriveRoots
// iteration step.
var ErrMissingStartupValidatorFolders = errors.New("delivery: NewDriveRootsValidator: StartupRootsProbe dependency is required (composition-time fail-fast)")

// DriveRootsValidator is the canonical impl of StartupDriveRootsValidator.
// All P1.3 wiring uses this implementation; future specialised
// variants (e.g. concurrent-probe if a registry grows past 50
// destinations) should embed this struct rather than replace it.
type DriveRootsValidator struct {
	registry *DestinationRegistry
	folders  StartupRootsProbe
	log      *zap.Logger
	// metrics is the observability handle for SRE surfaces
	// (counter per probe, latency histogram, run-summary gauges).
	// Nil-safe: composition roots can pre-empt metrics with nil
	// (typed-NIL-safe pattern after zap.Logger). Production
	// wiring injects delivery.NewDriveValidatorMetrics().
	metrics *DriveValidatorMetrics

	// Per probe-attempt: time budget before the retry loop gives up.
	ProbeTimeout time.Duration
	// ProbeAttempts is the retry-budget per root. 3 mirrors the
	// folderLookupMaxAttempts in infrastructure/drive/folder_manager.go.
	ProbeAttempts int
	// InitialBackoff is the first retry backoff. Backoff doubles per
	// attempt (factor 2.0); pkg/retry's MaxBackoff defaults apply.
	InitialBackoff time.Duration
}

// NewDriveRootsValidator constructs the validator. Fails-fast with
// typed sentinels on nil registry / nil folders (composition-time
// fail-closed pattern mirrored after drive.NewPublisher).
//
// log tolerance: nil log defaults to zap.NewNop() so composition
// roots that pre-wire the logger after NewComposition can still
// invoke the validator with a typed-NIL-safe log handle.
//
// metrics tolerance: nil metrics disables all SRE emission (the
// observe helpers short-circuit on a nil receiver). Composition
// roots that want a no-stats-run (e.g. dry-mode tooling) pass nil.
// Production wiring injects NewDriveValidatorMetrics() so probes /
// histograms / run-summary gauges populate prometheus.DefaultRegisterer.
func NewDriveRootsValidator(
	registry *DestinationRegistry,
	folders StartupRootsProbe,
	log *zap.Logger,
	metrics *DriveValidatorMetrics,
) (*DriveRootsValidator, error) {
	if registry == nil {
		return nil, ErrMissingStartupValidatorRegistry
	}
	if folders == nil {
		return nil, ErrMissingStartupValidatorFolders
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &DriveRootsValidator{
		registry:       registry,
		folders:        folders,
		log:            log,
		metrics:        metrics,
		ProbeTimeout:   30 * time.Second,
		ProbeAttempts:  3,
		InitialBackoff: 2 * time.Second,
	}, nil
}

// ValidateDriveRoots iterates every registered DestinationKey and
// probes its RootFolderID via FolderManagerPort.ProbeFolderAccess.
// Returns:
//   - (report, nil)         if every reachable root passed
//   - (report, ErrXxx)      if at least one reachable root failed;
//     the report still carries the full per-key
//     outcome (success + error) so the composition
//     root can log the burst detail.
//
// Sequential (not concurrent): probing 9 destinations concurrently at
// boot would hammer Drive's quota + need a bounded goroutine pool;
// sequential keeps the surface simple and the audit log line-by-line.
// The retry curve (2s → 4s, 3 attempts) means worst-case probe time
// is 9 × ~6s = ~54s of boot-time overhead on a fully-broken
// environment, which is acceptable for a fail-closed barrier.
//
// P1.4 (July 2026): per-probe observations are emitted to the
// injected *DriveValidatorMetrics (counter per outcome,
// histogram per elapsed), and a final observeRunEnd latches
// the run-summary gauges (last-run timestamp + success
// indicator). Nil-receiver safe — composition roots that
// pre-date the metrics surface pass nil without guard
// boilerplate.
func (v *DriveRootsValidator) ValidateDriveRoots(ctx context.Context) (*StartupValidationReport, error) {
	report := &StartupValidationReport{}

	for _, destKey := range v.registry.Keys() {
		policy, err := v.registry.Resolve(destKey)
		if err != nil {
			// Shouldn't happen (we just listed Keys()). Surface as a
			// failure on this destination so the report stays complete.
			report.PerDestination = append(report.PerDestination, DestinationValidationResult{
				Destination: destKey,
				Err:         err,
			})
			v.metrics.observeProbe(string(destKey), "failure", 0)
			continue
		}

		root := strings.TrimSpace(policy.RootFolderID)
		if root == "" {
			report.Skipped = append(report.Skipped, destKey)
			v.log.Warn("delivery: StartupDriveRootsValidator skipped — empty RootFolderID (capability may be intentionally disabled)",
				zap.String("destination", string(destKey)))
			// Skipped rows still surface on the SRE side: a skipped
			// probe is operator signal ("intentionally disabled
			// capability") distinct from a driven-but-failed probe.
			// Record with elapsed=0 because ProbeFolderAccess was
			// not invoked.
			v.metrics.observeProbe(string(destKey), "skipped", 0)
			continue
		}

		start := time.Now()
		probeErr := v.probeRoot(ctx, root)
		result := DestinationValidationResult{
			Destination:  destKey,
			RootFolderID: root,
			Err:          probeErr,
			Elapsed:      time.Since(start),
		}
		report.PerDestination = append(report.PerDestination, result)
		outcome := "success"
		if probeErr != nil {
			outcome = "failure"
		}
		v.metrics.observeProbe(string(destKey), outcome, result.Elapsed)

		if probeErr != nil {
			v.log.Error("delivery: StartupDriveRootsValidator probe failed",
				zap.String("destination", string(destKey)),
				zap.String("root_id", root),
				zap.Duration("elapsed", result.Elapsed),
				zap.Error(probeErr),
			)
		} else {
			v.log.Info("delivery: StartupDriveRootsValidator probe ok",
				zap.String("destination", string(destKey)),
				zap.String("root_id", root),
				zap.Duration("elapsed", result.Elapsed),
			)
		}
	}

	v.metrics.observeRunEnd(!report.HasFailures(), float64(time.Now().Unix()))

	if !report.HasFailures() {
		return report, nil
	}
	failed := report.FailedDestinations()
	wrapped := make([]error, 0, len(failed))
	for _, dest := range failed {
		// Pull the per-destination Err (NOT the iteration Err) so the
		// joined chain carries the typed ProbeFolderAccess surface
		// (e.g. ErrAmbiguousDriveFile) + the destination key.
		for _, r := range report.PerDestination {
			if r.Destination == dest && r.Err != nil {
				wrapped = append(wrapped,
					fmt.Errorf("destination %q (root=%q): %w", dest, r.RootFolderID, r.Err))
			}
		}
	}
	return report, fmt.Errorf("%w: %d root(s) failed — %w",
		ErrDriveStartupValidationFailed, len(failed), errors.Join(wrapped...))
}

// probeRoot probes Reachability for a single root via the narrow
// FolderManagerPort.ProbeFolderAccess method. Wraps the call in
// pkg/retry retry+timeout per the validator's configured budget.
//
// Per-attempt timeout: ProbeTimeout (default 30s). The per-attempt
// timeout is enforced inside retry.DoWithValue via context.WithTimeout
// so a single slow attempt does not consume the full retry budget on
// its own.
//
// Note: ProbeFolderAccess is read-only — no Drive files are created
// here even on transient retry failure. The retry is exclusively
// for transient GET errors (429, 503, timeout).
func (v *DriveRootsValidator) probeRoot(ctx context.Context, rootID string) error {
	_, err := retry.DoWithValue(ctx, func() (struct{}, error) {
		probeCtx, cancel := context.WithTimeout(ctx, v.ProbeTimeout)
		defer cancel()

		if perr := v.folders.ProbeFolderAccess(probeCtx, rootID); perr != nil {
			return struct{}{}, perr
		}
		return struct{}{}, nil
	}, retry.Options{
		MaxAttempts:    v.ProbeAttempts,
		InitialBackoff: v.InitialBackoff,
		BackoffFactor:  2.0,
		JitterFraction: 0.25, // ±25% — matches pkg/retry default
		IsRetryable:    retry.IsTransient,
		OnRetry: func(attempt int, err error) {
			v.log.Warn("delivery: StartupDriveRootsValidator transient probe error, retrying",
				zap.String("root_id", rootID),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
		},
	})
	return err
}

// Compile-time assertion pinning DriveRootsValidator to the
// StartupDriveRootsValidator interface contract. Future drift
// surfaces as a build failure here, not a nil-deref at boot.
var _ StartupDriveRootsValidator = (*DriveRootsValidator)(nil)
