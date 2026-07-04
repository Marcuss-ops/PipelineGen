// FASE 2.2 P0-COMPL-6 — TTLConfig typed envelope + FromRetentionPolicy
// helper. godlike/06 SSOT: this is the SOLE typed surface for the
// workspace-retention TTL config the platform config + admin CLI + the
// diagnosis surface consume.
//
// Rationale: the pre-FASE-2.2 surface had no typed config shape; the
// worker-side retention sweeper read raw ints from the platform
// StorageConfig (`WorkspaceDir` string only — no TTL field). FASE 2.2
// introduces TTLConfig as the canonical typed envelope so the
// platform-side retention knobs (e.g. `velox.worker.retention.ttl`)
// parse into a typed shape and Validate() before reaching the
// composition root.
//
// godlike/07 minimum-blast-radius: TTLConfig mirrors RetentionPolicy
// (one-to-one field mapping). FromRetentionPolicy is the canonical
// converter; the platform-config layer (forward-pointer:
// `internal/app/workspace_retention_config.go`) implements the
// workspace_retention.ParseTTLConfig() that returns a TTLConfig.
package completion

import (
	"errors"
	"time"
)

// TTLConfig is the canonical typed envelope for the workspace-retention
// knobs exposed via the platform config + admin CLI + the
// diagnosis surface. Mirrors RetentionPolicy field-by-field (one source
// of truth — FromRetentionPolicy converts the operational policy into
// this wire-shape).
type TTLConfig struct {
	// DefaultTTLSeconds is the canonical wire-shape for
	// RetentionPolicy.DefaultTTL (seconds — matches the platform
	// env-var convention `seconds`). Must be > 0 and <= MaxTTLSeconds.
	DefaultTTLSeconds int64

	// MaxTTLSeconds is the canonical upper-bound for any
	// ttlHint at Prepare (capped at this value if exceeded). Must be
	// > 0 and >= DefaultTTLSeconds.
	MaxTTLSeconds int64

	// SweepIntervalSeconds is the canonical cadence for the sweeper
	// goroutine. Must be > 0 and <= 86400 (24h in seconds).
	SweepIntervalSeconds int64

	// EvictionMode is the canonical wire-shape for the eviction-mode
	// enum. Stored as the lowercase string (per EvictionMode.String)
	// so platform-side YAML / env-var config uses the same canonical
	// names as the operational policy.
	EvictionMode string
}

// Errors returned by TTLConfig.Validate (godlike/07 typed-error
// contract — errors.Is probes are the canonical test surface). Each
// is a per-field cause so the caller can identify WHICH knob failed.
var (
	ErrTTLConfigInvalidDefaultTTLSec   = errors.New("ttl_config: DefaultTTLSeconds must be > 0")
	ErrTTLConfigInvalidMaxTTLSec       = errors.New("ttl_config: MaxTTLSeconds must be > 0")
	ErrTTLConfigInvalidOrdering        = errors.New("ttl_config: DefaultTTLSeconds must be <= MaxTTLSeconds")
	ErrTTLConfigInvalidSweepInterval   = errors.New("ttl_config: SweepIntervalSeconds must be > 0")
	ErrTTLConfigInvalidSweepIntervalUp = errors.New("ttl_config: SweepIntervalSeconds must be <= 86400 (24h)")
	ErrTTLConfigInvalidEvictionMode    = errors.New("ttl_config: EvictionMode must be one of {deferred, inline}")
)

// Validate is the canonical fail-closed gate (godlike/07). Returns
// nil only if EVERY field passes per-field validation; otherwise
// returns the FIRST failing cause (errors.Is probe is canonical).
// godlike/07 minimum-ripple: callers compose via errors.Is on either
// the canonical ErrRetentionPolicyInvalid surfaced via
// FromRetentionPolicy OR the per-field cause surfaced via this
// Validate.
func (c TTLConfig) Validate() error {
	if c.DefaultTTLSeconds <= 0 {
		return ErrTTLConfigInvalidDefaultTTLSec
	}
	if c.MaxTTLSeconds <= 0 {
		return ErrTTLConfigInvalidMaxTTLSec
	}
	if c.DefaultTTLSeconds > c.MaxTTLSeconds {
		return ErrTTLConfigInvalidOrdering
	}
	if c.SweepIntervalSeconds <= 0 {
		return ErrTTLConfigInvalidSweepInterval
	}
	if c.SweepIntervalSeconds > 86400 {
		return ErrTTLConfigInvalidSweepIntervalUp
	}
	switch c.EvictionMode {
	case "deferred", "inline":
		// canonical names per EvictionMode.String
	default:
		return ErrTTLConfigInvalidEvictionMode
	}
	return nil
}

// FromRetentionPolicy is the canonical converter from the
// operational RetentionPolicy to the wire-shape TTLConfig. The
// platform-side config layer (forward-pointer) calls this to produce
// the typed envelope that the admin CLI + diagnosis surface consume.
// godlike/06 SSOT: this is the ONLY canonical conversion path; any
// call-site that needs both shapes composes via this helper
// (no per-call struct literal copy).
func FromRetentionPolicy(p RetentionPolicy) (TTLConfig, error) {
	if err := p.Validate(); err != nil {
		return TTLConfig{}, err
	}
	return TTLConfig{
		DefaultTTLSeconds:    int64(p.DefaultTTL / time.Second),
		MaxTTLSeconds:        int64(p.MaxTTL / time.Second),
		SweepIntervalSeconds: int64(p.SweepInterval / time.Second),
		EvictionMode:         p.Mode.String(),
	}, nil
}

// ToRetentionPolicy is the canonical inverse converter from the
// wire-shape TTLConfig to the operational RetentionPolicy. The
// platform-side admin CLI + diagnosis surface call this to apply a
// typed override to the canonical policy. Validate() is invoked
// once at the entry point (c.Validate()); the converted policy is
// returned without re-validation because TTLConfig.Validate
// already enforced the canonical invariants (godlike/07
// minimum-ripple: do not duplicate work that an earlier gate
// already did).
func (c TTLConfig) ToRetentionPolicy() (RetentionPolicy, error) {
	if err := c.Validate(); err != nil {
		return RetentionPolicy{}, err
	}
	mode := EvictionDeferred
	switch c.EvictionMode {
	case "deferred":
		mode = EvictionDeferred
	case "inline":
		mode = EvictionInline
	}
	return RetentionPolicy{
		DefaultTTL:    time.Duration(c.DefaultTTLSeconds) * time.Second,
		MaxTTL:        time.Duration(c.MaxTTLSeconds) * time.Second,
		SweepInterval: time.Duration(c.SweepIntervalSeconds) * time.Second,
		Mode:          mode,
	}, nil
}

// Compile-time assertion that TTLConfig implements a defaulted
// canonical shape. Per godlike/06 SSOT, the canonical TTL cable is
// 24h default / 7d max / 1h sweep / EvictionDeferred.
var _ TTLConfig = TTLConfig{
	DefaultTTLSeconds:    24 * 3600,
	MaxTTLSeconds:        7 * 24 * 3600,
	SweepIntervalSeconds: 3600,
	EvictionMode:         "deferred",
}
