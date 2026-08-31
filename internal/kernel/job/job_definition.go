// Package job — job_definition.go (PR-KERNEL-JOB-POPULATE, commit 9, July 2026).
// Migration of canonical JobDefinition from internal/kernel/job/.
//
// JobDefinition is the canonical, complete description of a job type
// as it lives in the registry. It is the SSOT for runtime gating:
// queue, timeout, retry, codec, artifact policy, handler binding, and
// execution class are ALL declared on this struct.
//
// godlike/06 SSOT discipline (kernel-level canonical):
//   - Stdlib-only imports (no application/infrastructure/job imports).
//   - Every parameter type referenced is intra-package or stdlib
//     (Job, Event, Filter, Status live in this same kernel package).
//   - Cross-cutting contracts (CodecDescriptor, ArtifactPolicy,
//     ExecutionClass, Capability) are owned here so neither
//     application-layer nor infrastructure-layer package must declare
//     a competing surface.
//
// 10-field lock (C1 spec): Type, ExecutionClass, Queue, Timeout,
// RetryPolicyKey, ConcurrencyKey, RequiredCapabilities, PayloadCodec,
// ResultCodec, ArtifactPolicy. HandlerKey was removed in PR-AUDIT-7
// (binding is by def.Type directly in c3ValidateRuntimeGraph).
//
// MIGRATION FROM internal/kernel/job/job_definition.go:
//   - Pre-commit-9: JobDefinition was declared in internal/kernel/job/
//     as the canonical surface; the kernel owned mechanism types
//     only.
//   - Post-commit-9: JobDefinition is the kernel-level canonical.
//     internal/kernel/job/job_definition.go re-exports
//     `type JobDefinition = kerneljob.JobDefinition` and the
//     accompanying ExecutionClass / Capability / ArtifactPolicy
//     aliases for back-compat with the C1-C2 registry migration
//     wire-up in internal/capabilities/jobs/queue/registry*.
package job

import (
	"fmt"
	"strings"
	"time"
)

// ── ExecutionClass (canonical) ────────────────────────────────────────

// ExecutionClass gates where a job may execute. It is the second
// dimension — job.Type identity — for the two-runtime topology
// (Sender vs Creator / remote worker).
//
// Canonical values:
//
//   - ExecutionSenderOnly:     the job MUST run on the Sender
//     (central composition). A Creator
//     cannot claim it.
//   - ExecutionCreatorAllowed: the job may run on either the Sender
//     or the Creator (default; legacy
//     semantics). The Sender decides at
//     dispatch time based on capability
//     advertisement.
//   - ExecutionCreatorOnly:    the job MUST run on a Creator. The
//     Sender may advertise it but cannot
//     execute it. Examples: heavy media
//     pipelines with no Sender reach.
//
// Adding a fourth ExecutionClass is an ARCHITECTURE change; it
// requires an AGENTS.md / ARCHITECTURE.md update and an entry in
// architecture/current.yaml (per godlike/04 §"Registries and
// SSOT"). IsValid() rejects the new value in this binary.
type ExecutionClass string

const (
	// ExecutionSenderOnly marks jobs that the Sender owns exclusively.
	ExecutionSenderOnly ExecutionClass = "sender_only"

	// ExecutionCreatorAllowed marks jobs that may run on either runtime.
	ExecutionCreatorAllowed ExecutionClass = "creator_allowed"

	// ExecutionCreatorOnly marks jobs that a Creator MUST run.
	ExecutionCreatorOnly ExecutionClass = "creator_only"
)

// IsValid reports whether the ExecutionClass is one of the canonical
// three values.
func (e ExecutionClass) IsValid() bool {
	switch e {
	case ExecutionSenderOnly, ExecutionCreatorAllowed, ExecutionCreatorOnly:
		return true
	default:
		return false
	}
}

// String renders the canonical wire form. Implements fmt.Stringer.
func (e ExecutionClass) String() string { return string(e) }

// ── ArtifactPolicy (canonical) ────────────────────────────────────────

// ArtifactPolicy declares whether / how a job must produce artefacts.
// It is the producer-side contract between the Creator handler and
// the Sender's Complete handler.
//
// Three orthogonal dimensions:
//
//   - ProducesArtifacts:     whether the job is allowed to produce
//     files at all. False means the handler
//     MUST NOT return an ArtifactManifest;
//     the Sender refuses Complete if one is
//     returned. (e.g. assets.resolve returns
//     data only, no files.)
//   - RequireManifest:       whether the job MUST return a manifest
//     even if it produced zero artefacts.
//     Today the producer-side convention is
//     "every ProducesArtifacts=true job sets
//     RequireManifest=true so the Sender can
//     deterministically audit which artefacts
//     landed".
//   - MaxArtifacts / MaxTotalBytes: bounds enforced by the Creator
//     runtime BEFORE upload;
//     overruns are an immediate job failure,
//     not a partial success.
//
// The zero value (all fields zero / false) represents a pure-data
// sender-side job that returns no manifest — a safe default for
// classification jobs. Validate() below rejects the impossible
// combination (RequireManifest=true + ProducesArtifacts=false) and
// negative bounds.
type ArtifactPolicy struct {
	// ProducesArtifacts is true if the job may produce files.
	// Set false for jobs whose output is purely structured data.
	ProducesArtifacts bool

	// RequireManifest is true if Complete MUST carry an
	// ArtifactManifest (even when no files were produced).
	// Today, every ProducesArtifacts=true job sets this true.
	RequireManifest bool

	// MaxArtifacts caps the number of artefacts in the manifest.
	// Zero means "unbounded" (a sane upper limit is then enforced
	// by the canonical 1000-artefact runtime cap).
	MaxArtifacts int

	// MaxTotalBytes caps the cumulative size of all artefacts.
	// Zero means "unbounded" — the Sender still applies its own
	// per-job-type quota at upload time.
	MaxTotalBytes int64
}

// Validate returns nil if the policy is internally consistent, or
// a descriptive error.
//
// Invariants:
//
//   - RequireManifest=true implies ProducesArtifacts=true
//     (you can't require a manifest of zero artefacts).
//   - MaxArtifacts >= 0, MaxTotalBytes >= 0. Negative values
//     are configuration bugs, not runtime conditions.
func (p ArtifactPolicy) Validate() error {
	if p.RequireManifest && !p.ProducesArtifacts {
		return fmt.Errorf("ArtifactPolicy: RequireManifest=true but ProducesArtifacts=false (impossible)")
	}
	if p.MaxArtifacts < 0 {
		return fmt.Errorf("ArtifactPolicy: MaxArtifacts=%d must be >= 0", p.MaxArtifacts)
	}
	if p.MaxTotalBytes < 0 {
		return fmt.Errorf("ArtifactPolicy: MaxTotalBytes=%d must be >= 0", p.MaxTotalBytes)
	}
	return nil
}

// ── Capability (canonical) ────────────────────────────────────────────

// Capability is the canonical string label a worker advertises when
// it joins the cluster AND the tag a JobDefinition demands to be
// claimable. The intersection of (definition's RequiredCapabilities)
// AND (worker's advertised caps) gates claim acceptance.
//
// Today Capability is a typed-string alias because the surface is
// operationally a small set of well-known names (e.g.
// "media.image.generate", "image_gen_chrome"). Future capability
// taxonomies may introduce a typed substructure (Symbol, Version);
// until that lands, the string form is canonical.
type Capability string

// ── CodecDescriptor (canonical marker) ─────────────────────────────────

// CodecDescriptor is the canonical metadata-only marker interface
// (SchemaVersion + JobType). Body-bearing child interfaces
// (PayloadCodec + ResultCodec) are in codec.go and embed this marker.
//
// The marker contract surfaces two facts the registry / dispatch
// path need to identify a codec without invoking it (the body
// methods only run at EncodePayload / EncodeResult time):
//
//   - SchemaVersion: the wire-shape version (e.g. "v1"). A future
//     v2 codec is rejected by CompositionRoot codec-acceptance
//     gates (registry_compose_ssot_test.go).
//   - JobType: the canonical Type discriminator the codec is bound
//     to. Used by the JobTypeRegistry.ProducesArtifacts(jobType)
//     lookup that gates the legacy Complete-path vs the
//     CompleteWithArtifacts-path (godlike/07 ErrCompleteJobPathViolation).
type CodecDescriptor interface {
	// SchemaVersion is the wire-shape version of the codec
	// (e.g. "v1"). Future bump → v2; old codecs must reject
	// v2 wire-format rows.
	SchemaVersion() string

	// JobType is the canonical Type discriminator the codec is
	// bound to. The codec's Encode/Decode bodies assert the
	// request's JobType matches this value (runtime guard
	// against reusing a codec instance with the wrong
	// JobDefinition).
	JobType() string
}

// ── JobDefinition (canonical) ─────────────────────────────────────────

// JobDefinition is the canonical, complete description of a job type
// as it lives in the registry.
type JobDefinition struct {
	// Type identifies the job. Must be unique across the registry.
	Type string

	// Description is the human-readable, single-line summary of the
	// job's purpose (e.g. "script generation (clips -> voiceover/script
	// manifests)"). It is purely informational; Validate() does NOT
	// check it. The field was preserved from the pre-commit-9
	// internal/kernel/job/ canonical so legacy callers and per-owner
	// MustRegister() struct literals in internal/capabilities/**/job_types.go
	// continue to compile without churn (Card 9 baseline-repair,
	// July 2026).
	//
	// Format expectations (soft convention only, never enforced by
	// Validate()): descriptions SHOULD be a single line of <= 80 chars
	// in a lower-case noun-or-verb phrase, optionally extended with a
	// parenthetical data-flow summary in the form "input -> output".
	// No embedded newlines, no leading whitespace, no terminal
	// punctuation. The canonical MustRegister sites in
	// internal/capabilities/*/job_types.go follow this convention;
	// human / observability / CLI surfaces may rely on the
	// single-line shape.
	Description string

	// ExecutionClass gates who can claim this job.
	ExecutionClass ExecutionClass

	// Queue is the async queue name (e.g. "default", "heavy",
	// "ingest").
	Queue string

	// Timeout is the max wall-clock duration for a single attempt.
	// Zero = "use the canonical 10-minute default"; negative
	// rejected by Validate.
	Timeout time.Duration

	// RetryPolicyKey names the retry policy in the registry's
	// retry-policy table. Empty = "no retries — first failure
	// is terminal".
	RetryPolicyKey string

	// ConcurrencyKey names the per-key concurrency cap. Empty
	// defaults to "single_global" at compute time.
	ConcurrencyKey string

	// RequiredCapabilities is the set of capabilities an executor
	// MUST advertise to claim this job. Empty means "any worker
	// can claim".
	RequiredCapabilities []Capability

	// PayloadCodec encodes/decodes the job's INPUT payload.
	PayloadCodec PayloadCodec

	// ResultCodec encodes/decodes the job's OUTPUT result.
	ResultCodec ResultCodec

	// ArtifactPolicy declares whether this job produces files and
	// whether it MUST return an ArtifactManifest.
	ArtifactPolicy ArtifactPolicy
}

// Validate returns nil if the JobDefinition is internally consistent
// and ready for registry insertion, or a descriptive error covering
// the first violation.
//
// Invariants:
//
//   - Type non-empty after whitespace stripping.
//   - ExecutionClass.IsValid() (one of the three canonical values).
//   - Queue non-empty.
//   - Timeout >= 0.
//   - ArtifactPolicy.Validate() (delegated).
func (d JobDefinition) Validate() error {
	if strings.TrimSpace(d.Type) == "" {
		return fmt.Errorf("JobDefinition: Type is empty")
	}
	if !d.ExecutionClass.IsValid() {
		return fmt.Errorf("JobDefinition %q: ExecutionClass %q is not one of sender_only / creator_allowed / creator_only", d.Type, d.ExecutionClass)
	}
	if strings.TrimSpace(d.Queue) == "" {
		return fmt.Errorf("JobDefinition %q: Queue is empty (use the canonical DefaultQueue literal, not \"\")", d.Type)
	}
	if d.Timeout < 0 {
		return fmt.Errorf("JobDefinition %q: Timeout=%v is negative", d.Type, d.Timeout)
	}
	if err := d.ArtifactPolicy.Validate(); err != nil {
		return fmt.Errorf("JobDefinition %q: %w", d.Type, err)
	}
	return nil
}
