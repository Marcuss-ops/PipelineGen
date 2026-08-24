package providers

import (
	"fmt"
	"reflect"
)

// Register adds a provider under its Name(). The entry's Capabilities
// are populated from p.Capabilities() ([]Capability) — the typed
// ProviderCapabilitySet pointers are filled when the corresponding
// tag is advertised by the provider. HealthProbe / Timeout /
// MaxResults / MaxPages left zero-valued by default (zero-defaults
// per S3a spec wording "zero-value defaults").
//
// Returns:
//   - ErrNilProvider        if p is the zero Provider value;
//   - ErrEmptyName          if p.Name() returns "";
//   - ErrFrozen             if the registry is already frozen;
//   - ErrAlreadyRegistered  if a provider with the same Name exists.
//
// The ErrEmptyName check runs before Lock to short-circuit malformed
// providers without acquiring the registry's mutex.
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return ErrNilProvider
	}
	// Detect typed-nil interfaces: `var p Provider = someNilPtr`
	// produces a non-nil interface whose underlying pointer is nil;
	// calling a method on it would panic. The Kind==Ptr guard
	// short-circuits non-pointer kinds where IsNil would itself
	// panic.
	if rv := reflect.ValueOf(p); rv.Kind() == reflect.Ptr && rv.IsNil() {
		return ErrNilProvider
	}
	name := p.Name()
	if name == "" {
		return ErrEmptyName
	}
	entry := &ProviderEntry{
		Name:     name,
		Provider: p,
	}
	// Migration step: map Provider.Capabilities() []Capability to the
	// typed ProviderCapabilitySet pointer fields. Each pointer is
	// left nil when the provider does NOT advertise the capability
	// (zero-default path makes "no enrichment declared" the safe
	// baseline).
	for _, c := range p.Capabilities() {
		switch c {
		case CapabilitySearch:
			if entry.Capabilities.Search == nil {
				entry.Capabilities.Search = &ProviderCapabilityDetail{}
			}
		case CapabilityFetch:
			if entry.Capabilities.Fetch == nil {
				entry.Capabilities.Fetch = &ProviderCapabilityDetail{}
			}
		case CapabilityVideo:
			if entry.Capabilities.Video == nil {
				entry.Capabilities.Video = &ProviderCapabilityDetail{}
			}
		case CapabilityImage:
			if entry.Capabilities.Image == nil {
				entry.Capabilities.Image = &ProviderCapabilityDetail{}
			}
		case CapabilityMusic:
			if entry.Capabilities.Music == nil {
				entry.Capabilities.Music = &ProviderCapabilityDetail{}
			}
		case CapabilityVoice:
			if entry.Capabilities.Voice == nil {
				entry.Capabilities.Voice = &ProviderCapabilityDetail{}
			}
		case CapabilityScript:
			if entry.Capabilities.Script == nil {
				entry.Capabilities.Script = &ProviderCapabilityDetail{}
			}
		}
	}
	return r.registerEntryLocked(entry)
}

// RegisterEntry adds a fully-formed ProviderEntry to the registry.
// S3a (June 2026): the canonical entry-point for callers that want
// to attach per-capability details, a HealthProbe, Timeout, or
// MaxResults/MaxPages limits. Register(p) is a thin wrapper that
// builds the entry's name + capabilities from a Provider; this
// method accepts the already-typed entry as-is.
//
// Returns:
//   - ErrNilEntry           if entry is nil;
//   - ErrEmptyName          if entry.Name is "" AND entry.Provider is nil
//     (forward-compat: callers may pass entry
//     with explicit Name but no Provider);
//   - ErrFrozen             if the registry is already frozen;
//   - ErrAlreadyRegistered  if a provider with the same Name exists.
func (r *Registry) RegisterEntry(entry *ProviderEntry) error {
	if entry == nil {
		return ErrNilEntry
	}
	if entry.Name == "" {
		if entry.Provider == nil {
			return ErrEmptyName
		}
		entry.Name = entry.Provider.Name()
		if entry.Name == "" {
			return ErrEmptyName
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.entries[entry.Name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, entry.Name)
	}
	r.entries[entry.Name] = entry
	return nil
}

// registerEntryLocked is the internal helper that takes the lock and
// inserts the entry. Called by Register (back-compat path) and
// RegisterEntry (canonical path) so the lock acquisition pattern is
// in one place. Caller must hold (or skip, via the wrapping Lock in
// RegisterEntry) the appropriate lock contract.
func (r *Registry) registerEntryLocked(entry *ProviderEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrFrozen
	}
	if _, exists := r.entries[entry.Name]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, entry.Name)
	}
	r.entries[entry.Name] = entry
	return nil
}

// Freeze locks the registry. Idempotent: safe to call multiple times.
// After Freeze, Register returns ErrFrozen and lookups become
// effectively wait-free.
func (r *Registry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}
