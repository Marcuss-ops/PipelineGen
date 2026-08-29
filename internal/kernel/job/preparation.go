package job

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// UnitKind identifies what a preparation unit computes (e.g. tts.synthesize,
// clip.process, script.generate). It participates in the canonical fingerprint.
type UnitKind string

// ResourceClass classifies what a unit consumes so the scheduler can decide
// what runs where and when (speculation vs active, singleflight arbitration).
type ResourceClass string

const (
	ResourceNetwork  ResourceClass = "NETWORK"
	ResourceDisk     ResourceClass = "DISK"
	ResourceCPULight ResourceClass = "CPU_LIGHT"
	ResourceCPUHeavy ResourceClass = "CPU_HEAVY"
	ResourceLLM      ResourceClass = "LLM"
	ResourceTTS      ResourceClass = "TTS"
	ResourceGPU      ResourceClass = "GPU"
	ResourceDrive    ResourceClass = "DRIVE"
)

// CostClass is a coarse cost band used to weigh speculation appetite against
// resource spend.
type CostClass string

const (
	CostCheap     CostClass = "CHEAP"
	CostMedium    CostClass = "MEDIUM"
	CostExpensive CostClass = "EXPENSIVE"
)

// ResultKind describes where a prepared unit's output lives. Almost every
// production result should be a reference, never duplicated bytes.
type ResultKind string

const (
	ResultNone          ResultKind = "NONE"
	ResultArtifactCache ResultKind = "ARTIFACT_CACHE" // artifact_cache_entries key
	ResultContentObject ResultKind = "CONTENT_OBJECT" // content_objects CAS hash
	ResultDomainCache   ResultKind = "DOMAIN_CACHE"   // owner/cache of the domain, e.g. research:<fp>
	ResultInlineJSON    ResultKind = "INLINE_JSON"    // small payload <= ~64KB carried inline
)

// SpeculationLevel is how aggressively a unit may be executed ahead of the
// critical path (0 = never speculate, 5 = eagerly ahead of demand).
type SpeculationLevel int

const (
	SpeculationOff SpeculationLevel = 0
)

// ResourceBudget is the per-resource-class admission budget the resource
// scheduler applies to SPECULATIVE work. Active execution is never throttled
// by it: active work holds absolute priority and only consumes the
// ActiveReserved share, while speculation may use at most SpeculativeMax
// concurrent units on the same resource class.
type ResourceBudget struct {
	// Capacity is the total concurrent units the resource class can carry
	// (active + speculative). Zero disables speculation on the class.
	Capacity int
	// ActiveReserved is the portion of Capacity reserved so active work is
	// never starved by speculation. Speculation may use up to
	// Capacity - ActiveReserved concurrent units.
	ActiveReserved int
	// SpeculativeMax caps speculative concurrency independently of Capacity
	// (belt-and-braces; normally Capacity - ActiveReserved).
	SpeculativeMax int
}

// ActiveWorkObserver is the narrow port the resource scheduler consults to
// learn whether active (claimed, running) job work exists. Active work ALWAYS
// preempts speculation: the scheduler refuses to admit or continue
// speculative units while ActiveWorkAvailable reports true.
type ActiveWorkObserver interface {
	ActiveWorkAvailable() bool
}

// InputManifest is the canonical set of inputs that determine a unit's
// result. It is persisted as JSON (input_manifest_json) differentiated from
// the fingerprint: the fingerprint is for the machine, the manifest is for
// humans to understand why two units are HIT or MISS. It must contain only
// canonical inputs, never whole videos, scripts, or transcripts.
type InputManifest map[string]any

// CanonicalManifestKind identifies the operator contract used to build a
// manifest. The fields are intentionally represented as JSON so operators can
// evolve without adding database columns.
type CanonicalManifestKind string

const (
	ManifestLLM         CanonicalManifestKind = "llm"
	ManifestResearch    CanonicalManifestKind = "research"
	ManifestTTS         CanonicalManifestKind = "tts"
	ManifestTranslation CanonicalManifestKind = "translation"
	ManifestClip        CanonicalManifestKind = "clip"
	ManifestVidRush     CanonicalManifestKind = "vidrush"
	ManifestOverlay     CanonicalManifestKind = "overlay"
	ManifestAudio       CanonicalManifestKind = "audio"
	ManifestRender      CanonicalManifestKind = "render"
)

// PreparationUnit is the canonical executable description of a unit of work.
// It is richer than the persisted row: fields marked [manifest] are carried
// in InputManifest and not stored as their own columns; the rest map to
// preparation_units columns.
type PreparationUnit struct {
	ID          string // logical unit id, e.g. scene_03:tts
	Kind        UnitKind
	Fingerprint string

	// JobType is the owning job type. It participates in the fingerprint at
	// build time (PreparationUnitFingerprint) and is carried here so the
	// singleflight executor can stamp the durable preparation_units row
	// (job_type column) without re-deriving it.
	JobType string

	FingerprintVersion string // version of the fingerprint algorithm
	ProcessorVersion   string // version of the processor producing the result

	Inputs InputManifest

	ResourceClass  ResourceClass
	ExpectedWorkMS int64

	CostClass CostClass

	Reusable    bool
	Preemptible bool

	SpeculationLevel SpeculationLevel

	// Expected resource footprints. Persisted in the manifest (input_manifest_json)
	// rather than as dedicated columns, per the Control-Plane plan.
	ExpectedMemoryBytes  int64
	ExpectedVRAMBytes    int64
	ExpectedNetworkBytes int64
}

// PreparedResult is the typed output of one executed preparation unit. It is
// the contract shared by every unit executor: the canonical fingerprint-addressed
// executor persists it into the durable singleflight store (MarkPreparationReady)
// and adoption reads it back from a READY row.
type PreparedResult struct {
	// Fingerprint is the unit's content address (must match the executed unit).
	Fingerprint string
	// Kind is the unit kind that produced the result.
	Kind UnitKind
	// ArtifactURI is the canonical artifact reference (cache key or content
	// address) of the produced bytes, when the result materializes a file.
	ArtifactURI string
	// ArtifactSHA256 is the hex SHA-256 of the produced artifact bytes, when
	// the result materializes a file.
	ArtifactSHA256 string
	// ResultJSON is the inline JSON payload of the result (transcripts,
	// probes, small objects). Artifact-producing executors may leave it empty.
	ResultJSON json.RawMessage
	// ProcessorVersion is the version of the processor that produced the
	// result. Adoption MUST verify it before reusing the artifact.
	ProcessorVersion string
}

// ArtifactCacheKey mirrors artifactcache.Key's identifying fields. artifactcache
// lives in capabilities (downstream of kernel), so kernel/job cannot import it;
// this shape lets a PreparationUnit and its artifact-cache entry be built from
// the SAME computation and share ONE deterministic identity.
type ArtifactCacheKey struct {
	SourceSHA256     string
	Operation        string
	ParametersJSON   string
	ProcessorVersion string
}

// ArtifactIdentityDigest computes the shared artifact-cache / preparation-unit
// fingerprint for this unit's artifact-producing computation, delegating to the
// single SSOT algorithm (digest.ArtifactKeyDigest). When a unit and an
// artifactcache.Key describe the same computation, this equals
// artifactcache.Key.Digest() — prefetch and artifact cache reuse one identity.
func (u PreparationUnit) ArtifactIdentityDigest(sourceSHA256 string) (string, error) {
	params, err := u.CanonicalParametersJSON()
	if err != nil {
		return "", err
	}
	return digest.ArtifactKeyDigest(sourceSHA256, string(u.Kind), params, u.ProcessorVersion)
}

// CanonicalParametersJSON returns the manifest as canonical JSON, suitable for
// artifactcache.Key.ParametersJSON. Empty/nil manifests become "{}".
func (u PreparationUnit) CanonicalParametersJSON() (string, error) {
	if len(u.Inputs) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(u.Inputs)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// PreparationClaimSnapshot is the durable "photograph" of how ready a job was
// at the INSTANT it was claimed (preparation_claim_snapshots, migration 248).
// Immediately after claim the units transition RUNNING → READY / MISS → READY
// and the pristine pre-claim state is otherwise unrecoverable. One row per
// (job, attempt); a re-claimed job inserts a fresh row for its new revision.
type PreparationClaimSnapshot struct {
	JobID       string
	AttemptID   string
	JobRevision int64
	ClaimedAt   time.Time

	TotalUnits    int
	RequiredUnits int
	ReadyUnits    int
	RunningUnits  int
	MissingUnits  int

	// PreparedAtClaimRatio is the core KPI: ReadyUnits / RequiredUnits at
	// claim time. It measures how much critical-path work was moved ahead of
	// the claim (vs a shallow cache-hit count).
	PreparedAtClaimRatio float64

	EstimatedSavedMS       int64 // sum of expected_work_ms over READY required units
	SpeculativeWorkMS      int64 // sum of expected_work_ms over RUNNING required units
	QueueWaitMS            int64
	QueuePositionAtPlan    int64
	Metadata               json.RawMessage
}

// PreparationClaimTargets encodes the plan's KPI target bands.
//
//	cold jobs      0–20%
//	normal queue  50–80%
//	N+1 speculative 80–95%
//	warm replay   95–100%
const (
	PreparationClaimBandCold     float64 = 0.20
	PreparationClaimBandNormal   float64 = 0.80
	PreparationClaimBandSpecNext float64 = 0.95
)

// PreparationClaimBandName classifies a prepared_at_claim_ratio into one of the
// planning target bands, for dashboards and alerting.
func PreparationClaimBandName(ratio float64) string {
	switch {
	case ratio < PreparationClaimBandCold:
		return "cold"
	case ratio < PreparationClaimBandNormal:
		return "normal"
	case ratio < PreparationClaimBandSpecNext:
		return "speculative"
	default:
		return "warm"
	}
}

// PreparationClaimInput is the caller-provided identity + queue context a
// claim snapshot carries alongside the readiness counts the store derives.
type PreparationClaimInput struct {
	JobID                string
	AttemptID            string
	JobRevision          int64
	ClaimedAt            time.Time
	QueueWaitMS          int64
	QueuePositionAtPlan  int64
	Metadata             json.RawMessage
}

// WorkloadDimension is the canonical scaling axis the work estimator learns on.
// Which dimension drives cost differs per unit kind: TTS is dominated by text
// length, renders by frame count, downloads by bytes, LLM calls by tokens.
// Unknown/none means the unit is small or not size-scaling (fall back to a
// per-kind average).
type WorkloadDimension string

const (
	WorkloadNone   WorkloadDimension = "none"
	WorkloadChars  WorkloadDimension = "chars"
	WorkloadFrames WorkloadDimension = "frames"
	WorkloadBytes  WorkloadDimension = "bytes"
	WorkloadTokens WorkloadDimension = "tokens"
)

// ManifestWorkloadKeys is the set of input_manifest keys the estimator probes
// (in order) when deriving a unit's workload amount for a dimension.
var manifestWorkloadKeys = map[WorkloadDimension][]string{
	WorkloadChars:  {"char_count", "text_length", "input_chars"},
	WorkloadFrames: {"frames", "frame_count"},
	WorkloadBytes:  {"bytes", "size_bytes", "expected_network_bytes"},
	WorkloadTokens: {"tokens", "token_count"},
}

// WorkloadDriver describes the scaling amount for one unit. Dimension is "none"
// when the unit is not size-scaling (Estimator falls back to a per-kind EMA).
type WorkloadDriver struct {
	Dimension WorkloadDimension
	Amount    float64
}

// Driver maps a unit to its size-scaling axis. The dimension is chosen by kind
// and the amount is extracted (best-effort, type-flexible) from the manifest.
// A zero/negative amount forces the dimension to none so the estimator falls
// back to the per-kind average instead of computing a bogus scaled estimate.
func (u PreparationUnit) Driver() WorkloadDriver {
	dim := WorkloadNone
	switch {
	case u.Kind == "tts.synthesize" || u.Kind == "tts" || u.Kind == "TTS":
		dim = WorkloadChars
	case u.Kind == "chronon.render" || u.Kind == "render" || u.Kind == "clip.process" || u.Kind == "RENDER":
		dim = WorkloadFrames
	case u.Kind == "asset.download" || u.Kind == "download" || u.Kind == "clip.download" || u.Kind == "NETWORK":
		dim = WorkloadBytes
	case u.Kind == "script.generate" || u.Kind == "research.llm" || u.Kind == "LLM":
		dim = WorkloadTokens
	}
	if dim == WorkloadNone {
		return WorkloadDriver{Dimension: WorkloadNone}
	}
	amount := firstNumberManifestValue(u.Inputs, manifestWorkloadKeys[dim])
	if amount <= 0 {
		return WorkloadDriver{Dimension: WorkloadNone}
	}
	return WorkloadDriver{Dimension: dim, Amount: amount}
}

func firstNumberManifestValue(m InputManifest, keys []string) float64 {
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case float64:
			if n > 0 {
				return n
			}
		case int:
			if n > 0 {
				return float64(n)
			}
		case int64:
			if n > 0 {
				return float64(n)
			}
		case json.Number:
			if f, err := n.Float64(); err == nil && f > 0 {
				return f
			}
		}
	}
	return 0
}

// WorkObservation is one measured execution the estimator learns from: a wall
// time plus (optionally) the workload amount that drove it.
type WorkObservation struct {
	Kind      UnitKind
	WallMS    int64
	Dimension WorkloadDimension
	Amount    float64
}

// WorkEstimate is the output of the estimator: an expected wall time for a
// kind (or a scaled estimate when the driver amount is known).
type WorkEstimate struct {
	Kind         UnitKind
	ExpectedWorkMS int64
	Source       WorkloadDimension // which axis produced the estimate
	Observations int
}

type PreparationState string

const (
	PreparationPlanned PreparationState = "PLANNED"
	PreparationRunning PreparationState = "RUNNING"
	PreparationReady   PreparationState = "READY"
	PreparationFailed  PreparationState = "FAILED"
	PreparationStale   PreparationState = "STALE"
)

type PreparedUnit struct {
	Fingerprint  string
	UnitID       string
	UnitKind     string
	JobType      string
	State        PreparationState
	LeaseOwner   string
	LeaseExpires *time.Time
	ArtifactID   string
	CacheKey     string
	Result       json.RawMessage
	Error        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    *time.Time

	// v2 Control-Plane metadata (migration 244). Populated at plan/acquire time
	// and read back by adopters.
	FingerprintVersion string
	ProcessorVersion   string
	ResourceClass      ResourceClass
	CostClass          CostClass
	SpeculationLevel   SpeculationLevel
	ExpectedWorkMS     int64
	// ResultKind / ResultRef are the canonical reference for the produced
	// result (ARTIFACT_CACHE / CONTENT_OBJECT / DOMAIN_CACHE / INLINE_JSON).
	ResultKind ResultKind
	ResultRef  string
}

type PreparationStore interface {
	GetPreparationUnit(context.Context, string) (*PreparedUnit, error)
	// PlanPreparationUnit seeds a durable PLANNED row for a fingerprint,
	// enabling the PLANNED → RUNNING → READY lifecycle. Idempotent: an
	// existing row (any state) is left untouched — singleflight wins.
	PlanPreparationUnit(context.Context, PreparationPlanInput) error
	AcquirePreparationUnit(context.Context, PreparationUnitClaim) (*PreparedUnit, bool, error)
	RenewPreparationUnitLease(context.Context, string, string, time.Duration) error
	MarkPreparationReady(context.Context, PreparationReadyUpdate) error
	MarkPreparationFailed(context.Context, string, string, string) error
	// ExpirePreparationUnits flips READY units whose expires_at is at or
	// before now to STALE and returns how many were expired.
	ExpirePreparationUnits(context.Context, time.Time) (int, error)
	// RegisterPreparationJobUnit records the job→unit dependency
	// idempotently. Registering twice for the same (job, unit) is a no-op.
	RegisterPreparationJobUnit(context.Context, RegisterPreparationJobUnitInput) error
	// ListPreparationJobUnits returns the units registered for a job,
	// ordered by queue rank (nulls last) then unit ID.
	ListPreparationJobUnits(context.Context, string) ([]PreparationJobUnit, error)
	// MarkPreparationJobUnitAdopted sets adopted=1 + adopted_at for the
	// job→unit mapping once the job reuses the prepared result.
	MarkPreparationJobUnitAdopted(context.Context, string, string) error
	// SnapshotPreparationClaim captures the claim-time readiness KPI for a job
	// (required/ready/running/missing units + prepared_at_claim_ratio + estimated
	// work saved) and persists it durably. Returns the computed snapshot.
	SnapshotPreparationClaim(context.Context, PreparationClaimInput) (*PreparationClaimSnapshot, error)
}

// PreparationPlanInput is the identity of a unit to seed as PLANNED. It carries
// the same v2 Control-Plane metadata as PreparationUnit so the durable row is
// populated at plan time (migration 244 columns) instead of being guessed later.
type PreparationPlanInput struct {
	Fingerprint string
	UnitID      string
	UnitKind    string
	JobType     string

	// FingerprintVersion versions the fingerprint algorithm; changing it
	// invalidates old prepared results instead of reusing incompatible ones.
	FingerprintVersion string
	// ProcessorVersion versions the processor that produces the result.
	ProcessorVersion string
	// Inputs is the canonical input manifest (persisted as input_manifest_json).
	Inputs InputManifest

	// ResourceClass / CostClass classify what the unit consumes so the
	// scheduler can arbitrate speculation and singleflight.
	ResourceClass  ResourceClass
	CostClass      CostClass
	SpeculationLevel SpeculationLevel
	// ExpectedWorkMS is the learned/static cost estimate used for claim-time
	// saved-work accounting. Zero leaves the column default.
	ExpectedWorkMS int64

	Reusable    bool
	Preemptible bool
}

// PreparationJobUnit is the durable per-job view of a prepared unit.
type PreparationJobUnit struct {
	JobID       string
	UnitID      string
	Fingerprint string
	Required    bool
	Adopted     bool
	QueueRank   *int
	PlannedAt   time.Time
	AdoptedAt   *time.Time
}

// RegisterPreparationJobUnitInput is the job→unit mapping to record.
type RegisterPreparationJobUnitInput struct {
	JobID       string
	UnitID      string
	Fingerprint string
	Required    bool
	QueueRank   *int
}

type PreparationUnitClaim struct {
	Fingerprint   string
	UnitID        string
	UnitKind      string
	JobType       string
	LeaseOwner    string
	LeaseDuration time.Duration
}

type PreparationReadyUpdate struct {
	Fingerprint string
	LeaseOwner  string
	ArtifactID  string
	CacheKey    string
	Result      json.RawMessage
	ExpiresAt   *time.Time

	// ActualWorkMS is the measured execution time of the unit, persisted so
	// the work estimator has real data and claim-time saved-work is accurate.
	ActualWorkMS int64
	// ResultKind / ResultRef identify where the produced result lives.
	ResultKind ResultKind
	ResultRef  string
}
