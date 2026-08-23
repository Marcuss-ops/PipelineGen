// Package delivery provides the configured application facade for the canonical Drive publish contract.
//
// registry.go owns the PUBLIC surface of the destination registry:
// the type-level interface that every endpoint and every job that
// uploads a file to Google Drive consumes.
//
// Fit-for-purpose split (split-delivery-registry, July 2026):
//
//	internal/platform/delivery/registry.go            // configured registry facade (this file)
//	internal/platform/delivery/registry_lifecycle.go  // the policy table
//	internal/platform/delivery/registry_transport.go  // the per-destination path builders
//
// The split separates two distinct concerns without changing callers:
//
//   - registry.go (this file): the type surface — PathBuilder,
//     DestinationPolicy, DestinationRegistry — and the lookup methods
//     (Has / Resolve / Keys). The ctor NewDestinationRegistry lives
//     here as the canonical entry point; its body delegates the
//     policy-table construction to registry_lifecycle.go.
//
//   - registry_lifecycle.go owns the LIFECYCLE of the destination
//     table: which DestinationKey exists, what its default root folder
//     and ConflictPolicy are. This is the single canonical owner of
//     "what destinations exist" (godlike/06 SSOT) — adding a new
//     destination means adding exactly one policy entry here.
//
//   - registry_transport.go owns the TRANSPORT concern: how to compute
//     the destination folder given a PublishRequest. All PathBuilder
//     implementations live here, alongside the namespace-wrapping
//     helpers (withNamespace, maybeWrapNamespace) and the typed error
//     sentinel.
//
// Wire-protocol preservation (P-2 invariant, July 2026):
//
//   - Public methods (NewDestinationRegistry, Has, Resolve, Keys).
//   - Public types (PathBuilder, DestinationPolicy, DestinationRegistry).
//   - Behaviour of every lookup and the resulting
//     DestinationRegistry.policies map (key set, per-key
//     DestinationPolicy fields, Namespace / RootFolderID /
//     ConflictPolicy / RequireSubpath / PathBuilder closures) are
//     BIT-IDENTICAL pre- and post-split.
//
// Consumers across the codebase (≥ 145 importers — see
// cmd/archcheck/scan/percheck_root_override.go for the contract
// allowlist) continue to use the same exported names with no
// changes required.
package delivery

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// PathBuilder computes the folder path segments for a given PublishRequest.
// Each segment becomes a nested Drive folder under the destination's root.
// The function MUST sanitise every segment via pathutil.SafeFolderName.
// Returning an empty slice is valid only when RequireSubpath is false.
type PathBuilder func(req PublishRequest) ([]string, error)

// DestinationPolicy defines how a DestinationKey resolves to a Drive path
// AND how to handle a filename collision in the resolved folder.
//
// The DestinationRegistry holds exactly one policy per DestinationKey.
// All fields are captured eagerly at construction time; the registry is
// immutable after creation. Per-destination ConflictPolicy is set here
// — NOT threaded through PublishRequest — so callers that omit the
// field cannot accidentally trigger uploads against the wrong policy.
//
// P1.1 (July 2026): the legacy zero = ConflictOverwrite default was
// unsafe; a caller forgetting to pick a policy would silently overwrite
// any existing Drive file under that name. The publisher now consults
// this field when req.ConflictPolicy == 0 (the "caller didn't pick"
// path) so the safety contract lives in the registry, not as a hidden
// zero-value trap. Explicit PublishRequest.ConflictPolicy always wins.
type DestinationPolicy struct {
	// RootFolderID returns the Drive folder ID that serves as the root
	// for this destination. Derived from config at construction time.
	RootFolderID string

	// PathBuilder computes nested folder segments under RootFolderID.
	PathBuilder PathBuilder

	// RequireSubpath, when true, rejects uploads that would land directly
	// in the root folder (i.e. when PathBuilder returns an empty slice).
	// This prevents accidental pollution of top-level Drive folders.
	RequireSubpath bool

	// Namespace is the canonical top-level directory name for this
	// destination when the unified media_root_folder is active (see
	// config.DriveConfig.IsUsingMediaRoot). When empty, the namespace
	// is not prepended — the destination either has its own dedicated
	// root folder or does not require namespace isolation.
	//
	// Canonical namespace values:
	//   clips, stock, artlist, images, voiceovers, books, scripts,
	//   sound_effects, documents, admin
	//
	// Per godlike/06 SSOT (one canonical owner per fact): the namespace
	// is assigned ONCE at registry construction and never mutated.
	Namespace string

	// ConflictPolicy is the registry-driven default for filename
	// collisions in the resolved Drive folder. The publisher applies
	// this when PublishRequest.ConflictPolicy is the zero value (the
	// "caller didn't pick" path). MUST be one of ConflictOverwrite /
	// ConflictSkip / ConflictRename — zero is NOT a valid default and
	// would silently fall back to ConflictOverwrite via the PutFile
	// seam.
	//
	// Semantics per destination (P1.1 mapping, July 2026):
	//   - YouTube clip / Artlist / Stock / Image / Voiceover /
	//     SoundEffect → ConflictSkip  (immutable / versioned assets,
	//     collisions are content-hash dupes that should not overwrite)
	//   - Book / Script / Document → ConflictOverwrite (regenerable
	//     outputs, latest version wins)
	//
	// Operator overrides (e.g. an explicit admin reupload that wants
	// ConflictOverwrite on a normally-Skip destination) MUST thread
	// PublishRequest.ConflictPolicy explicitly; the registry default
	// only applies when the caller left it at zero.
	ConflictPolicy ConflictPolicy
}

// DestinationRegistry is the single authority that maps a DestinationKey
// to a root folder and a path structure. Adding a new capability means
// adding one policy entry here — no endpoint-level Drive logic is permitted.
type DestinationRegistry struct {
	policies map[DestinationKey]DestinationPolicy
}

// NewDestinationRegistry builds the registry from application config.
// Every DestinationKey has exactly one policy. The root folder IDs and
// per-destination ConflictPolicy are captured eagerly (at construction
// time) so the registry is immutable after creation.
//
// Per-destination ConflictPolicy mapping (P1.1, July 2026) — see
// DestinationPolicy.ConflictPolicy for the rationale. Pure data:
//
//	Skip             → immutable / versioned asset (do not overwrite
//	                    silently when an existing Drive file under the
//	                    same name is found)
//	Overwrite        → regenerable artefact where the latest version
//	                    wins (caller can override per request, e.g. an
//	                    explicit admin reupload wants Overwrite on a
//	                    normally-Skip destination — PublishRequest is
//	                    the surface for that override)
//
// Implementation note (split-delivery-registry, July 2026): the body
// of this constructor delegates to buildDestinationPolicies (in
// registry_lifecycle.go), which owns the per-DestinationKey policy
// table. Lookup methods (Has / Resolve / Keys) live on this file
// because they are part of the registry's public surface.
func NewDestinationRegistry(cfg *config.Config) *DestinationRegistry {
	return &DestinationRegistry{
		policies: buildDestinationPolicies(cfg),
	}
}

// Has reports whether the registry contains a policy for the given key.
func (r *DestinationRegistry) Has(key DestinationKey) bool {
	_, ok := r.policies[key]
	return ok
}

// Resolve returns the policy for the given key, or an error if the key
// is not registered. Callers MUST check Has() first when iterating over
// a known set of keys (e.g. in tests).
func (r *DestinationRegistry) Resolve(key DestinationKey) (DestinationPolicy, error) {
	p, ok := r.policies[key]
	if !ok {
		return DestinationPolicy{}, fmt.Errorf("delivery: unknown destination key %q", key)
	}
	return p, nil
}

// Keys returns all registered destination keys. Useful for diagnostics
// and completeness tests.
func (r *DestinationRegistry) Keys() []DestinationKey {
	keys := make([]DestinationKey, 0, len(r.policies))
	for k := range r.policies {
		keys = append(keys, k)
	}
	return keys
}
