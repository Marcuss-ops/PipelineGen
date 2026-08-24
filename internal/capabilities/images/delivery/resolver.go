// Package destinations — DestinationResolver port and YamlResolver
// concrete implementation (FASE 2D EXPAND, July 2026).
//
// DestinationResolver is the canonical typed port (Pattern 0) for
// "logical destinationKey -> concrete Drive folder ID" lookups.
//
// YamlResolver reads config/image_destinations.yaml once at
// NewYamlResolver time and caches the parsed mapping. The cache is
// read-only after construction (no Reload method yet — operator-driven
// config changes follow the restart convention until a reload-supported
// alternative is requested).
package delivery

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ── Port (Pattern 0) ──────────────────────────────────────────────────

// DestinationResolver is the SINGLE authoritative port for resolving
// a logical destinationKey into a concrete Destination. Implementations
// MUST be safe for concurrent use and case-insensitive on keys.
type DestinationResolver interface {
	Resolve(destinationKey string) (Destination, error)
}

// ── YAML constructor ──────────────────────────────────────────────────

// YamlResolver is the canonical DestinationResolver for production.
type YamlResolver struct {
	cache      map[string]string
	fallbackID string
}

// NewYamlResolver parses yamlPath and returns a YamlResolver.
// fallbackID is the destination used when a key is unknown OR maps to
// an empty string.
//
// Empty yamlPath or unreadable/malformed file -> fmt.Errorf at
// construction time (fail-closed at boot; operators see the
// misconfiguration at startup, not in a runtime traceback).
func NewYamlResolver(yamlPath, fallbackID string) (*YamlResolver, error) {
	if yamlPath == "" {
		return nil, fmt.Errorf("destinations.NewYamlResolver: yamlPath is empty")
	}

	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("destinations.NewYamlResolver: read %q: %w", yamlPath, err)
	}

	var parsed destinationsFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("destinations.NewYamlResolver: unmarshal %q: %w", yamlPath, err)
	}

	cache := make(map[string]string, len(parsed.Destinations))
	for k, v := range parsed.Destinations {
		cache[strings.ToLower(k)] = v
	}

	return &YamlResolver{cache: cache, fallbackID: fallbackID}, nil
}

// Convenience constructor for tests (in-memory map; no file I/O).
func NewYamlResolverFromMap(entries map[string]string, fallbackID string) *YamlResolver {
	cache := make(map[string]string, len(entries))
	for k, v := range entries {
		cache[strings.ToLower(k)] = v
	}
	return &YamlResolver{cache: cache, fallbackID: fallbackID}
}

// ── Resolve (port impl) ──────────────────────────────────────────────

// Resolve looks up the case-insensitive destinationKey in the cached
// YAML map.
//
// Error semantics:
//
//   - exact-key hit on non-empty value       -> (Destination{folder}, nil)
//   - exact-key hit on empty value + fallback -> (Destination{fallback}, nil)
//   - unknown key + fallback configured       -> (Destination{fallback}, nil)
//   - any path + fallback == ""               -> (zero, ErrDestinationNotFound)
//
// Pure read-only operation; safe for concurrent use.
func (r *YamlResolver) Resolve(destinationKey string) (Destination, error) {
	if r == nil {
		return Destination{}, fmt.Errorf("%w: resolver is nil", ErrDestinationNotFound)
	}

	key := strings.ToLower(destinationKey)

	if v, ok := r.cache[key]; ok && v != "" {
		return Destination{DriveFolderID: v}, nil
	}

	if r.fallbackID != "" {
		return Destination{DriveFolderID: r.fallbackID}, nil
	}

	return Destination{}, fmt.Errorf(
		"%w: %q (no entry in config/image_destinations.yaml, no fallback configured)",
		ErrDestinationNotFound, destinationKey,
	)
}

// ── Compile-time interface assertion (Pattern 0) ──────────────────────

var _ DestinationResolver = (*YamlResolver)(nil)
