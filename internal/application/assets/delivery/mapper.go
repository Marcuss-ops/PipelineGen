// Package delivery — semantic-location mapper (SEMANTIC-LOCATION-API-2026-07-06
// Wave 1).
//
// BuildPublishRequest is the SOLE owner of the semantic-location → PublishRequest
// translation (godlike/06 SSOT one-canonical-owner-per-fact). The mapper
// converts an AssetPublishInput (endpoint-facing: AssetLocationInput + identity)
// into a typed PublishRequest that the Publisher consumes. No endpoint or job
// creates a PublishRequest verified-directly (they always route through this
// mapper); the typed-error contract enforces godlike/07 NO-FAKE-AVAILABILITY
// via dedicated sentinels per missing-required field.
package delivery

import (
	"errors"
	"fmt"

	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/domain/delivery"
)

// ── typed-error contract (godlike/07) ───────────────────────────────────

var (
	// ErrAssetPublishDestinationUnknown surfaces when AssetPublishInput
	// .Destination is not registered in the DestinationRegistry. The
	// mapper does not infer destination from Location fields — the caller
	// MUST pass an explicit DestinationKey so the typed-error stays
	// bite-size instead of producing "looks-like-stock-but-could-be-image"
	// ambiguities.
	ErrAssetPublishDestinationUnknown = errors.New(
		"delivery: BuildPublishRequest: destination key is empty or unregistered",
	)

	// ErrAssetPublishLocalPathMissing surfaces when AssetPublishInput
	// .LocalPath is empty. The Publisher needs a real file path on disk;
	// empty LocalPath is forbidden at the mapper boundary so the
	// Publisher never has to defensive-check for missing source.
	ErrAssetPublishLocalPathMissing = errors.New(
		"delivery: BuildPublishRequest: LocalPath is required",
	)

	// ErrAssetPublishFilenameMissing surfaces when AssetPublishInput
	// .Filename is empty. Filename is the canonical display name on
	// Drive; an empty filename would land on Drive with empty metadata.
	ErrAssetPublishFilenameMissing = errors.New(
		"delivery: BuildPublishRequest: Filename is required",
	)

	// ErrAssetPublishLocationIncompleteForDestination surfaces when the
	// per-destination mandatory Location fields are not satisfied. The
	// error message includes both the destination key and the missing
	// field so a failing caller knows exactly what to populate.
	//
	// Format: "delivery: BuildPublishRequest: location incomplete for
	//   destination %q: missing %q (and any other unlisted fields)"
	//
	// godlike/07 fail-fast: the mapper refuses to emit a half-populated
	// PublishRequest; the typed-error propagates back to the caller
	// which can probe via errors.Is(err, ErrAssetPublish...Incomplete...).
	ErrAssetPublishLocationIncompleteForDestination = errors.New(
		"delivery: BuildPublishRequest: location incomplete for destination",
	)

	// ErrAssetPublishNameCannotReplaceSubject surfaces when both
	// Location.Subject and Location.Name are empty AND the destination
	// requires a Subject (image / youtube / stock / artlist / document).
	// Distinct from IncompleteForDestination per godlike/07 typed-error
	// clarity: "name-soft-fallback exhausted" is a different failure mode.
	ErrAssetPublishNameCannotReplaceSubject = errors.New(
		"delivery: BuildPublishRequest: name cannot replace subject (both empty)",
	)
)

// ── input shape ──────────────────────────────────────────────────────────

// AssetPublishInput is the broader command-shape input that wraps
// AssetLocationInput plus artifact identity. Endpoints / jobs / CLI tools
// pass this to BuildPublishRequest instead of building a PublishRequest
// directly. godlike/06 SSOT: BuildPublishRequest is the SOLE owner of the
// typed-contract translation, so callers MUST NOT instantiate
// PublishRequest directly (the PostMapper surface is locked behind this
// entry-point — forwarding a hand-built PublishRequest to a Publisher
// that is also passed through BuildPublishRequest would be a 2-route
// SSOT drift).
type AssetPublishInput struct {
	// Location is the canonical semantic location (godlike/06 SSOT owner:
	// internal/domain/delivery/location.go).
	Location domaindelivery.AssetLocationInput

	// Destination is the canonical Drive destination key. The mapper
	// does NOT infer DestinationKey from Location fields — the caller
	// MUST pass an explicit DestinationKey so the typed-error contract
	// is bite-size. Empty Destination → ErrAssetPublishDestinationUnknown.
	Destination DestinationKey

	// LocalPath is the absolute path on local filesystem. Required.
	LocalPath string

	// Filename is the desired display name on Drive. Required.
	Filename string

	// Description is an optional human-readable description.
	Description string

	// AssetID is the canonical media_assets.id (or asset equivalent).
	// Document destination REQUIRES AssetID; other destinations
	// treat it as the IdempotencyKey identification component.
	AssetID string

	// ContentHash is the hex-encoded SHA-256 digest of the artifact
	// content. Combined with AssetID + Destination + SourceVersion
	// derives IdempotencyKey.
	ContentHash string

	// SourceVersion is the logical source version at publish time.
	SourceVersion int64

	// Tags are the semantic keywords for the Qdrant payload (Wave 10
	// upstream). Held here so the mapper can validate non-emptiness per
	// destination (e.g. image-ai typically carries 2-8 tags; voiceover
	// typically 0-1 tags). Forward-pointer: consumed in Wave 10.
	Tags []string
}

// ── mapper function ──────────────────────────────────────────────────────

// BuildPublishRequest maps an AssetPublishInput into a canonical
// PublishRequest that the Publisher consumes.
//
// Validation order (godlike/07 NO-FAKE-AVAILABILITY — fail-fast-at-input
// > fail-slow-at-publish-time):
//
//  1. Destination → ErrAssetPublishDestinationUnknown
//  2. LocalPath empty → ErrAssetPublishLocalPathMissing
//  3. Filename empty → ErrAssetPublishFilenameMissing
//  4. Per-destination mandatory Location fields → ErrAssetPublish...Incomplete
//
// Per-destination mapping table (SEMANTIC-LOCATION-API-2026-07-06 Wave 1;
// Wave 4 stock migration will additionally inject Provider into StockPath,
// Wave 7 image family support Origin via a separate input field):
//
//   - DestinationImage:        req.Style = loc.Style;         req.Subject = loc.SubjectOrName()
//   - DestinationStock:        req.Group  = loc.Category;      req.Subject  = loc.SubjectOrName(); req.Provider = loc.Provider
//   - DestinationYouTubeClip:  req.Group  = loc.Category;      req.Subject  = loc.SubjectOrName()
//   - DestinationArtlist:      req.Group  = loc.Category;      req.Subject  = loc.SubjectOrName()
//   - DestinationVoiceover:    req.ProjectID = loc.Project;    req.Language = loc.Language
//   - DestinationBook:         req.ProjectID = loc.Project
//   - DestinationScript:       req.ProjectID = loc.Project;    req.Language = loc.Language
//   - DestinationSoundEffect:  req.Group     = loc.Category
//   - DestinationDocument:     req.AssetID   = loc.SubjectOrName()
//
// Idempotency: when AssetID OR ContentHash is non-empty, the mapper
// derives IdempotencyKey via DeriveIdempotencyKey(Destination, AssetID,
// ContentHash, SourceVersion). Both empty → IdempotencyKey empty
// (backward-compat fallback to filename-based lookup).
//
// godlike/06 SSOT: this function is the SOLE owner of the translation.
// PathBuilder functions (StockPath, ImagePath, etc.) are the SOLE owner
// of the segment-shape reduction. Two layers; no overlap.
func BuildPublishRequest(input AssetPublishInput) (PublishRequest, error) {
	// ── 1. Destination must be set and registered ────────────────────
	if input.Destination == "" {
		return PublishRequest{}, fmt.Errorf("%w: %q", ErrAssetPublishDestinationUnknown, input.Destination)
	}

	// ── 2. LocalPath required ─────────────────────────────────────────
	if input.LocalPath == "" {
		return PublishRequest{}, fmt.Errorf("%w", ErrAssetPublishLocalPathMissing)
	}

	// ── 3. Filename required ───────────────────────────────────────────
	if input.Filename == "" {
		return PublishRequest{}, fmt.Errorf("%w", ErrAssetPublishFilenameMissing)
	}

	// ── 4. Initialise PublishRequest with universal fields ────────────
	req := PublishRequest{
		Destination:   input.Destination,
		LocalPath:     input.LocalPath,
		Filename:      input.Filename,
		Description:   input.Description,
		AssetID:       input.AssetID,
		ContentHash:   input.ContentHash,
		SourceVersion: input.SourceVersion,
	}

	loc := input.Location

	// ── 5. Per-destination Location mapping ──────────────────────────
	switch input.Destination {
	case DestinationImage:
		if loc.Style == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "style",
			)
		}
		subject := loc.SubjectOrName()
		if subject == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: %w", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, ErrAssetPublishNameCannotReplaceSubject,
			)
		}
		req.Style = loc.Style
		req.Subject = subject

	case DestinationStock:
		if loc.Category == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "category",
			)
		}
		if loc.Provider == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "provider",
			)
		}
		subject := loc.SubjectOrName()
		if subject == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: %w", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, ErrAssetPublishNameCannotReplaceSubject,
			)
		}
		req.Category = loc.Category  // primary field for StockPath (DoD item 4)
		req.Group = loc.Category     // backward-compat fallback for legacy callers
		req.Subject = subject
		req.Provider = loc.Provider   // required; validated above

	case DestinationYouTubeClip:
		if loc.Category == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "category",
			)
		}
		subject := loc.SubjectOrName()
		if subject == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: %w", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, ErrAssetPublishNameCannotReplaceSubject,
			)
		}
		req.Group = loc.Category
		req.Subject = subject

	case DestinationArtlist:
		if loc.Category == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "category",
			)
		}
		subject := loc.SubjectOrName()
		if subject == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: %w", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, ErrAssetPublishNameCannotReplaceSubject,
			)
		}
		req.Group = loc.Category
		req.Subject = subject

	case DestinationVoiceover:
		if loc.Project == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "project_id",
			)
		}
		if loc.Language == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "language",
			)
		}
		req.ProjectID = loc.Project
		req.Language = loc.Language

	case DestinationBook:
		if loc.Project == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "project_id",
			)
		}
		req.ProjectID = loc.Project

	case DestinationScript:
		if loc.Project == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "project_id",
			)
		}
		if loc.Language == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "language",
			)
		}
		req.ProjectID = loc.Project
		req.Language = loc.Language

	case DestinationSoundEffect:
		if loc.Category == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: missing %q", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, "category",
			)
		}
		req.Group = loc.Category

	case DestinationDocument:
		subject := loc.SubjectOrName()
		if subject == "" {
			return PublishRequest{}, fmt.Errorf(
				"%w %q: %w", ErrAssetPublishLocationIncompleteForDestination,
				input.Destination, ErrAssetPublishNameCannotReplaceSubject,
			)
		}
		req.AssetID = subject

	default:
		// Unknown DestinationKey — typed-error so callers can probe.
		return PublishRequest{}, fmt.Errorf(
			"%w: %q is not in the canonical DestinationKey registry",
			ErrAssetPublishDestinationUnknown, input.Destination,
		)
	}

	// ── 6. IdempotencyKey derivation (when either AssetID or ContentHash is set) ──
	if input.AssetID != "" || input.ContentHash != "" {
		req.IdempotencyKey = DeriveIdempotencyKey(
			input.Destination,
			input.AssetID,
			input.ContentHash,
			input.SourceVersion,
		)
	}

	return req, nil
}
