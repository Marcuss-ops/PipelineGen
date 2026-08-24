package adminconsole

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the canonical set of administrative entities.
type Registry struct {
	mu       sync.RWMutex
	entities map[string]EntityDescriptor
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{entities: make(map[string]EntityDescriptor)}
}

// Register adds an entity descriptor to the registry.
// It panics if the descriptor has no Key or if an entity with the same
// key was already registered.
func (r *Registry) Register(d EntityDescriptor) {
	if d.Key == "" {
		panic("adminconsole: entity descriptor Key is required")
	}
	if d.PrimaryKey == "" {
		d.PrimaryKey = "id"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entities[d.Key]; ok {
		panic(fmt.Sprintf("adminconsole: entity %q already registered", d.Key))
	}
	r.entities[d.Key] = d
}

// Get returns the descriptor for an entity key.
func (r *Registry) Get(key string) (EntityDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.entities[key]
	return d, ok
}

// Exists reports whether an entity with the given key is registered.
func (r *Registry) Exists(key string) bool {
	_, ok := r.Get(key)
	return ok
}

// List returns all registered entity descriptors sorted by label.
func (r *Registry) List() []EntityDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EntityDescriptor, 0, len(r.entities))
	for _, d := range r.entities {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
