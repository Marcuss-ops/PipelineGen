// Package app — WireAssets dep-getter type-classification (PR-WIRE-ASSETS-NIL-CLASSIFICATION, July 2026).
//
// Per godlike/06 SSOT (one canonical owner per fact + no-fake-availability),
// every WireAssets dep-getter must declare its classification explicitly so
// the failure mode is uniform across the composition root:
//
//   - DepRequired:        production fail-closed (returns ErrRequiredDepMissing wrapped).
//     A nil/empty dep is a composition bug; the server must not start.
//   - DepOptionalDisabled: capability degraded but server stays up (logs Warn, returns nil).
//     A nil/empty dep is an operator-facing policy decision (feature flag
//     off, capability pending Phase 2+).
//   - DepTestOnly:         only valid in test fixtures; production must fail-closed
//     (returns ErrTestOnlyDepMissing wrapped). A nil/empty dep in
//     production is a deployment error (test stub leaked).
//
// Per godlike/07 typed-error contract, each class has a dedicated typed sentinel
// so callers can pattern-match via `errors.Is(err, Err*)` for telemetry aggregation.
// The helper takes `isNil bool` (caller computes it) rather than the dep directly —
// this avoids the typed-nil trap with generics/reflection and keeps the canonical
// `, ok := iface.(*Concrete)` Go type-assertion idiom in the call site.
package wiring

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// DepClassification type-classifies a WireAssets dep-getter per godlike/06 SSOT.
type DepClassification int

const (
	// DepRequired: production fail-closed. A nil/empty dep is a composition bug.
	DepRequired DepClassification = iota

	// DepOptionalDisabled: capability degraded but server stays up.
	// Logs Warn, returns nil so the server lifecycle continues.
	DepOptionalDisabled

	// DepTestOnly: only valid in test fixtures; production must fail-closed.
	// A nil/empty dep in production is a deployment error.
	DepTestOnly
)

// String returns the canonical lowercase name for logging + error messages.
func (c DepClassification) String() string {
	switch c {
	case DepRequired:
		return "required"
	case DepOptionalDisabled:
		return "optional-disabled"
	case DepTestOnly:
		return "test-only"
	}
	return "unknown"
}

// Typed sentinels (godlike/07 typed-error contract).
// Each class has a dedicated sentinel so callers can pattern-match via
// `errors.Is(err, Err*)` for telemetry aggregation + per-class alerting.
//
// Note: DepOptionalDisabled does NOT have a sentinel because the helper
// logs Warn + returns nil for that class (capability degraded, server
// stays up). If a future caller needs to aggregate the disabled-set,
// surface the OptionalDisabled state via the log stream (zap fields
// `dep` + `class`) rather than a wrapped error — log scanning is the
// canonical aggregation surface per godlike/06 SSOT.
var (
	// ErrRequiredDepMissing: composition bug; the server cannot start.
	// Caller must fail-closed (return error from WireAssets).
	ErrRequiredDepMissing = errors.New("composition: required dep missing (production fail-closed)")

	// ErrTestOnlyDepMissing: test stub leaked into production.
	// Caller must fail-closed (return error from WireAssets).
	ErrTestOnlyDepMissing = errors.New("composition: test-only dep missing (production must fail-closed)")
)

// ClassifyDepGet is the canonical dep-getter classification helper.
//
// Returns nil if isNil is false (dep is present + non-nil).
// Otherwise returns a typed error per class:
//
//   - DepRequired         → fmt.Errorf("%s: %w", name, ErrRequiredDepMissing)
//   - DepOptionalDisabled → log.Warn + return nil (capability degraded)
//   - DepTestOnly         → fmt.Errorf("%s: %w", name, ErrTestOnlyDepMissing)
//
// godlike/07 minimum-blast-radius: the helper is ADDITIVE — existing inline
// `if !ok || X == nil { return error }` patterns continue to work, but new code
// SHOULD route through this helper for consistent typed errors + per-class
// telemetry.
//
// Parameters:
//   - name: canonical dep name (e.g. "WireAssets: searchFanOut") surfaced in the
//     error message + log fields for operator-side diagnostics.
//   - isNil: pre-computed nil check. The caller computes this from the
//     `, ok := iface.(*Concrete)` type-assertion (idiomatic Go) + the
//     `concrete == nil` pointer-nil check. The helper does NOT use reflection
//     (per godlike/06 typed-nil safety + per the thinker's design validation).
//   - class: the DepClassification for this dep.
//   - log: zap logger (used for DepOptionalDisabled Warn); may be nil for
//     test fixtures (no-op in that case).
func ClassifyDepGet(name string, isNil bool, class DepClassification, log *zap.Logger) error {
	if !isNil {
		return nil
	}
	switch class {
	case DepRequired:
		return fmt.Errorf("%s: %w", name, ErrRequiredDepMissing)
	case DepOptionalDisabled:
		if log != nil {
			log.Warn("optional dep disabled, capability degraded",
				zap.String("dep", name),
				zap.String("class", class.String()))
		}
		return nil
	case DepTestOnly:
		return fmt.Errorf("%s: %w", name, ErrTestOnlyDepMissing)
	}
	return nil
}
