// Package resolver — internal/infrastructure/drive/resolver — stub
// adapter for the canonical Pattern-0 LocationResolverPort declared in
// internal/application/assets/sourcing/ports.go.
//
// SEMANTIC-LOCATION-API-2026-07-06 Wave 7 (PR-RESOLVER-PORT-EXTRACT):
// this adapter is the production-time stub that ships with the Wave 7
// wiring. It implements the canonical per-destination segment-shape
// mapping table (mirroring BuildPublishRequest's switch in
// internal/platform/delivery/mapper.go) and returns a
// deterministic folder-id string per destination.
//
// godlike/07 NO-FAKE-AVAILABILITY: when the real Drive API integration
// is bounded (CUTOVER), the adapter is extended to call Drive.EnsureFolder
// for the segment-shape; the stub returns a typed prefix-fold-id so
// end-to-end tracing still binds the resolved folder to the location.
// Today it lives in-memory and survives only for the duration of the
// process. Forward-pointer: external DriveFolderCachePort (CUTOVER-product
// surface) for cross-process persistence.
//
// godlike/06 SSOT one-canonical-owner-per-fact: the per-destination
// mapping table that mirrors BuildPublishRequest's switch lives ONLY
// in mapping.go (extracted from this slim adapter per AGENTS.md
// Pattern 5 v2, code-motion pura). BuildPublishRequest continues to own
// the AssetLocationField→PublishRequestField translation (delivery-side);
// this adapter owns the AssetLocationField→FolderIDSegmentShape
// translation (drive-side). Two layers, no overlap.

package resolver

import (
	"context"
	"fmt"
	"strings"

	sourcing "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Pattern 0 port: FolderEnsurer ────────────────────────────────────────

// FolderEnsurer is the narrow Pattern 0 port the resolver adapter
// consumes to create/resolve real Drive folders. Defined here in the
// resolver package (NOT in the parent drive package) to break the
// import cycle: resolver is a child of drive, so it cannot import
// drive.Admin or drive.EnsureFolderPath directly.
//
// The composition root in internal/app/ creates a thin adapter that
// wraps drive.EnsureFolderPath(ctx, admin, rootID, segments...) and
// injects it here.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this interface is the
// canonical SOLE typed contract between the resolver and the Drive
// folder-creation surface. No other interface in the codebase
// duplicates this shape for the resolver's consumption.
type FolderEnsurer interface {
	// EnsureFolder walks the given segments under rootID, creating
	// each folder if it doesn't already exist. Returns the final
	// (leaf) folder's Drive File.ID.
	EnsureFolder(ctx context.Context, rootID string, segments ...string) (string, error)
}

// ── adapter struct ────────────────────────────────────────────────────────

// Adapter implements the canonical Pattern-0 LocationResolverPort
// (declared in internal/application/assets/sourcing/ports.go).
//
// godlike/06 SSOT one-canonical-owner-per-fact: this struct is the
// canonical SOLE adapter for the resolver port. No other file in
// `internal/infrastructure/drive/` produces a LocationResolverPort
// implementation. Composition root wires ONE instance per process
// boot (newAssetRegisterService) and shares it across the sourcing
// façade via .WithLocationResolver(...).
//
// godlike/07 typed-error contract: every Resolve exit path returns a
// typed sentinel (or a Go-1.20+ dual-%w chain wrapping the canonical
// sentinel + the underlying cause) so callers can probe via errors.Is.
//
// godlike/07 no-fake-availability: when the adapter fails to produce
// a folder-id for a non-empty Location, it returns a typed sentinel
// rather than synthesizing a synthetic prefix-fold-id. Doing the
// latter would silently land assets in the wrong folder — operator
// observability requires fail-closed semantics.
//
// DoD #5 (SEMANTIC-LOCATION-API-2026-07-06): the adapter holds the
// full DriveConfig so it can resolve per-destination root folders
// (stock_root_folder, clips_root_folder, etc.) with fallback to
// MediaRootFolder + namespace. When neither is set, Resolve returns
// ErrDestinationNoRootFolder (fail-closed at call-time).
type Adapter struct {
	mediaRoot     string             // cfg.Drive.MediaRootFolder (cached for rootForDestination fallback)
	cfg           config.DriveConfig // per-destination root resolution (DoD #5)
	log           resolverLogger
	folderEnsurer FolderEnsurer // real Drive folder creation (nil → ErrFolderEnsurerNotWired at call-time)
}

// NewAdapter creates a fail-closed resolver adapter wired to the
// canonical DriveConfig.
//
// driveCfg is the full Drive configuration — the adapter resolves
// per-destination roots via the priority chain:
//  1. destination-specific root (e.g. StockFolder() → StockRootFolder)
//  2. MediaRootFolder (unified fallback)
//  3. ErrDestinationNoRootFolder (fail-closed)
//
// log is the optional narrow logging port — nil falls back to a
// no-op logger so test fixtures do not need a real logger.
//
// godlike/07 fail-closed-at-construction: NewAdapter returns
// (*Adapter, error) so future validation gates have a typed-error
// envelope to surface on composition-time misconfiguration.
// Today the gate is permissive (empty DriveConfig is allowed for
// tests; the per-Resolve gate rejects missing roots at call-time).
//
// DoD #5 (SEMANTIC-LOCATION-API-2026-07-06): the per-destination
// root resolution happens ON-DEMAND at Resolve time, not at
// construction time — an operator can add a root folder to config
// without restarting the process (the adapter re-reads cfg on each
// Resolve call). The MediaRootFolder is cached at construction for	// the destination-specific roots are
// resolved fresh on each call.
func NewAdapter(driveCfg config.DriveConfig, log resolverLogger) (*Adapter, error) {
	// Round-2 SHOULD-FIX (2026-07-06): nil-logger fail-closed default.
	if log == nil {
		log = nopResolverLogger{}
	}
	return &Adapter{
		mediaRoot: strings.TrimSpace(driveCfg.MediaRootFolder),
		cfg:       driveCfg,
		log:       log,
	}, nil
}

// WithFolderEnsurer injects the real Drive FolderEnsurer dependency.
// This is the canonical fluent setter the composition root calls to
// wire the real Drive.EnsureFolderPath adapter. Without it, Resolve
// returns ErrFolderEnsurerNotWired for any non-empty Location.
//
// godlike/07 fail-closed-at-call-time: construction succeeds even
// without a FolderEnsurer (the process may boot without Drive
// credentials for local/dry-run tasks). The fail-closed gate fires
// at Resolve time when a non-empty Location is provided.
func (a *Adapter) WithFolderEnsurer(ensurer FolderEnsurer) *Adapter {
	if a == nil {
		return nil
	}
	a.folderEnsurer = ensurer
	return a
}

// WithLogger overrides the adapter's logger (fluent setter for tests).
//
// Round-2 SHOULD-FIX (2026-07-06): nil-logger fail-closed default. A
// nil-resolverLog passed in is replaced with nopResolverLogger{} so
// subsequent a.log.Warn / .Info / .Error calls do not silently dead-
// panic on Go's nil-interface receiver. Symmetric with NewAdapter's
// nil-defense (godlike/06 SSOT: same fail-closed contract on both
// construction paths).
func (a *Adapter) WithLogger(log resolverLogger) *Adapter {
	if a == nil {
		return nil
	}
	if log == nil {
		log = nopResolverLogger{}
	}
	a.log = log
	return a
}

// ── port-method entry-point ──────────────────────────────────────────────

// Resolve is the canonical LocationResolverPort.Resolve implementation.
//
// godlike/07 typed-error contract validation order (fail-fast-at-input):
//
//  1. loc.IsEmpty()     → ErrLocationResolverEmpty
//  2. dest unsupported  → ErrLocationResolverDestinationUnsupported
//  3. incompatible fields per destination → ErrLocationResolverIncompatibleFields
//  4. segment-shape mapping → return canonical folder-id
//
// godlike/06 SSOT: the per-destination segment-shape table is the ONLY
// place in the codebase where loc.Style / loc.Category / loc.Provider etc.
// are joined into a subpath list for a Drive folder-id. BuildPublishRequest
// in delivery/mapper.go embodies a separate translation (location fields
// → PublishRequest fields). The two tables mirror each other in spirit
// but are distinct surfaces.
func (a *Adapter) Resolve(ctx context.Context, loc domaindelivery.AssetLocationInput, dest delivery.DestinationKey) (string, error) {
	if a == nil {
		return "", fmt.Errorf(
			"%w (resolver adapter not constructed — composition-root bug; check newAssetRegisterService .WithLocationResolver wiring)",
			sourcing.ErrLocationResolverEmpty,
		)
	}
	if loc.IsEmpty() {
		return "", fmt.Errorf(
			"%w: destination=%q",
			sourcing.ErrLocationResolverEmpty, dest,
		)
	}
	if dest == "" {
		return "", fmt.Errorf(
			"%w: empty destination key",
			sourcing.ErrLocationResolverDestinationUnsupported,
		)
	}

	// DoD #5: resolve per-destination root folder BEFORE field
	// validation (specific root > media_root + namespace > error).
	// Running root resolution first follows "fail-fast-at-infrastructure
	// > fail-fast-at-input" — if both root AND fields are misconfigured,
	// the operator sees the root error first, fixes it, then discovers
	// the field error (rather than the reverse two-pass dance).
	root, err := a.rootForDestination(dest)
	if err != nil {
		return "", err
	}

	// Incompatible-fields probe: drop fields that the destination
	// does not consume (mirrors BuildPublishRequest's per-destination
	// mandatory-check). Each pair records a (fieldLabel, destName) in
	// the error message so operator can see exactly which field is
	// the problem.
	var incompatibleFields []string
	for _, f := range incompatibleFieldProbe(dest) {
		switch f {
		case "style":
			if loc.Style != "" {
				incompatibleFields = append(incompatibleFields, "style")
			}
		case "provider":
			if loc.Provider != "" {
				incompatibleFields = append(incompatibleFields, "provider")
			}
		case "project":
			if loc.Project != "" {
				incompatibleFields = append(incompatibleFields, "project")
			}
		case "language":
			if loc.Language != "" {
				incompatibleFields = append(incompatibleFields, "language")
			}
		}
	}
	if len(incompatibleFields) > 0 {
		return "", fmt.Errorf(
			"%w: destination=%q carries non-applicable fields: [%s]",
			sourcing.ErrLocationResolverIncompatibleFields, dest, strings.Join(incompatibleFields, ","),
		)
	}

	// Round-2 SHOULD-FIX (2026-07-06): soft-ignored metadata probe.
	// Fields retained for downstream Qdrant indexing but OFF the
	// per-destination Drive subpath mapping emit a per-call Warn
	// log (via a.log.Warn) rather than hard-rejecting as
	// ErrLocationResolverIncompatibleFields. godlike/06 SSOT:
	// BuildPublishRequest in delivery/mapper.go also ignores these
	// fields without raising typed-sentinels — the resolver soft-ignores
	// to mirror that semantic (warn-only observability, no caller
	// rejection).
	//
	// godlike/07 typed-error contract: NONE of the soft-ignored fields
	// can flip the typed-sentinel returned by Resolve. The hard-
	// rejection list (style / provider / subject / name) is unchanged
	// for Voiceover/Book/Script destinations per the SHOULD-FIX scope.
	var softIgnoredFields []string
	for _, f := range softIgnoredFieldProbe(dest) {
		if f == "category" && loc.Category != "" {
			softIgnoredFields = append(softIgnoredFields, "category")
		}
	}
	if len(softIgnoredFields) > 0 {
		a.log.Warn(
			"resolver: soft-ignoring off-channel metadata fields (carried downstream for indexing, not used in folder-id)",
			"destination", string(dest),
			"soft_ignored_fields", strings.Join(softIgnoredFields, ","),
		)
	}

	// Per-destination segment-shape mapping (mirrors BuildPublishRequest
	// in delivery/mapper.go). The table-driven form keeps the two
	// canonical surfaces synchronize-able in future extensions.
	segments := segmentsForDestination(dest, loc)
	if len(segments) == 0 {
		return "", fmt.Errorf(
			"%w: destination=%q has no mapping for location fields=[cat=%q,sub=%q,name=%q]",
			sourcing.ErrLocationResolverDestinationUnsupported, dest,
			loc.Category, loc.Subject, loc.Name,
		)
	}

	// Per-destination mandatory-field gate (godlike/07 NO-FAKE-AVAILABILITY).
	// BuildPublishRequest enforces mandatory Location fields per destination
	// (e.g. Stock requires Category+Provider+Subject; Image requires
	// Style+Subject). When the resolver produces a segment-shape with an
	// empty mandatory slot — e.g. Voiceover+emptyProject, Stock+emptyProvider
	// — BuildPublishRequest would later fail; the resolver fails-fast-at-input
	// instead so callers see the typed_error at the resolver surface, not
	// after a partial folder-id has been computed downstream.
	if missing := mandatoryFieldGate(dest, segments); missing != "" {
		return "", fmt.Errorf(
			"%w: destination=%q has unmapped mandatory segment for location fields=[cat=%q,sub=%q,name=%q,style=%q,provider=%q,project=%q,language=%q]",
			sourcing.ErrLocationResolverDestinationUnsupported, dest,
			loc.Category, loc.Subject, loc.Name, loc.Style, loc.Provider, loc.Project, loc.Language,
		)
	}

	// Fail-closed gate: the real Drive FolderEnsurer must be wired
	// at composition time. Without it, the resolver cannot produce
	// a real Drive folder-id — returning a stub would silently land
	// assets in the wrong folder. godlike/07 NO-FAKE-AVAILABILITY.
	if a.folderEnsurer == nil {
		return "", fmt.Errorf(
			"%w: destination=%q, segments=[%s]",
			ErrFolderEnsurerNotWired, dest, strings.Join(segments, "/"),
		)
	}

	// Build the full segment list: destination key as the first
	// segment under root, followed by the per-destination metadata
	// segments. This mirrors the composeStubFolderID structure
	// (root → dest → segments) and the Publisher's path hierarchy.
	allSegs := make([]string, 0, len(segments)+1)
	allSegs = append(allSegs, string(dest))
	for _, s := range segments {
		if strings.TrimSpace(s) != "" {
			allSegs = append(allSegs, strings.TrimSpace(s))
		}
	}

	folderID, err := a.folderEnsurer.EnsureFolder(ctx, root, allSegs...)
	if err != nil {
		return "", fmt.Errorf(
			"%w: destination=%q, root=%q, segments=[%s]: %w",
			ErrFolderEnsureFailed, dest, root, strings.Join(allSegs, "/"), err,
		)
	}
	if folderID == "" {
		return "", fmt.Errorf(
			"%w: Drive.EnsureFolder returned empty folder-id for destination=%q",
			ErrFolderIDNonAlphanumeric, dest,
		)
	}
	if a.log != nil {
		a.log.Info(
			"resolver: resolved location to real Drive folder",
			"destination", string(dest),
			"root", root,
			"folder_id", folderID,
			"segments", strings.Join(allSegs, "/"),
		)
	}
	return folderID, nil
}

// ── compile-time pin (godlike/06 SSOT) ───────────────────────────────────
//
// Compile-time assertion: *Adapter satisfies the canonical Pattern-0
// port declared in internal/application/assets/sourcing/ports.go.
// Future drift in Resolve signature surfaces as a build failure, NOT
// a runtime nil-panic.
var _ sourcing.LocationResolverPort = (*Adapter)(nil)
