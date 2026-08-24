package providers

import (
	"errors"
	"sync"
	"time"
)

// Sentinel errors for the registry.
var (
	// ErrAlreadyRegistered is returned when a provider with the
	// same Name is already present. The returned error is wrapped
	// with %w so errors.Is(err, ErrAlreadyRegistered) matches.
	ErrAlreadyRegistered = errors.New("providers: provider already registered")

	// ErrFrozen is returned when Register is called after Freeze.
	ErrFrozen = errors.New("providers: registry frozen")

	// ErrNilProvider is returned when Register is called with a nil
	// Provider interface value.
	ErrNilProvider = errors.New("providers: nil provider")

	// ErrEmptyName is returned when Register is called with a
	// provider whose Name() returns "".
	ErrEmptyName = errors.New("providers: provider name is empty")

	// ErrNilEntry is returned when RegisterEntry is called with a
	// nil ProviderEntry pointer.
	ErrNilEntry = errors.New("providers: nil provider entry")
)

// DefaultHealthTimeout is the timeout applied to Provider-level
// health probes when the entry's Timeout field is left at its zero
// value. Per S3a (June 2026) spec: "timeout config (default 5s)".
const DefaultHealthTimeout = 5 * time.Second

// Registry is a one-shot, freezeable provider catalog.
// Register/Freeze run once during composition root wiring; after
// Freeze() any call to Register() returns ErrFrozen.
//
// S3a (June 2026): storage shape migrated from
// `entries map[string]Provider` to `entries map[string]*ProviderEntry`.
// All lookup methods were updated to return Provider (stripping the
// Entry wrapper) so the existing public API surface is bit-for-bit
// preserved at the lookup-site. New code paths (HealthCheck +
// Entries + RegisterEntry) operate on the Entry surface directly.
//
// Concurrency: writes use sync.RWMutex.Lock; lookups
// (Get, All, ByCapability, IsFrozen, Entries) use RLock and are
// effectively wait-free after Freeze. Freeze is naturally idempotent
// — concurrent calls converge on the same final state with no data
// dependency.
//
// Determinism: All(), ByCapability(), and Entries() return slices
// sorted by Name() so callers can rely on a stable iteration order.
//
// Companion files:
//   - registry_entry.go        types: HealthProbe, ProviderCapabilityDetail,
//     ProviderCapabilitySet, ProviderEntry + methods
//   - registry_registration.go Register, RegisterEntry, registerEntryLocked,
//     Freeze
//   - registry_lookup.go       Get, GetEntry, ByCapability, All, Entries,
//     IsFrozen, HealthCheck, RegisterSearch, RegisterFetch
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*ProviderEntry
	frozen  bool
}

// NewRegistry returns an empty, mutable registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*ProviderEntry)}
}
