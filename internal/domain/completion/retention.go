// FASE 2.2 P0-COMPL-6 — canonical retention policy for workspace
// post-completion TTL semantics. godlike/06 one-canonical-owner-per-fact:
// this file is the SOLE canonical owner of the workspace-eviction
// cadence + eviction mode shape. The pre-FASE-2.2 surface scattered
// similar concerns across the worker-side retention sweeper
// (lifecycle.go::NewRetentionSweeper) and the document-side
// WorkspaceManager.Cleanup hook — both belong to the same canonical
// policy shape via this file.
//
// godlike/07 no-fake-availability: the eviction mode is the SECOND
// of the typed-mode enums (Deferred = sweeper-goroutine, batch,
// observable; Inline = per-job synchronous eviction). Deferred is
// the canonical default per the thinker verdict for FASE 2.2 Q2
// (inline eviction would degrade atomic-Complete TX latency; the
// sweeper pattern keeps the latency observable AND surfaces any
// eviction failure as the canonical `voiceover.cleanup.requested`
// outbox event).
//
// godlike/07 typed-error contract: 5 cause sentinels declared at
// PACKAGE LEVEL so callers compose via errors.Is (pointer-identity
// match) instead of brittle string-equal. errors.Join composes the
// cause + the canonical ErrRetentionPolicyInvalid sentinel so
// callers can probe either invariant ("did Validate fail?" via the
// canonical sentinel OR "which cause?" via the per-field sentinels).
package completion

import (
	"errors"
	"time"
)

// EvictionMode is the canonical typed enum for how the workspace
// sweeper goroutine evicts expired workspaces. godlike/07 SSOT: any
// non-typed string fallback (per the pre-FASE-2.2 voiceover-pattern
// outbox event-name string-concat) is a regression.
type EvictionMode int

const (
	// EvictionDeferred is the canonical sweeper-goroutine mode (FASE
	// 2.2 default). Eviction is asynchronous, batch, and observable
	// via the `voiceover.cleanup.requested` outbox event so any
	// eviction failure surfaces as a typed error (per godlike/07
	// no-fake-availability).
	EvictionDeferred EvictionMode = iota + 1

	// EvictionInline is reserved for the test-only test surface (the
	// canonical complete_job_service_test.go uses it to assert
	// synchronous eviction on test entry); production code MUST use
	// EvictionDeferred per the FASE 2.2 deadline-driven design.
	EvictionInline
)

// String returns the canonical wire-name of the eviction mode (used in
// log/metric emission and the outbox event metadata). godlike/07 SSOT:
// the canonical names are the ONLY stable surface; adding a new mode
// requires adding the string here AND the wire-name in the agent
// switch (no string-literal fallbacks on the caller side).
func (m EvictionMode) String() string {
	switch m {
	case EvictionDeferred:
		return "deferred"
	case EvictionInline:
		return "inline"
	default:
		return "eviction_mode_unset"
	}
}

// IsValid returns whether the mode is a canonical named variant. The
// zero-value (EvictionMode=0) is INVALID per godlike/07 — the caller
// MUST initialise the policy via NewRetentionPolicyFromConfig before
// relying on the mode.
func (m EvictionMode) IsValid() bool {
	switch m {
	case EvictionDeferred, EvictionInline:
		return true
	default:
		return false
	}
}

// RetentionPolicy is the canonical godlike/06 SSOT for the
// per-workspace TTL policy. All fields are required (Validate() is
// fail-closed for any zero / out-of-range value).
type RetentionPolicy struct {
	// DefaultTTL is the canonical TTL applied at Prepare time when
	// the caller does not pass an explicit ttlHint. Must be > 0 and
	// <= MaxTTL (fail-closed at Validate()).
	DefaultTTL time.Duration

	// MaxTTL is the canonical upper-bound enforced at MarkTTL. A
	// caller passing ttlHint > MaxTTL at Prepare is silently capped
	// (no error — the typed-shape is idiomatic for Optional-or-cap
	// semantics); a caller calling MarkTTL with ttl > MaxTTL returns
	// ErrWorkspaceMarkTTLFailed.
	MaxTTL time.Duration

	// SweepInterval is the canonical cadence for the sweeper
	// goroutine's evict loop. Must be >= DefaultTTL/4 to avoid
	// pathological sweeps; <= 24h to avoid unbounded accumulated
	// workspaces.
	SweepInterval time.Duration

	// Mode is the canonical eviction mode (EvictionDeferred
	// production-default; EvictionInline test-only).
	Mode EvictionMode
}

// godlike/07 typed-error contract: 6 var-level sentinels cover the
// canonical error surface. errors.Is probes are the canonical test
// surface — pointer-identity match so caller's tc.wantCause must be
// the SAME package-level sentinel (not an inline errors.New).
//
// Why package-level sentinels (godlike/07 typed-error discipline):
// `errors.Is` matches by POINTER IDENTITY for plain errors.New returns.
// An inline `errors.New("DefaultTTL must be > 0")` declared in
// retention.go Validate() returns a DIFFERENT pointer than an inline
// `errors.New("DefaultTTL must be > 0")` declared in the test, so
// errors.Is(err, tc.wantCause) returns false. The package-level vars
// below fix this exact failure mode.
var (
	// ErrRetentionPolicyInvalid is the canonical sentinel returned
	// by RetentionPolicy.Validate when any field is zero / out-of-
	// range. Production callers MUST NOT proceed without Validate()
	// returning nil per godlike/07 fail-closed at composition time.
	ErrRetentionPolicyInvalid = errors.New("completion: retention policy invalid (see wrapped cause)")

	// Per-field cause sentinels: each is wrapped into the canonical
	// ErrRetentionPolicyInvalid via errors.Join so callers can probe
	// either invariant via errors.Is.
	ErrRetentionDefaultTTLZero = errors.New("DefaultTTL must be > 0")
	ErrRetentionMaxTTLZero     = errors.New("MaxTTL must be > 0")
	ErrRetentionDefaultGTMax   = errors.New("DefaultTTL must be <= MaxTTL")
	ErrRetentionSweepZero      = errors.New("SweepInterval must be > 0")
	ErrRetentionSweepOver24h   = errors.New("SweepInterval must be <= 24h")
	ErrRetentionModeInvalid    = errors.New("Mode must be a canonical EvictionMode (zero-value is invalid)")
)

// Validate is the canonical fail-closed gate (godlike/07). Returns
// ErrRetentionPolicyInvalid (composed via errors.Join with the per-
// field cause) on any zero-value or out-of-range input. Callers
// compose via `errors.Is(err, ErrRetentionPolicyInvalid)` for the
// canonical gate OR `errors.Is(err, ErrRetentionDefaultTTLZero)` for
// the per-field root cause.
func (p RetentionPolicy) Validate() error {
	if p.DefaultTTL <= 0 {
		return errors.Join(ErrRetentionPolicyInvalid, ErrRetentionDefaultTTLZero)
	}
	if p.MaxTTL <= 0 {
		return errors.Join(ErrRetentionPolicyInvalid, ErrRetentionMaxTTLZero)
	}
	if p.DefaultTTL > p.MaxTTL {
		return errors.Join(ErrRetentionPolicyInvalid, ErrRetentionDefaultGTMax)
	}
	if p.SweepInterval <= 0 {
		return errors.Join(ErrRetentionPolicyInvalid, ErrRetentionSweepZero)
	}
	if p.SweepInterval > 24*time.Hour {
		return errors.Join(ErrRetentionPolicyInvalid, ErrRetentionSweepOver24h)
	}
	if !p.Mode.IsValid() {
		return errors.Join(ErrRetentionPolicyInvalid, ErrRetentionModeInvalid)
	}
	return nil
}

// CapTTL returns the canonical effective TTL for a Prepare-call's
// ttlHint. If ttlHint is 0, DefaultTTL is returned. If ttlHint
// exceeds MaxTTL, MaxTTL is returned (silent cap per the
// idiomatic-Optional-or-cap contract). Negative values are clamped
// to DefaultTTL (the typed-error ErrWorkspaceMarkTTLFailed ONLY
// applies at MarkTTL, NOT at Prepare per the canonical contract).
func (p RetentionPolicy) CapTTL(ttlHint time.Duration) time.Duration {
	if ttlHint <= 0 {
		return p.DefaultTTL
	}
	if ttlHint > p.MaxTTL {
		return p.MaxTTL
	}
	return ttlHint
}

// defaultsFromPlatformConfig returns the canonical TTL default values
// matching the pre-FASE-2.2 voiceover-pattern outbox TTL defaults.
// Forward-pointer: the platform-config integration is a separate
// FASE-2.2 follow-up PR (workspace_retention_config.go in internal/app)
// that funnels platform.Worker.Retention.TTL into this constructor.
//
// golike/06 SSOT: these defaults are the canonical SSOT; any
// platform-side override MUST be parsed via
// NewRetentionPolicyFromConfig so the Validate() gate runs.
const (
	defaultDefaultTTL    = 24 * time.Hour
	defaultMaxTTL        = 7 * 24 * time.Hour
	defaultSweepInterval = 1 * time.Hour
)

// NewDefaultRetentionPolicy returns the canonical production-default
// retention policy matching the pre-FASE-2.2 voiceover-pattern
// sweeper cadence (24h default / 7d max / 1h sweep interval /
// EvictionDeferred mode). Validates fail-closed: returns
// ErrRetentionPolicyInvalid if any field is out-of-range (impossible
// with these constant defaults, but the gate is invoked to surface
// any future drift in the constant shape).
func NewDefaultRetentionPolicy() (RetentionPolicy, error) {
	p := RetentionPolicy{
		DefaultTTL:    defaultDefaultTTL,
		MaxTTL:        defaultMaxTTL,
		SweepInterval: defaultSweepInterval,
		Mode:          EvictionDeferred,
	}
	if err := p.Validate(); err != nil {
		return RetentionPolicy{}, err
	}
	return p, nil
}
