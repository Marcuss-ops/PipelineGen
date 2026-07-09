// Package job — job_definition.go (P0 Commit 1, July 2026).
//
// JobDefinition is the canonical, complete description of a job type
// as it lives in the registry. It is the SSOT for runtime gating:
// queue, timeout, retry, codec, artifact policy, handler binding, and
// execution class are ALL declared on this struct.
//
// JobDefinition sits next to the pre-P0 records in the domain/job
// package (TypeScriptGenerate + 25 more Type* constants and the
// ArtifactManifest wire-format type). It is a NEW type: the legacy
// RegistryEntry / JobPolicy record in
// internal/application/jobs/registry.go continues to be the
// operational registry for now. Migration wire-up is in P0 Commits
// 2..3 (CodecDescriptor wiring + MutableJobRegistry /
// CompiledJobRegistry + Freeze + StartupValidator).
//
// ── LAYERING ────────────────────────────────────────────────────────
//
// This file defines ONLY types and programmatic validators. It does
// NOT import from internal/application/** or internal/infrastructure/**,
// per godlike/06 §"Database rules" (the canonical layering rule:
// domain has no application/infrastructure dependencies). Adapter
// methods that bridge JobPolicy ↔ JobDefinition live in
// internal/application/jobs/ once CompiledJobRegistry lands in
// Commit 3.
//
// ── P0 Commit 1 surface ──────────────────────────────────────────────
//
//   - ExecutionClass   (sender_only | creator_allowed | creator_only)
//     — the second dimension of P0's two-runtime
//     topology (Sender / Creator / both).
//   - ArtifactPolicy   (ProducesArtifacts, RequireManifest,
//     MaxArtifacts, MaxTotalBytes)
//     — the producer-side file contract.
//   - Capability       — the typed-string label for worker-advertised
//     / definition-required capabilities.
//   - CodecDescriptor — the metadata-only marker interface
//     (SchemaVersion + JobType). Kept from C1 as
//     the parent of PayloadCodec and ResultCodec.
//   - PayloadCodec   — canonical typed encode/decode for the
//     JobDefinition's INPUT payload (C2 elaboration
//     of CodecDescriptor with EncodePayload +
//     DecodePayload bodies). Defined in codec.go.
//   - ResultCodec    — canonical typed encode/decode for the
//     JobDefinition's OUTPUT result (C2 elaboration
//     of CodecDescriptor with EncodeResult +
//     DecodeResult bodies). Defined in codec.go.
//   - JobDefinition    — the SSOT struct, plus Validate.
//
// ── Field scope (locked to user C1 spec) ─────────────────────────────
//
// The JobDefinition struct has exactly the 10 fields listed in the
// user's C1 prompt: Type, ExecutionClass, Queue, Timeout,
// RetryPolicyKey, ConcurrencyKey, RequiredCapabilities, PayloadCodec,
// ResultCodec, ArtifactPolicy. (HandlerKey was removed in PR-AUDIT-7;
// handler binding is via c3ValidateRuntimeGraph by def.Type directly.)
// A "Description" field
// appears in the parallel pre-P0 JobPolicy record but is intentionally
// NOT mirrored here — the canonical registry entry's Description
// lives on JobPolicy until Commit 3's CompiledJobRegistry reconciles
// both records into a single canonical roster.
//
// ── Migration schedule for downstream commits ──────────────────────
//
//   - Commit 2:  PayloadCodec + ResultCodec interfaces (extending
//     CodecDescriptor marker) + TypedCodecAdapter[T,R]
//     decorator that adapts the existing Codec[T,R]
//     infrastructure to satisfy both domain interfaces.
//   - Commit 3:  MutableJobRegistry + CompiledJobRegistry + Freeze +
//     StartupValidator.ValidateRuntimeGraph.
//   - Commit 4:  Dispatcher.Enqueue through def.PayloadCodec.
//   - Commit 5..14: handler migration + CI gates.
//   - Commit 15: cutover — JobDefinition supersedes JobPolicy as the
//     registry SSOT; JobPolicy retained as a derived
//     projection.
package job

import (
	"fmt"
	"strings"
	"time"
)

// ── ExecutionClass (P0 §4.1) ─────────────────────────────────────────

// ExecutionClass gates where a job may execute. It is the second
// dimension — job.Type identity — for P0's two-runtime topology
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
// three values. StartupValidator (P0 Commit 3) uses this to reject
// registry entries whose ExecutionClass is unset or carries a
// future value not yet accepted by this binary.
func (e ExecutionClass) IsValid() bool {
	switch e {
	case ExecutionSenderOnly, ExecutionCreatorAllowed, ExecutionCreatorOnly:
		return true
	default:
		return false
	}
}

// String renders the canonical wire form. Implements fmt.Stringer
// for ergonomic logging.
func (e ExecutionClass) String() string { return string(e) }

// ── ArtifactPolicy (P0 §4.1) ─────────────────────────────────────────

// ArtifactPolicy declares whether / how a job must produce artefacts.
// It is the producer-side contract between the Creator handler and
// the Sender's Complete handler (P0 §6.6, Commit 7).
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
//     runtime (P0 Commit 6) BEFORE upload;
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
	// by the canonical 1000-artefact runtime cap in P0 Commit 6).
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

// ── Capability (P0 §4.3) ────────────────────────────────────────────

// Capability is the canonical string label a worker advertises when
// it joins the cluster AND the tag a JobDefinition demands to be
// claimable. The intersection of (definition's RequiredCapabilities)
// AND (worker's advertised caps) gates claim acceptance.
//
// Today Capability is a typed-string alias because the surface is
// operationally a small set of well-known names (e.g.
// "media.image.generate", "image_gen_chrome"). Future capability
// taxonomies may introduce a typed substructure (Symbol, Version);
// until that lands, the string form is canonical. The typed alias
// gives a single migration surface: a future rename of a
// capability lands in the alias, not in N literals across M files.
//
// Mirrors the existing
// internal/application/jobs/registry.go::RegistryEntry.RequiredCapabilities
// ([]string).
type Capability string

// ── Codec surfaces (P0 C2) ──────────────────────────────────────────

// CodecDescriptor (defs in codec.go, kept here as an alias note):
//
//   The metadata-only marker interface (SchemaVersion + JobType)
//   shipped in C1 is now the parent of two body-bearing interfaces
//   in codec.go:
//     PayloadCodec — embeds CodecDescriptor + Encode/Decode payloads
//     ResultCodec  — embeds CodecDescriptor + Encode/Decode results
//   All three interfaces live in this package (domain/job) but
//   PayloadCodec / ResultCodec's definitions are in codec.go for
//   cohesion with the application-layer adapter that satisfies them.

// ── JobDefinition (P0 §4.1) ─────────────────────────────────────────

// JobDefinition is the canonical, complete description of a job type
// as it lives in the registry. It is the runtime source of truth for
// queue, timeout, retry, codec, artifact policy, handler binding, and
// execution-class gating.
//
// The 10 fields below are locked to the user C1 spec
// (HandlerKey removed in PR-AUDIT-7, July 2026):
//
//  1. Type
//  2. ExecutionClass
//  3. Queue
//  4. Timeout
//  5. RetryPolicyKey
//  6. ConcurrencyKey
//  7. RequiredCapabilities
//  8. PayloadCodec
//  9. ResultCodec
//  10. ArtifactPolicy
//
// JobDefinition is constructed once at composition time (Commit 3:
// MutableJobRegistry.RegisterDefinition), validated at startup
// (Commit 3: StartupValidator.ValidateRuntimeGraph), and never
// mutated after Freeze (Commit 3: CompiledJobRegistry is the
// read-only view).
//
// MIGRATION: the pre-P0 JobPolicy (= RegistryEntry in
// internal/application/jobs/registry.go) is a parallel record. C1
// is a clean additive commit: JobPolicy continues to compile and
// the existing registry_compose_ssot_test + registry_completeness_test
// tests in internal/application/jobs/ stay untouched. Field-by-field
// migration happens in C2..C15.
type JobDefinition struct {
	// Type identifies the job. Must be unique across the registry.
	// String-compatible with the existing JobPolicy.Type field;
	// the canonical discriminator constants live at the top of
	// job.go (TypeScriptGenerate, TypeImagesGenerate, etc.).
	Type string

	// ExecutionClass gates who can claim this job. Empty value
	// is rejected by Validate(). The canonical three values are
	// documented on the ExecutionClass type.
	ExecutionClass ExecutionClass

	// Queue is the async queue name (e.g. "default", "heavy",
	// "ingest"). The broker uses this to bucket workers. Empty
	// value is rejected by Validate() — use the canonical
	// DefaultQueue literal, not the empty string.
	Queue string

	// Timeout is the max wall-clock duration for a single attempt.
	// Per the P0 plan §4.1 ("Zero means use the canonical
	// 10-minute default"), the legacy semantics carry over: zero
	// IS valid (= "use default"); only a negative duration is
	// rejected. Commit 3's StartupValidator does NOT need to
	// re-enforce this — Validate is sufficient.
	Timeout time.Duration

	// RetryPolicyKey names the retry policy in the registry's
	// retry-policy table. Empty means "no retries — first failure
	// is terminal".
	RetryPolicyKey string

	// ConcurrencyKey names the per-key concurrency cap (e.g.
	// "single_global" → 1, "global_cap_4" → 4). Empty defaults
	// to "single_global" at compute time.
	ConcurrencyKey string

	// RequiredCapabilities is the set of capabilities an executor
	// MUST advertise to claim this job. The intersection of
	// (definition.RequiredCapabilities) AND (worker.advertised)
	// is the claim-time gate. Empty means "any worker can claim".
	RequiredCapabilities []Capability

	// PayloadCodec encodes/decodes the job's INPUT payload.
	// Type is now the body-bearing PayloadCodec (C2 elaboration):
	// must implement CodecDescriptor (SchemaVersion + JobType) AND
	// the typed EncodePayload / DecodePayload bodies. May be nil
	// for jobs whose payload is empty; Commit 3 enforces
	// per-registry presence (a missing codec for an executable
	// job blocks startup).
	PayloadCodec PayloadCodec

	// ResultCodec encodes/decodes the job's OUTPUT result.
	// Type is the body-bearing ResultCodec (C2 elaboration):
	// must implement CodecDescriptor + the typed EncodeResult /
	// DecodeResult bodies. May be nil for sender-only
	// classification jobs that have no structured result.
	ResultCodec ResultCodec

	// ArtifactPolicy declares whether this job produces files and
	// whether it MUST return an ArtifactManifest. Sender-side
	// runtime enforces RequireManifest at Complete time.
	ArtifactPolicy ArtifactPolicy
}

// Validate returns nil if the JobDefinition is internally consistent
// and ready for registry insertion, or a descriptive error covering
// the first violation. Validate is a focused, programmatic check;
// StartupValidator (P0 Commit 3) layers cross-cutting invariants
// on top (no duplicates, codec presence per registry, no
// manifest-required without codec compatible, etc.).
//
// Invariants:
//
//   - Type non-empty after whitespace stripping.
//   - ExecutionClass.IsValid() (one of the three canonical values).
//   - Queue non-empty (applyDefaults normalises up-stream; this
//     check rejects empty in source).
//   - Timeout >= 0 (zero = "use the canonical 10-minute default"
//     per P0 plan §4.1; negative is a configuration bug).
//   - ArtifactPolicy.Validate() (delegated).
//
// Pass-through (NOT validated here; surface-level correctness is
// the caller's responsibility):
//
//   - RetryPolicyKey may be empty (means "no retries").
//   - ConcurrencyKey may be empty (means "single_global").
//   - PayloadCodec / ResultCodec may be nil (Commit 3 enforces
//     per-registry presence for executable jobs).
//   - RequiredCapabilities may be empty (means "any worker can
//     claim").

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
		return fmt.Errorf("JobDefinition %q: Timeout=%v is negative (use a positive duration or 0 = canonical default)", d.Type, d.Timeout)
	}
	if err := d.ArtifactPolicy.Validate(); err != nil {
		return fmt.Errorf("JobDefinition %q: %w", d.Type, err)
	}
	return nil
}
