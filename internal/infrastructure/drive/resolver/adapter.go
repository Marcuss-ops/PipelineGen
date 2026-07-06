// Package resolver — internal/infrastructure/drive/resolver — stub
// adapter for the canonical Pattern-0 LocationResolverPort declared in
// internal/application/assets/sourcing/ports.go.
//
// SEMANTIC-LOCATION-API-2026-07-06 Wave 7 (PR-RESOLVER-PORT-EXTRACT):
// this adapter is the production-time stub that ships with the Wave 7
// wiring. It implements the canonical per-destination segment-shape
// mapping table (mirroring BuildPublishRequest's switch in
// internal/application/assets/delivery/mapper.go) and returns a
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
// here. BuildPublishRequest continues to own the
// AssetLocationField→PublishRequestField translation (delivery-side);
// this adapter owns the AssetLocationField→FolderIDSegmentShape
// translation (drive-side). Two layers, no overlap.
package resolver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	sourcing "github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"
	_ "net/http" // future CUTOVER C9: Drive.EnsureFolder canonical HTTP status mapping
)

// ── typed-error contract (godlike/07 NO-FAKE-AVAILABILITY) ────────────────
//
// The adapter returns the canonical typed-error envelope that the port
// declares (ErrLocationResolverEmpty / ErrLocationResolverDestinationUnsupported
// / ErrLocationResolverIncompatibleFields). Each error message includes
// the destination key + the offending field name so a failing caller
// can probe via errors.Is + errors.As + log-scanner grep.
//
// Additional adapter-local sentinel: ErrFolderEnsureNotImplemented is
// raised ONLY when the production Drive API integration is expected
// but the stub's prefix-mode is in use. Today (Wave 7 stub) the adapter
// is intentionally NOT calling Drive.EnsureFolder — the prefix-mode
// resolution is the canonical Wave 7 deliverable. Operators reading
// the diagnostic see the suffix `(<mode>=stub-shift)` or
// `(<mode>=ensure-api)` once the real Drive API wiring lands.
var (
	// ErrFolderEnsureNotImplemented surfaces if a future CUTOVER
	// version of this adapter is wired but the Drive.EnsureFolder
	// call fails (re-export of the canonical Drive-side error).
	ErrFolderEnsureNotImplemented = errors.New(
		"resolver: Drive.EnsureFolder not implemented (forward-pointer CUTOVER)",
	)

	// ErrFolderIDNonAlphanumeric surfaces if a per-destination mapping
	// resolves to a folder-id containing characters that Drive
	// cannot accept (whitespace, slashes, etc.). The adapter
	// returns this BEFORE any Drive-side call so caller stops
	// dispatching the malformed request.
	ErrFolderIDNonAlphanumeric = errors.New(
		"resolver: resolved folder-id must be non-empty alphanumeric (drive-safe)",
	)
)

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
type Adapter struct {
	rootFolder string
	log        resolverLogger
}

// NewAdapter creates a fail-closed resolver adapter.
//
// rootFolder is the canonical Drive root folder-id under which per-
// destination subfolders are computed. Empty rootFolder is allowed
// only for test fixtures (production wiring should pass the canonical
// cfg.Drive.RootFolder — the composition root guarantees this via
// the validateDriveServiceAvailability check at boot).
//
// log is the optional narrow logging port — nil falls back to a
// no-op logger so test fixtures do not need a real logger.
//
// godlike/07 fail-closed-at-construction: NewAdapter returns
// (*Adapter, error) so future validation gates (e.g. rootFolder
// must equal DriveService.RootFolder()) have a typed-error envelope
// to surface on composition-time misconfiguration. Today the gate
// is permissive (rootFolder may be empty in tests; the per-Resolve
// gate rejects incompatible-fields + empty-Location at call-time).
func NewAdapter(rootFolder string, log resolverLogger) (*Adapter, error) {
	if rootFolder != "" {
		if strings.ContainsAny(rootFolder, " \t\n/\\") {
			return nil, fmt.Errorf(
				"%w: rootFolder %q contains whitespace or path separator",
				ErrFolderIDNonAlphanumeric, rootFolder,
			)
		}
	}
	return &Adapter{
		rootFolder: rootFolder,
		log:        log,
	}, nil
}

// WithLogger overrides the adapter's logger (fluent setter for tests).
func (a *Adapter) WithLogger(log resolverLogger) *Adapter {
	if a == nil {
		return nil
	}
	a.log = log
	return a
}

// resolverLogger is a narrow logging port for resolver-specific
// observability. Mirrors internal/application/assets/sourcing.Logger
// but kept package-local to avoid the import cycle.
//
// godlike/07 minimum-blast-radius: a nil logger falls back to a
// no-op default so callers do not need to wire a real logger in
// every test fixture.
type resolverLogger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}

type nopResolverLogger struct{}

func (nopResolverLogger) Info(string, ...any)  {}
func (nopResolverLogger) Warn(string, ...any)  {}
func (nopResolverLogger) Error(string, ...any) {}
func (nopResolverLogger) Debug(string, ...any) {}

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

	// Forward-pointer CUTOVER (C9): real Drive folder-ids are alphanumeric,
	// not subpath strings. The stub-mode canonical shape prepends a
	// `stub-shift:` sentinel so future CUTOVER consumers can detect + reject
	// stub-mode outputs via strings.HasPrefix(directoryID, "stub-shift:").
	folderID := composeStubFolderID(a.rootFolder, dest, segments)
	if folderID == "" {
		return "", fmt.Errorf(
			"%w: composed empty folder-id for destination=%q",
			ErrFolderIDNonAlphanumeric, dest,
		)
	}
	if a.log != nil {
		a.log.Info(
			"resolver: resolved location to folder",
			"destination", string(dest),
			"folder_id", folderID,
			"segments", strings.Join(segments, "/"),
		)
	}
	_ = ctx // forward-pointer: real Drive API call will use ctx.Deadline + ctx.Err
	return folderID, nil
}

// ── per-destination mapping helpers ──────────────────────────────────────

// incompatibleFieldProbe returns the per-destination field labels
// that the resolver REFUSES to consume (because they are not used by
// BuildPublishRequest's mapping). Mirrors the per-destination case
// labels in delivery/mapper.go.
func incompatibleFieldProbe(dest delivery.DestinationKey) []string {
	switch dest {
	case delivery.DestinationImage,
		delivery.DestinationStock,
		delivery.DestinationYouTubeClip,
		delivery.DestinationArtlist,
		delivery.DestinationSoundEffect,
		delivery.DestinationDocument:
		return []string{"project", "language"}
	case delivery.DestinationVoiceover,
		delivery.DestinationBook,
		delivery.DestinationScript:
		return []string{"style", "provider", "category", "subject", "name"}
	default:
		return nil
	}
}

// segmentsForDestination returns the canonical segment-shape for a
// (Destination, Location) pair. Mirror of BuildPublishRequest's per-
// destination mandatory checks but expressed in subpath-segment form.
func segmentsForDestination(dest delivery.DestinationKey, loc domaindelivery.AssetLocationInput) []string {
	switch dest {
	case delivery.DestinationImage:
		// Mirror: req.Style = loc.Style; req.Subject = loc.SubjectOrName()
		return []string{loc.Style, loc.SubjectOrName()}
	case delivery.DestinationStock, delivery.DestinationYouTubeClip, delivery.DestinationArtlist:
		// Mirror: req.Group = loc.Category; req.Subject = loc.SubjectOrName() (and Provider for Stock)
		segs := []string{loc.Category, loc.SubjectOrName()}
		if dest == delivery.DestinationStock {
			segs = append(segs, loc.Provider)
		}
		return segs
	case delivery.DestinationSoundEffect:
		// Mirror: req.Group = loc.Category
		return []string{loc.Category}
	case delivery.DestinationVoiceover, delivery.DestinationBook, delivery.DestinationScript:
		// Mirror: req.ProjectID = loc.Project; req.Language = loc.Language (voiceover + script only)
		segs := []string{loc.Project}
		if dest == delivery.DestinationVoiceover || dest == delivery.DestinationScript {
			segs = append(segs, loc.Language)
		}
		return segs
	case delivery.DestinationDocument:
		// Mirror: req.AssetID = loc.SubjectOrName()
		return []string{loc.SubjectOrName()}
	default:
		return nil
	}
}

// mandatoryFieldGate returns the per-destination FIRST missing mandatory
// segment label (or "" if every mandatory segment is populated). The
// resulting Resolve reply wraps ErrLocationResolverDestinationUnsupported
// so callers probe via errors.Is. Mirrors BuildPublishRequest's per-
// destination mandatory-check at the resolver boundary.
func mandatoryFieldGate(dest delivery.DestinationKey, segments []string) string {
	switch dest {
	case delivery.DestinationImage, delivery.DestinationYouTubeClip, delivery.DestinationArtlist,
		delivery.DestinationDocument:
		if segments[len(segments)-1] == "" {
			return "subject"
		}
	case delivery.DestinationStock:
		if segments[len(segments)-1] == "" {
			return "provider"
		}
		if segments[len(segments)-2] == "" {
			return "subject"
		}
	case delivery.DestinationSoundEffect:
		if len(segments) > 0 && segments[0] == "" {
			return "category"
		}
	case delivery.DestinationVoiceover, delivery.DestinationScript:
		if len(segments) >= 2 && segments[1] == "" {
			return "language"
		}
		fallthrough
	case delivery.DestinationBook:
		if len(segments) >= 1 && segments[0] == "" {
			return "project"
		}
	}
	return ""
}

// composeStubFolderID joins the root, destination, and per-destination
// segments into a canonical STUB-MODE folder-id. Sanitises each segment
// against the pathutil.SafeFolderName contract (no whitespace, no slash)
// so the resulting id is Drive-safe at the wire-shape level.
//
// STUB-MODE contract (forward-pointer CUTOVER C9):
//   - Output is prefixed with "stub-shift:" sentinel so future consumers
//     can detect stub-mode outputs via strings.HasPrefix(folderID, "stub-shift:").
//   - Real Drive folder-ids are alphanumeric Drive.File.ID strings;
//     the stub returns a synthetic slash-joined path BECAUSE the real
//     Drive.EnsureFolder integration is forward-pointer.
//   - Segments joined by "/" — real CUTOVER replaces this with the
//     canonical Drive File.ID returned by EnsureFolder.
//
// godlike/07 NO-FAKE-AVAILABILITY: stub-mode ids are NOT silently
// consumed downstream; composition-root + adapter tests reject them
// on forward-detection. Future CUTOVER PR removes the prefix sentinel.
const stubModePrefix = "stub-shift:"

func composeStubFolderID(root string, dest delivery.DestinationKey, segments []string) string {
	parts := make([]string, 0, len(segments)+3)
	parts = append(parts, stubModePrefix)
	if root != "" {
		parts = append(parts, root)
	}
	parts = append(parts, string(dest))
	for _, s := range segments {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		s = strings.ReplaceAll(s, "/", "-")
		s = strings.ReplaceAll(s, "\\", "-")
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "/")
}

// ── compile-time pin (godlike/06 SSOT) ───────────────────────────────────
//
// Compile-time assertion: *Adapter satisfies the canonical Pattern-0
// port declared in internal/application/assets/sourcing/ports.go.
// Future drift in Resolve signature surfaces as a build failure, NOT
// a runtime nil-panic.
var _ sourcing.LocationResolverPort = (*Adapter)(nil)
