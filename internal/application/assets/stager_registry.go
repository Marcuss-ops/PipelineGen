// Package assets — stager_registry.go (Stock Cutover Commit 1.2, July 2026).
//
// SourceStagerRegistry is the typed resolver for the canonical
// assets.SourceStager port: orchestrators consume the resolver to
// pick the right per-source-kind adapter (youtube / http / artlist /
// drive / existing_catalog) without conditional branching in the
// pipeline code.
//
// godlike/06 SSOT — one canonical owner per fact:
//
//   "Which SourceStager adapter runs for a given SourceKind?" lives
//   here. The Stock Cutover previously had a LOCAL stockpipeline
//   SourceStager interface + NoopSourceStager + StageRequest shape
//   duplicated in internal/application/assets/providers/stock/
//   stockpipeline/stager.go; that local copy is now retired
//   (Commit 1.2). All callers route through assets.SourceStager +
//   assets.SourceStagerRegistry and the canonical adapters
//   (YouTubeStager, ArtlistStager, StockStager) which expose
//   `var _ assets.SourceStager = (*X)(nil)` compile-time
//   assertions pinned in their respective adapter files.
//
// godlike/07 typed-error contract: ErrSourceKindUnknown and
// ErrSourceStagerNil are sentinels reachable via errors.Is from
// any caller seam; Resolve returns ErrSourceKindUnknown wrapped
// with the failing kind in the message so log-scanners can
// surface which kind was missing.

package assets

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// SourceKind is the canonical per-source-type identifier that maps
// to a SourceStager adapter in SourceStagerRegistry.
//
// Five canonical kinds — extensible, but downstream consumers
// freeze on the canonical list until a follow-up introduces a
// new kind via a godlike/07 deprecation record.
//
//   - youtube           : yt-dlp-backed source fetcher
//   - http              : direct URL downloads (CDN, mirror, raw file)
//   - artlist           : Artlist HLS/m3u8 + progressive MP4
//   - drive             : existing Drive file (no download needed
//     because the asset is already on Drive)
//   - existing_catalog  : lookup of an already-ingested stock
//     asset (no download — coordinate via
//     the catalog index)
type SourceKind string

const (
	SourceKindYouTube         SourceKind = "youtube"
	SourceKindHTTP            SourceKind = "http"
	SourceKindArtlist         SourceKind = "artlist"
	SourceKindDrive           SourceKind = "drive"
	SourceKindExistingCatalog SourceKind = "existing_catalog"
)

// CanonicalSourceKinds returns all 5 canonical SourceKind values
// in deterministic (lexicographic) order. Useful for composition
// roots that want to pre-register the 5 canonical adapters in a
// single loop, and for HealthCheck-style diagnostics that want a
// stable iteration surface.
func CanonicalSourceKinds() []SourceKind {
	return []SourceKind{
		SourceKindYouTube,
		SourceKindHTTP,
		SourceKindArtlist,
		SourceKindDrive,
		SourceKindExistingCatalog,
	}
}

// ErrSourceKindUnknown is returned by SourceStagerRegistry.Resolve
// when the requested SourceKind was not registered. Reachable via
// errors.Is(err, ErrSourceKindUnknown) from any caller seam.
var ErrSourceKindUnknown = errors.New("assets.SourceStagerRegistry: source kind not registered")

// ErrSourceStagerNil is returned by SourceStagerRegistry.Register
// when the supplied SourceStager is nil (typed-nil probe matches
// interfaces whose underlying pointer is nil). Reachable via
// errors.Is(err, ErrSourceStagerNil) from any caller seam.
var ErrSourceStagerNil = errors.New("assets.SourceStagerRegistry: nil SourceStager adapter")

// ErrSourceKindAlreadyRegistered is returned by Register when the
// same SourceKind is registered twice. Composition-root patterns
// should fail-closed on this — silent overwrite breaks SSOT
// ("which adapter owns this kind?") silently.
var ErrSourceKindAlreadyRegistered = errors.New("assets.SourceStagerRegistry: source kind already registered")

// SourceStagerRegistry maps SourceKind → assets.SourceStager.
//
// Thread-safe via sync.RWMutex (zero-default writes; lock-free
// reads after construction). Composition roots construct a
// registry once, register the 5 canonical adapters via Register,
// then freeze by convention (no Freeze method — pattern matches
// providers.Registry semantics where the registry is wired once
// and reads from then on).
//
// godlike/06 SSOT: this registry is the ONLY canonical resolver
// for "given a SourceKind, which SourceStager runs". Any future
// per-source-kind branching must route through here. Inline
// type-switch dispatch in callers is an antipattern.
type SourceStagerRegistry struct {
	mu      sync.RWMutex
	entries map[SourceKind]SourceStager
}

// NewSourceStagerRegistry returns an empty, mutable registry.
func NewSourceStagerRegistry() *SourceStagerRegistry {
	return &SourceStagerRegistry{
		entries: make(map[SourceKind]SourceStager, len(CanonicalSourceKinds())),
	}
}

// Register adds a SourceStager adapter for the given kind.
//
//   - ErrSourceStagerNil           if stager is nil
//     (the typed-nil probe guards against
//     `var s assets.SourceStager = someNilPtr`).
//   - ErrSourceKindAlreadyRegistered if kind is already present.
//
// Composition-root paths feed Register errors into log+drop per
// godlike/07; tests use MustRegister.
func (r *SourceStagerRegistry) Register(kind SourceKind, stager SourceStager) error {
	if stager == nil {
		return fmt.Errorf("%w: kind=%q", ErrSourceStagerNil, kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[kind]; exists {
		return fmt.Errorf("%w: kind=%q", ErrSourceKindAlreadyRegistered, kind)
	}
	r.entries[kind] = stager
	return nil
}

// MustRegister is the panic-on-error variant. Used by composition
// roots + tests where duplicate registration is a wiring bug
// rather than a runtime signal.
func (r *SourceStagerRegistry) MustRegister(kind SourceKind, stager SourceStager) {
	if err := r.Register(kind, stager); err != nil {
		panic(fmt.Sprintf("assets.SourceStagerRegistry.MustRegister: %v", err))
	}
}

// Resolve returns the adapter registered for kind, or
// ErrSourceKindUnknown.
// Compile-time guarantee: returns nil + non-nil error when the
// kind is unregistered, so callers can branch without an extra
// nil check.
func (r *SourceStagerRegistry) Resolve(kind SourceKind) (SourceStager, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.entries[kind]
	if !ok {
		return nil, fmt.Errorf("%w: kind=%q", ErrSourceKindUnknown, kind)
	}
	return s, nil
}

// Has reports whether a SourceStager is registered for kind. Cheap
// (RLock + map presence check) — use this for "should I take the
// registry path or fall through?" gating without paying the Resolve
// error-allocation cost.
func (r *SourceStagerRegistry) Has(kind SourceKind) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.entries[kind]
	return ok
}

// Kinds returns the registered kinds sorted lexicographically.
// Useful for diagnostics + HealthCheck in follow-ups.
func (r *SourceStagerRegistry) Kinds() []SourceKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SourceKind, 0, len(r.entries))
	for k := range r.entries {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
