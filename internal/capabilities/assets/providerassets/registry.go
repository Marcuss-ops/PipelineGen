package providerassets

import (
	"errors"
	"sync"
)

// Sentinel errors for the registry.
var (
	ErrAlreadyRegistered = errors.New("providerassets: adapter already registered")
	ErrNotFound          = errors.New("providerassets: adapter not found")
	ErrNilAdapter        = errors.New("providerassets: nil adapter")
	ErrEmptyName         = errors.New("providerassets: adapter name is empty")
	ErrRegistryFrozen    = errors.New("providerassets: registry is frozen")
)

// Registry is a catalog of ProviderAdapter implementations. It is safe
// for concurrent use and becomes immutable after Freeze.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]ProviderAdapter
	frozen  bool
}

// NewRegistry returns an empty registry.
