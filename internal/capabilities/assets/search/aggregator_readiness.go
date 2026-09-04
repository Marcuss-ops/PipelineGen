package search

// HasBackend reports whether the frozen backend registry contains the named
// backend. It is a narrow composition/readiness surface: callers can prove
// that a capability was actually registered without reaching into the
// registry or re-deriving readiness from unrelated clients.
func (a *Aggregator) HasBackend(name string) bool {
	if a == nil || a.backends == nil || name == "" {
		return false
	}
	for _, backend := range a.backends.All() {
		if backend != nil && backend.Name() == name {
			return true
		}
	}
	return false
}
