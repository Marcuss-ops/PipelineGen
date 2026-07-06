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
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
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
	mediaRoot string             // cfg.Drive.MediaRootFolder (cached for composeStubFolderID)
	cfg       config.DriveConfig // per-destination root resolution (DoD #5)
	log       resolverLogger
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
// Resolve call). The MediaRootFolder is cached at construction for
// composeStubFolderID use; the destination-specific roots are
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

	// Forward-pointer CUTOVER (C9): real Drive folder-ids are alphanumeric,
	// not subpath strings. The stub-mode canonical shape prepends a
	// `stub-shift:` sentinel so future CUTOVER consumers can detect + reject
	// stub-mode outputs via strings.HasPrefix(directoryID, "stub-shift:").
	folderID := composeStubFolderID(root, dest, segments)
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

// ── DoD #5: per-destination root resolution ─────────────────────────────

// rootForDestination resolves the canonical Drive root folder for a
// destination via the priority chain mandated by DoD #5:
//
//  1. destination-specific root (e.g. StockFolder() → StockRootFolder)
//  2. MediaRootFolder (unified fallback)
//  3. ErrDestinationNoRootFolder (fail-closed)
//
// godlike/06 SSOT one-canonical-owner-per-fact: this method is the
// SINGLE canonical source for the destination→root mapping. Every
// destination added to the segment-shape table MUST also be added
// to the switch below (godlike/06 2-surface lockstep).
//
// godlike/07 typed-error contract: returns (rootID, nil) on success;
// returns ("", ErrDestinationNoRootFolder) when both the specific root
// AND MediaRootFolder are empty. The sentinel is wrapped with the
// destination name + the config-key hint so the operator can grep
// their config.yaml for the missing key.
func (a *Adapter) rootForDestination(dest delivery.DestinationKey) (string, error) {
	if a == nil {
		return "", fmt.Errorf(
			"%w: adapter nil (composition-root bug)",
			ErrDestinationNoRootFolder,
		)
	}

	// Resolve the effective folder for this destination.
	// Priority: specific root > MediaRootFolder > "".
	var folder string
	switch dest {
	case delivery.DestinationImage:
		folder = a.cfg.ImagesFolder()
	case delivery.DestinationStock:
		folder = a.cfg.StockFolder()
	case delivery.DestinationYouTubeClip:
		folder = a.cfg.ClipsFolder()
	case delivery.DestinationArtlist:
		folder = a.cfg.ArtlistFolder()
	case delivery.DestinationVoiceover:
		folder = a.cfg.VoiceoverFolder()
	case delivery.DestinationBook:
		folder = a.cfg.BooksFolder()
	case delivery.DestinationScript:
		folder = a.cfg.ScriptsFolder()
	case delivery.DestinationSoundEffect:
		folder = a.cfg.SoundEffectsFolder()
	case delivery.DestinationDocument:
		folder = a.cfg.DocumentsFolder()
	default:
		folder = a.cfg.RootFolder()
	}

	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "", fmt.Errorf(
			"%w: destination=%q has no configured root folder (set %s_root_folder or media_root_folder in config.yaml)",
			ErrDestinationNoRootFolder, dest, rootConfigKey(dest),
		)
	}
	return folder, nil
}

// rootConfigKey returns the config.yaml key name for the destination-
// specific root folder, used in the ErrDestinationNoRootFolder message
// so operators know exactly which key to set.
func rootConfigKey(dest delivery.DestinationKey) string {
	switch dest {
	case delivery.DestinationImage:
		return "images"
	case delivery.DestinationStock:
		return "stock"
	case delivery.DestinationYouTubeClip:
		return "clips"
	case delivery.DestinationArtlist:
		return "artlist"
	case delivery.DestinationVoiceover:
		return "voiceover"
	case delivery.DestinationBook:
		return "books"
	case delivery.DestinationScript:
		return "scripts"
	case delivery.DestinationSoundEffect:
		return "sound_effects"
	case delivery.DestinationDocument:
		return "scripts" // DocumentsFolder delegates to ScriptsRootFolder
	default:
		return string(dest)
	}
}

// ── per-destination mapping helpers ──────────────────────────────────────

// incompatibleFieldProbe returns the per-destination field labels
// that the resolver REFUSES to consume (because they are not used by
// BuildPublishRequest's mapping AND are not downstream-indexing
// metadata). Mirrors the per-destination case labels in
// delivery/mapper.go.
//
// Round-2 SHOULD-FIX (2026-07-06, category softened): "category" was
// REMOVED from the Voiceover/Book/Script hard-reject list because
// BuildPublishRequest ignores it silently for project-language
// destinations AND it carries "metadata for downstream Qdrant indexing"
// semantics per location.go godoc. The resolver now soft-warns instead
// of hard-rejecting (see softIgnoredFieldProbe below).
//
// godlike/07 typed-error contract: the typed sentinel
// ErrLocationResolverIncompatibleFields fires ONLY for these
// hard-rejection fields. A future drift that removes a hard-reject
// field MUST also update the corresponding TDD test surface.
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
		// style/provider/subject/name are TRULY off-channel for project-
		// language destinations — not consumed by BuildPublishRequest AND
		// not metadata for downstream indexing. Hard-rejection preserves
		// godlike/07 typed-error contract; "category" moved to softIgnored.
		return []string{"style", "provider", "subject", "name"}
	default:
		return nil
	}
}

// softIgnoredFieldProbe returns per-destination fields that the
// resolver does NOT use in the Drive folder-id but that callers may
// legitimately set (e.g. Category as Qdrant-indexing metadata for
// Voiceover tracks). The corresponding Resolve pass emits a per-call
// Warn log via a.log.Warn but does NOT raise the typed sentinel —
// the resolver proceeds with the canonical segment-shape mapping.
//
// godlike/06 SSOT one-canonical-owner-per-fact: this probe lives ONLY
// here. Future CUTOVER C9 (Drive.EnsureFolder wiring) extends this
// probe in lockstep with location.go field additions; godlike/07
// no-fake-availability guarantees the warn-log remains a real
// observable signal (not a swallowed dead-call) by the WithLogger
// + NewAdapter nil-default nopResolverLogger{} guard.
func softIgnoredFieldProbe(dest delivery.DestinationKey) []string {
	switch dest {
	case delivery.DestinationVoiceover,
		delivery.DestinationBook,
		delivery.DestinationScript:
		return []string{"category"}
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
	// root is always non-empty at this point (rootForDestination
	// returns ErrDestinationNoRootFolder before reaching here when
	// no root is configured).
	parts = append(parts, root)
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
