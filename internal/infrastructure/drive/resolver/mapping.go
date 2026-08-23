// File mapping.go — per-destination mapping helpers for the resolver
// adapter. Extracted from adapter.go per AGENTS.md Pattern 5 v2
// (1 concetto per file; code-motion pura, zero logica cambiata).
//
// Concept scope: every function here participates in the canonical
// (Destination, Location) → Drive folder-id translation pipeline:
//
//   - rootForDestination      → Drive root priority chain (DoD #5)
//   - rootConfigKey           → diagnostic-key hint for ErrDestinationNoRootFolder
//   - incompatibleFieldProbe  → per-destination hard-reject fields
//   - softIgnoredFieldProbe   → per-destination off-channel metadata fields
//   - segmentsForDestination  → canonical segment-shape table
//   - mandatoryFieldGate      → per-destination FIRST-missing field check
//   - stubModePrefix → retained for test regression guards only (composeStubFolderID retired)
//
// godlike/06 SSOT one-canonical-owner-per-fact: these tables live ONLY
// here. The canonical BuildPublishRequest surface in
// internal/application/assets/delivery/mapper.go mirrors these tables in
// spirit (Location fields → PublishRequest fields vs Location fields →
// FolderIDSegmentShape) but is a distinct concern.
//
// godlike/07 NO-FAKE-AVAILABILITY: stub-mode ids are NOT silently
// consumed downstream; composition-root + adapter tests reject them on
// forward-detection. Future CUTOVER PR removes the prefix sentinel.
package resolver

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
)

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

// stubModePrefix is retained for test regression guards that assert
// real Drive folder-ids do NOT carry the legacy stub prefix.
// composeStubFolderID was retired when the real Drive.EnsureFolderPath
// integration landed (CUTOVER C9). The prefix constant remains so
// test assertions like `strings.HasPrefix(folderID, stubModePrefix)`
// serve as regression guards detecting accidental stub-mode revival.
const stubModePrefix = "stub-shift:"
