// File errors.go — canonical typed-error sentinels for the resolver
// adapter. Extracted from adapter.go per AGENTS.md Pattern 5 v2
// (1 concetto per file; code-motion pura, zero logica cambiata).
//
// godlike/07 NO-FAKE-AVAILABILITY typed-error contract: each sentinel
// is the canonical SOLE owner of its error key. Callers probe via
// errors.Is without unwrapping parse-fragments.
//
// godlike/06 SSOT one-canonical-owner-per-fact: these sentinels live
// ONLY here. Other files in the package resolve them by reference.
package resolver

import "errors"

// ── typed-error contract (godlike/07 NO-FAKE-AVAILABILITY) ────────────────
//
// The adapter returns the canonical typed-error envelope that the port
// declares (ErrLocationResolverEmpty / ErrLocationResolverDestinationUnsupported
// / ErrLocationResolverIncompatibleFields). Each error message includes
// the destination key + the offending field name so a failing caller
// can probe via errors.Is + errors.As + log-scanner grep.
//
// Adapter-local sentinels for the real Drive FolderEnsurer integration
// (CUTOVER C9). The real Drive.EnsureFolderPath call is now wired via
// the Pattern-0 FolderEnsurer port. Operators reading diagnostics see
// `(<mode>=ensure-api)` for the real Drive path.
var (
	// ErrFolderEnsurerNotWired surfaces when the real Drive
	// FolderEnsurer dependency was NOT injected at composition time
	// but a non-empty Location is being resolved. This is the
	// fail-closed gate that prevents silent stub-mode folder-ids
	// from being consumed downstream. godlike/07 NO-FAKE-AVAILABILITY:
	// the adapter never synthesises a stub folder-id in production.
	ErrFolderEnsurerNotWired = errors.New(
		"resolver: Drive FolderEnsurer not wired at composition time (set resolverAdapter.WithFolderEnsurer or pass non-nil to NewAdapter)",
	)

	// ErrFolderEnsureFailed surfaces when the real Drive
	// EnsureFolder call fails (re-export of the canonical
	// Drive-side error). godlike/07 typed-error contract:
	// callers probe via errors.Is(err, ErrFolderEnsureFailed).
	ErrFolderEnsureFailed = errors.New(
		"resolver: Drive.EnsureFolder call failed",
	)

	// ErrFolderIDNonAlphanumeric surfaces if a per-destination mapping
	// resolves to a folder-id containing characters that Drive
	// cannot accept (whitespace, slashes, etc.). The adapter
	// returns this BEFORE any Drive-side call so caller stops
	// dispatching the malformed request.
	ErrFolderIDNonAlphanumeric = errors.New(
		"resolver: resolved folder-id must be non-empty alphanumeric (drive-safe)",
	)

	// ErrDestinationNoRootFolder surfaces when a destination has
	// NEITHER a specific root folder (e.g. stock_root_folder) NOR
	// a MediaRootFolder configured. The operator MUST set at least
	// one of the two in config.yaml or via VELOX_DRIVE_* env vars.
	//
	// DoD #5 (SEMANTIC-LOCATION-API-2026-07-06): "se root specifica
	// manca → usa media_root_folder + namespace; se entrambe mancano
	// → errore chiaro". This sentinel is the godlike/07
	// NO-FAKE-AVAILABILITY enforcement — the adapter never
	// synthesises a root folder when none is configured.
	ErrDestinationNoRootFolder = errors.New(
		"resolver: destination has no configured root folder",
	)
)
