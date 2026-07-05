// FASE 2.2 P0-COMPL-6 — typed-contract tests for the canonical
// RetentionPolicy + EvictionMode + TTLConfig. godlike/07 typed-error
// contract: errors.Is probes via package-level var sentinels (per
// pointer-identity match semantics) + Validate() fail-closed gating
// are the canonical test surface.
//
// No concrete sweeper-goroutine adapter is supplied in FASE 2.2 —
// the point is to pin the PACKAGE surface so a future sweeper
// adapter MUST satisfy the canonical invariants at composition
// time (fail-closed per godlike/07).
package completion

import (
	"errors"
	"testing"
	"time"
)

// TestNewDefaultRetentionPolicy_ValidatesClean pins the canonical
// production-default surface (24h default / 7d max / 1h sweep /
// EvictionDeferred). Any drift in the constant shape surfaces here
// per godlike/07 typed-error contract.
func TestNewDefaultRetentionPolicy_ValidatesClean(t *testing.T) {
	p, err := NewDefaultRetentionPolicy()
	if err != nil {
		t.Fatalf("NewDefaultRetentionPolicy: %v", err)
	}
	if got, want := p.DefaultTTL, 24*time.Hour; got != want {
		t.Fatalf("DefaultTTL = %v, want %v (canonical production-default)", got, want)
	}
	if got, want := p.MaxTTL, 7*24*time.Hour; got != want {
		t.Fatalf("MaxTTL = %v, want %v (canonical production-default)", got, want)
	}
	if got, want := p.SweepInterval, time.Hour; got != want {
		t.Fatalf("SweepInterval = %v, want %v (canonical production-default)", got, want)
	}
	if p.Mode != EvictionDeferred {
		t.Fatalf("Mode = %v, want EvictionDeferred (canonical production-default)", p.Mode)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("canonical production-default must validate: %v", err)
	}
}

// TestRetentionPolicy_ValidateFailClosed pins the canonical 6
// fail-closed gates. Each test crafts an invalid input and asserts
// that Validate returns a non-nil error containing BOTH the canonical
// ErrRetentionPolicyInvalid sentinel AND the per-field cause sentinel
// (godlike/07 typed-error contract — errors.Is pointer-identity match).
//
// IMPORTANT: per godlike/07 typed-error discipline, the test uses the
// SAME package-level var sentinels as retention.go Validate() to
// match by pointer identity (errors.Is compares plain errors.New
// returns by pointer — inline errors.New() returns DIFFERENT
// pointers per call, which would silently break the assertion).
func TestRetentionPolicy_ValidateFailClosed(t *testing.T) {
	base := RetentionPolicy{
		DefaultTTL:    24 * time.Hour,
		MaxTTL:        7 * 24 * time.Hour,
		SweepInterval: time.Hour,
		Mode:          EvictionDeferred,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("sanity: base policy must validate: %v", err)
	}
	tests := []struct {
		name      string
		mutate    func(p *RetentionPolicy)
		wantCause error
	}{
		{"DefaultTTL zero", func(p *RetentionPolicy) { p.DefaultTTL = 0 }, ErrRetentionDefaultTTLZero},
		{"MaxTTL zero", func(p *RetentionPolicy) { p.MaxTTL = 0 }, ErrRetentionMaxTTLZero},
		{"DefaultTTL > MaxTTL", func(p *RetentionPolicy) { p.DefaultTTL = p.MaxTTL + time.Hour }, ErrRetentionDefaultGTMax},
		{"SweepInterval zero", func(p *RetentionPolicy) { p.SweepInterval = 0 }, ErrRetentionSweepZero},
		{"SweepInterval > 24h", func(p *RetentionPolicy) { p.SweepInterval = 25 * time.Hour }, ErrRetentionSweepOver24h},
		{"Mode zero-value", func(p *RetentionPolicy) { p.Mode = 0 }, ErrRetentionModeInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			err := p.Validate()
			if err == nil {
				t.Fatalf("Validate must fail for invalid policy %s", tc.name)
			}
			if !errors.Is(err, ErrRetentionPolicyInvalid) {
				t.Fatalf("Validate error must chain ErrRetentionPolicyInvalid: got %v", err)
			}
			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("Validate error must chain the per-field sentinel %q: got %v", tc.wantCause.Error(), err)
			}
		})
	}
}

// TestRetentionPolicy_CapTTL pins the canonical ttlHint semantics for
// Prepare. The canonical contract:
//   - ttlHint <= 0  → DefaultTTL (silent fallback per idiomatic
//     Optional-or-cap)
//   - ttlHint > MaxTTL → MaxTTL (silent cap, NOT an error — the
//     typed-error ErrWorkspaceMarkTTLFailed ONLY applies at MarkTTL)
//   - 0 < ttlHint <= MaxTTL → ttlHint (verbatim round-trip)
func TestRetentionPolicy_CapTTL(t *testing.T) {
	p, err := NewDefaultRetentionPolicy()
	if err != nil {
		t.Fatalf("NewDefaultRetentionPolicy: %v", err)
	}
	tests := []struct {
		name string
		hint time.Duration
		want time.Duration
	}{
		{"zero returns DefaultTTL", 0, 24 * time.Hour},
		{"negative returns DefaultTTL", -1 * time.Hour, 24 * time.Hour},
		{"under MaxTTL returns hint", 12 * time.Hour, 12 * time.Hour},
		{"at MaxTTL returns hint", p.MaxTTL, p.MaxTTL},
		{"over MaxTTL caps to MaxTTL", p.MaxTTL + time.Hour, p.MaxTTL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.CapTTL(tc.hint); got != tc.want {
				t.Fatalf("CapTTL(%v) = %v, want %v", tc.hint, got, tc.want)
			}
		})
	}
}

// TestEvictionMode_String pins the canonical wire-names. The
// godlike/07 SSOT requires these be stable across the agent switch
// (no string-literal fallbacks on the caller side — every caller
// composes via EvictionMode.String()).
func TestEvictionMode_String(t *testing.T) {
	tests := []struct {
		mode EvictionMode
		want string
	}{
		{EvictionDeferred, "deferred"},
		{EvictionInline, "inline"},
		{EvictionMode(0), "eviction_mode_unset"},
		{EvictionMode(99), "eviction_mode_unset"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := tc.mode.String(); got != tc.want {
				t.Fatalf("EvictionMode(%d).String() = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

// TestEvictionMode_IsValid pins the canonical mode boundary. Zero-
// value is the canonical INVALID state per godlike/07 (the caller
// MUST initialise the policy via NewRetentionPolicyFromConfig before
// relying on the mode).
func TestEvictionMode_IsValid(t *testing.T) {
	tests := []struct {
		mode EvictionMode
		want bool
	}{
		{EvictionMode(0), false},
		{EvictionDeferred, true},
		{EvictionInline, true},
		{EvictionMode(99), false},
		{EvictionMode(-1), false},
	}
	for _, tc := range tests {
		t.Run(tc.mode.String(), func(t *testing.T) {
			if got := tc.mode.IsValid(); got != tc.want {
				t.Fatalf("EvictionMode(%d).IsValid() = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

// TestTTLConfig_ValidateFailClosed pins the canonical 6 fail-closed
// gates for the wire-shape TTLConfig. Each per-field error sentinel
// must be reachable via errors.Is from Validate() output.
func TestTTLConfig_ValidateFailClosed(t *testing.T) {
	base := TTLConfig{
		DefaultTTLSeconds:    24 * 3600,
		MaxTTLSeconds:        7 * 24 * 3600,
		SweepIntervalSeconds: 3600,
		EvictionMode:         "deferred",
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("sanity: base TTLConfig must validate: %v", err)
	}
	tests := []struct {
		name      string
		mutate    func(c *TTLConfig)
		wantCause error
	}{
		{"DefaultTTL zero", func(c *TTLConfig) { c.DefaultTTLSeconds = 0 }, ErrTTLConfigInvalidDefaultTTLSec},
		{"MaxTTL zero", func(c *TTLConfig) { c.MaxTTLSeconds = 0 }, ErrTTLConfigInvalidMaxTTLSec},
		{"DefaultTTL > MaxTTL", func(c *TTLConfig) { c.DefaultTTLSeconds = c.MaxTTLSeconds + 1 }, ErrTTLConfigInvalidOrdering},
		{"SweepInterval zero", func(c *TTLConfig) { c.SweepIntervalSeconds = 0 }, ErrTTLConfigInvalidSweepInterval},
		{"SweepInterval > 24h", func(c *TTLConfig) { c.SweepIntervalSeconds = 86401 }, ErrTTLConfigInvalidSweepIntervalUp},
		{"EvictionMode unsupported", func(c *TTLConfig) { c.EvictionMode = "sync-or-similar" }, ErrTTLConfigInvalidEvictionMode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate must fail for invalid TTLConfig %s", tc.name)
			}
			if !errors.Is(err, tc.wantCause) {
				t.Fatalf("Validate error must chain the per-field cause %q: got %v", tc.wantCause.Error(), err)
			}
		})
	}
}

// TestTTLConfig_ToFromRetentionPolicyRoundTrip pins the canonical
// round-trip between TTLConfig (wire-shape) and RetentionPolicy
// (operational). Any drift in FromRetentionPolicy vs
// TTLConfig.ToRetentionPolicy surfaces here per godlike/06 SSOT
// (one canonical conversion path both directions).
func TestTTLConfig_ToFromRetentionPolicyRoundTrip(t *testing.T) {
	p, err := NewDefaultRetentionPolicy()
	if err != nil {
		t.Fatalf("NewDefaultRetentionPolicy: %v", err)
	}
	cfg, err := FromRetentionPolicy(p)
	if err != nil {
		t.Fatalf("FromRetentionPolicy: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("TTLConfig.Validate: %v", err)
	}
	p2, err := cfg.ToRetentionPolicy()
	if err != nil {
		t.Fatalf("TTLConfig.ToRetentionPolicy: %v", err)
	}
	if p.DefaultTTL != p2.DefaultTTL {
		t.Fatalf("DefaultTTL round-trip: orig=%v, round-tripped=%v", p.DefaultTTL, p2.DefaultTTL)
	}
	if p.MaxTTL != p2.MaxTTL {
		t.Fatalf("MaxTTL round-trip: orig=%v, round-tripped=%v", p.MaxTTL, p2.MaxTTL)
	}
	if p.SweepInterval != p2.SweepInterval {
		t.Fatalf("SweepInterval round-trip: orig=%v, round-tripped=%v", p.SweepInterval, p2.SweepInterval)
	}
	if p.Mode != p2.Mode {
		t.Fatalf("Mode round-trip: orig=%v, round-tripped=%v", p.Mode, p2.Mode)
	}
}
