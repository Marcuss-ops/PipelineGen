//go:build drivepolicypkgtest

// Package delivery — registry_test_factories.go (June 2026).
//
// NewDestinationRegistryWithPolicies was introduced in commit 70f2b6c8
// for the P0 #2 resolveDestination test pair (publish + ResolveFolder
// symmetric enforcement). Production code never imports this factory
// — only the publisher tests under //go:build drivepolicypkgtest
// consume it. Gating the symbol behind the build tag keeps the
// production `go doc` surface clean and prevents future production
// code from accidentally depending on a test affordance.
//
// Build tag (June 2026): //go:build drivepolicypkgtest. Default
// `go build` skips this file; `go test -tags drivepolicypkgtest` compiles
// it. The companion test file
// internal/infrastructure/drive/publisher_policies_test.go carries the
// same tag so the 3 dependent tests are only compiled in the same
// `go test` invocation that enables the factory.
package delivery

// NewDestinationRegistryWithPolicies constructs a registry with caller-
// supplied policies. Production code MUST use NewDestinationRegistry;
// this constructor exists so tests can inject degenerate PathBuilders
// (e.g. an "always returns empty segments" stub) without registering
// a new canonical destination. Each entry's RequireSubpath is honoured
// exactly the way it would be in the canonical registry.
//
// Reason for //go:build drivepolicypkgtest gate: this is the canonical
// P0 #2 test affordance. The companion tests verify symmetric Publish +
// ResolveFolder enforcement of RequireSubpath when the PathBuilder
// returns an empty segment slice. Without the factory, production
// binaries would carry an unused but exported symbol — an attractive
// nuisance for future contributors who might rely on it accidentally.
// The build tag ensures the symbol exists ONLY when tests opt in.
func NewDestinationRegistryWithPolicies(policies map[DestinationKey]DestinationPolicy) *DestinationRegistry {
	return &DestinationRegistry{policies: policies}
}
