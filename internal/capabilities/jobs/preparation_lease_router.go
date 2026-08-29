package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrPreparationLeaseBusy is returned by the artifact-claimer adapter when the
// underlying artifact cache reports ErrLeaseBusy (an abandoned/in-flight
// builder still owns the BUILDING row past its bounded wait window). The
// router treats it as a benign skip: another worker is building the unit, so
// the speculative candidate should stop without burning resources or failing
// the whole coordinator cycle.
var ErrPreparationLeaseBusy = errors.New("preparation: artifact lease busy (another worker owns the unit)")

// ArtifactClaimRequest is the canonical identity + lease parameters for an
// artifact-producing unit. It mirrors artifactcache.Key's identifying fields +
// the ClaimStore.Claim lease arguments so the capabilities layer need not
// import the downstream artifactcache package (avoids a dependency cycle); the
// wiring adapter converts it to artifactcache.Key before calling Claim.
type ArtifactClaimRequest struct {
	SourceSHA256     string
	Operation        string
	ParametersJSON   string
	ProcessorVersion string
	Lease            time.Duration
	ExpectedWorkMS   int64
}

// ArtifactClaimResult is the outcome of the artifact-cache single-builder
// lease. Acquired=true means this worker now owns the BUILDING row (it should
// run the artifact). Acquired=false with a non-nil identity means the artifact
// was already READY (no speculative work needed) or another worker won the
// insert/update race.
type ArtifactClaimResult struct {
	Acquired bool
	LeaseID  string
}

// ArtifactClaimer is the narrow port wrapping the artifact cache's real
// durable singleflight (artifact_cache_entries BUILDING rows). Concrete
// caches returning ErrLeaseBusy must map it to ErrPreparationLeaseBusy.
type ArtifactClaimer interface {
	Claim(ctx context.Context, req ArtifactClaimRequest) (ArtifactClaimResult, error)
}

// PreparationUnitLeaser leases a non-artifact unit on preparation_units via its
// scheduler_owner/lease_until fields (the store's AcquirePreparationUnit, which
// is the existing cross-job singleflight there). No new singleflight.
type PreparationUnitLeaser interface {
	AcquirePreparationUnit(context.Context, job.PreparationUnitClaim) (*job.PreparedUnit, bool, error)
}

const preparationCoordinatorLeaseFallback = 5 * time.Minute

// artifactProducingKinds are the unit kinds that materialize derived artifact
// bytes. These route through the artifact cache's durable Claim() singleflight
// (one builder per deterministic key); everything else routes through the
// preparation_units scheduler_owner/lease_until singleflight.
var artifactProducingKinds = map[string]bool{
	// TTS audio synthesis
	"TTS": true, "scene.tts": true, "tts.synthesize": true, "tts": true,
	// VidRush clip search/selection
	"VIDRUSH": true, "scene.vidrush": true,
	// Overlay / Chronon composition render
	"OVERLAY": true, "scene.overlay": true,
	// Audio compile
	"AUDIO": true, "audio.prepare": true,
	// Clip / render processors
	"clip.process": true, "chronon.render": true, "render": true,
}

func isArtifactUnit(u PreparationUnit) bool {
	return artifactProducingKinds[u.Kind]
}

// PreparationLeaseRouter routes a planning-unit lease through the correct
// existing singleflight mechanism — it does NOT introduce a fourth lease
// implementation:
//
//   - artifact-producing units go through the artifact cache's Claim()
//     (durable single-builder BUILDING-lease singleflight);
//   - everything else goes through preparation_units.scheduler_owner / lease_until
//     via AcquirePreparationUnit (the existing preparation singleflight).
//
// The goal is not to execute work (that remains owned by domain adapters) but
// to claim the right singleflight slot so a speculative candidate either wins
// the lease (and should run) or learns another worker already owns it (and
// should stop). Losing the lease or finding a READY hit is always a benign
// stop, never an error, so the coordinator never tears down over contention.
type PreparationLeaseRouter struct {
	leaser  PreparationUnitLeaser
	claimer ArtifactClaimer
	worker  string
	lease   time.Duration
}

// NewPreparationLeaseRouter builds the router. leaser and claimer may be nil
// independently (fail-open wiring): a nil leaser skips non-artifact leasing and
// a nil claimer routes artifact units onto the preparation_units lease. worker
// becomes the scheduler_owner on non-artifact rows; lease is the duration on
// both paths. Zero lease uses the fallback TTL.
func NewPreparationLeaseRouter(leaser PreparationUnitLeaser, claimer ArtifactClaimer, worker string, lease time.Duration) *PreparationLeaseRouter {
	if worker == "" {
		worker = "preparation-coordinator"
	}
	if lease <= 0 {
		lease = preparationCoordinatorLeaseFallback
	}
	return &PreparationLeaseRouter{leaser: leaser, claimer: claimer, worker: worker, lease: lease}
}

// Acquire claims the singleflight slot for a candidate via the appropriate
// mechanism. Returning nil (with or without an acquired lease) means the
// candidate either owns the lease (callers run the unit) or should back off.
// Returned errors are real faults (store/transport), not lease losses.
func (r *PreparationLeaseRouter) Acquire(ctx context.Context, candidate SpeculationCandidate) error {
	if r == nil {
		return nil
	}
	if candidate.Job == nil {
		return fmt.Errorf("preparation lease: candidate job is nil")
	}
	unit := candidate.Unit
	if isArtifactUnit(unit) && r.claimer != nil {
		return r.acquireArtifact(ctx, unit, candidate.EstimatedTimeSavedMS)
	}
	return r.acquirePreparation(ctx, candidate)
}

// acquireArtifact routes through the artifact cache's durable Claim(). A
// READY hit or ErrPreparationLeaseBusy are benign stops (nothing to build).
// When the unit has no canonical source/processor identity, it falls back to
// the preparation_units lease so the unit is still singleflight-claimed.
func (r *PreparationLeaseRouter) acquireArtifact(ctx context.Context, unit PreparationUnit, expectedMS int64) error {
	if unit.SourceSHA256 == "" || unit.ProcessorVersion == "" {
		return r.acquirePreparation(ctx, SpeculationCandidate{Job: nil, Unit: unit, EstimatedTimeSavedMS: expectedMS})
	}
	params := unit.ParametersJSON
	if params == "" {
		b, err := json.Marshal(unit.Inputs)
		if err != nil {
			return fmt.Errorf("preparation lease: canonicalize artifact parameters for %q: %w", unit.ID, err)
		}
		params = string(b)
	}
	res, err := r.claimer.Claim(ctx, ArtifactClaimRequest{
		SourceSHA256:     unit.SourceSHA256,
		Operation:        unit.Kind,
		ParametersJSON:   params,
		ProcessorVersion: unit.ProcessorVersion,
		Lease:            r.lease,
		ExpectedWorkMS:   expectedMS,
	})
	if err != nil {
		if errors.Is(err, ErrPreparationLeaseBusy) {
			// Another worker is building this artifact — back off, no fault.
			return nil
		}
		return err
	}
	_ = res // acquired (run) or READY hit (stop) — both handled by the caller
	return nil
}

// acquirePreparation routes through preparation_units.scheduler_owner /
// lease_until (AcquirePreparationUnit). When another worker already owns the
// running unit we lose the race and stop without error.
func (r *PreparationLeaseRouter) acquirePreparation(ctx context.Context, candidate SpeculationCandidate) error {
	if r.leaser == nil {
		return nil
	}
	unit := candidate.Unit
	jobType := ""
	if candidate.Job != nil {
		jobType = candidate.Job.Type
	}
	_, _, err := r.leaser.AcquirePreparationUnit(ctx, job.PreparationUnitClaim{
		Fingerprint:   unit.Fingerprint,
		UnitID:        unit.ID,
		UnitKind:      unit.Kind,
		JobType:       jobType,
		LeaseOwner:    r.worker,
		LeaseDuration: r.lease,
	})
	if err != nil {
		return err
	}
	return nil
}
