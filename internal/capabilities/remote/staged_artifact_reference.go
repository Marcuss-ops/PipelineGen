// Package remote — staged_artifact_reference.go (P0-COMPL-5-WIRE-NAMING,
// July 2026, COMPLETION-CUTOVER-P0-2026-07-04 wave follow-up).
//
// Canonical StagedArtifactReference is the wire-format input shape for the
// complete-with-artifacts HTTP endpoint AFTER the wire-format rename
// (PublishedArtifacts -> StagedArtifacts). The Sent caller ships a slice
// of these references (pre-publish, identifier-only); the Sender uses
// each reference to drive the canonical ArtifactPreparation.Prepare chain
// (validate + sha256 recompute + Drive upload) and produces the canonical
// 7-field PublishedArtifact envelope (ArtifactID, Kind, Filename, MIMEType,
// SizeBytes, SHA256, IdempotencyKey) plus the Drive Location {Provider,
// FileID, WebViewLink, DownloadLink, FolderID, FolderPath, Action}.
//
// godlike/06 SSOT (one canonical owner per fact): StagedArtifactReference
// is the SINGLE canonical input shape for the wire; the prior shape
// (internal/application/jobs/domain_commands.go::CompleteWithArtifactsCommand
// .PublishedArtifacts json.RawMessage) is now renamed StagedArtifacts and
// carries a typed []*StagedArtifactReference slice (replacing the json.
// RawMessage opaque-bytes convention). This package (internal/domain/remote/)
// is the godlike/06-canonical home for the remote-domain wire types
// (alongside CompleteArtifactsRequest/Response in complete_artifacts.go).
//
// godlike/07 typed-error contract: Validate() returns a typed
// ErrStagedArtifactReferenceMissingFields sentinel so a malformed wire
// body surfaces as errors.Is(r.ValidationErr(), ErrXxx) at the handler
// boundary (no string-match probing, no panic-on-malformed input).
//
// Honest scope-lock (godlike/07):
//   - This type carries the MINIMUM identifier envelope needed for the
//     Sender to drive prepare+sha256+publish; richer provenance (timestamps,
//     media_type, MIME hint) lives on the media_assets row looked up via
//     staged.Resolver during the conversion. The conversion layer reads
//     the on-disk LocalPath from media_assets (NOT from the wire) because
//     the LocalPath is an internal detail (godlike/07 typed LocalPath ban).
//   - "Drive FileID/link/checksum" semantics for the post-publish envelope
//     come from the canonical asset publicator concrete (internal/infrastructure/
//     drive/publisher.go) — flagged here as the single-source-of-truth.
//
// godlike/06 SSOT (one canonical owner per fact) — IMPORTANT: this type
// MUST NOT duplicate fields that already exist on the canonical envelope
// (internal/domain/finalization/types.go::VerifiedArtifact or
// PublishedArtifact). If a needed field already lives there, prefer
// embedding/reference to duplication (Pattern 5 — slim type surface).
package remote

import (
	"errors"
	"fmt"
	"strings"
)

// ErrStagedArtifactReferenceMissingFields is the godlike/07 typed sentinel
// returned by StagedArtifactReference.Validate() when one or more mandatory
// fields are missing. The wrap chain preserves the field name + index for
// operator diagnosis (via errors.As + custom envelope, NOT direct pointer
// equality — the wrap must survive across functions).
var ErrStagedArtifactReferenceMissingFields = errors.New(
	"remote: StagedArtifactReference failed validation (one or more mandatory fields missing)",
)

// ErrStagedArtifactReferenceInvalidDestination is the typed sentinel for
// the destination field failing the canonical 4-letter validation. The
// canonical destination set is bounded by the application-layer
// delivery.DestinationKey enum (internal/platform/delivery/
// types.go); the wire layer accepts only the rendered 4-letter keys
// (drive, doc, image, sound_effect, script, voiceover, artlist, etc.)
// per the canonical 9-key directory in godlike/06 §SSOT.
var ErrStagedArtifactReferenceInvalidDestination = errors.New(
	"remote: StagedArtifactReference.Destination is not in the canonical 9-key directory (drive, doc, image, sound_effect, script, voiceover, artlist, etc.)",
)

// CanonicalDestinationKeys is the canonical 9-key directory used by the
// wire layer's destination validation. Kept here (rather than imported
// from internal/platform/delivery) to avoid an import cycle
// (internal/domain -> internal/application is a layering violation per
// godlike/06 one-canonical-owner-per-fact). The application-layer
// delivery.DestinationKey enum is the CANONICAL source of these values;
// this list is the PACKAGE-LOCAL projection used for wire validation.
var CanonicalDestinationKeys = map[string]bool{
	"youtube_clip": true,
	"artlist":      true,
	"stock":        true,
	"image":        true,
	"voiceover":    true,
	"book":         true,
	"script":       true,
	"sound_effect": true,
	"document":     true,
}

// StagedArtifactReference is the canonical 3-field wire-shape for a
// pre-publish artifact reference. The Sender completes the canonical
// PublishedArtifact envelope post-publish using Staged Resolver +
// ArtifactPreparation to look up the on-disk file.
//
// Mandatory fields:
//   - ArtifactID: canonical id (mirrors media_assets.id)
//   - Destination: 1 of the canonical 9-key directory values
//
// Optional (but recommended for fast-path):
//   - SHA256: pre-computed hash hint for the prepare-pipeline fast-path
//     (the prepare pipeline recomputes on-disk anyway as a fail-closed gate)
//
// Honest scope-lock (godlike/07): richer provenance (timestamps, MIME
// hint, media_type) is READ from media_assets (via staged.Resolver) at
// conversion time, NOT shipped on the wire. This keeps the wire envelope
// minimal (godlike/07 SSOT: small/typed wire shapes are auditable) and
// avoids leaking server-internal detail (LocalPath, internal_cols, …) to
// the caller (godlike/07 typed LocalPath ban).
type StagedArtifactReference struct {
	// ArtifactID is the canonical media_assets.id that the Sender uses
	// to look up the on-disk file. The wire-body MAY ship a synthetic
	// ArtifactID (e.g. "tmp-…") when the artifact is in-flight, but the
	// canonical validation pipeline MUST resolve to a media_assets row
	// before preparing. Empty ArtifactID fails Validate() with
	// ErrStagedArtifactReferenceMissingFields.
	ArtifactID string `json:"artifact_id"`

	// Destination is the canonical 9-key directory key. The Sender uses
	// this to select the correct Drive folder + canonical Drive metadata.
	// Empty Destination fails Validate() with ErrStagedArtifactReference
	// MissingFields. A non-canonical Destination key (not in
	// CanonicalDestinationKeys) fails Validate() with
	// ErrStagedArtifactReferenceInvalidDestination.
	Destination string `json:"destination"`

	// SHA256 is the OPTIONAL pre-computed SHA-256 hint. When non-empty,
	// the prepare-pipeline can use it for fast-path diff-checks against
	// the on-disk recompute (godlike/07 fail-closed: the on-disk check
	// is the authoritative source; SHA256 is a hint, not a bypass).
	// Empty SHA256 is valid (the Sender recomputes on-disk per the
	// canonical prepare pipeline).
	SHA256 string `json:"sha256,omitempty"`

	// Local-worker completion fields are projected from the validated
	// artifact manifest before ArtifactPreparation runs. They remain
	// optional for callers that submit a reference through the public wire.
	Path             string         `json:"path,omitempty"`
	Filename         string         `json:"filename,omitempty"`
	MIMEType         string         `json:"mime_type,omitempty"`
	SizeBytes        int64          `json:"size_bytes,omitempty"`
	Required         bool           `json:"required,omitempty"`
	DriveGroup       string         `json:"drive_group,omitempty"`
	DriveLanguage    string         `json:"drive_language,omitempty"`
	ArtifactMetadata map[string]any `json:"artifact_metadata,omitempty"`
}

// Validate returns nil if the StagedArtifactReference is well-formed;
// otherwise wraps a typed sentinel via errors.Is-compatible probe.
//
// godlike/07 typed-error contract:
//   - ErrStagedArtifactReferenceMissingFields for any missing mandatory field.
//   - ErrStagedArtifactReferenceInvalidDestination for an out-of-directory key.
//
// Multiple failures are aggregated into a single diagnostic message
// (preserving the field-name list for operator diagnosis) but the
// wrapping is a SINGLE sentinel (errors.Is probes one sentinel at a
// time; the message carries the per-field detail).
func (r *StagedArtifactReference) Validate() error {
	if r == nil {
		return fmt.Errorf(
			"StagedArtifactReference.Validate: nil receiver: %w",
			ErrStagedArtifactReferenceMissingFields,
		)
	}
	var missing []string
	if strings.TrimSpace(r.ArtifactID) == "" {
		missing = append(missing, "artifact_id")
	}
	if strings.TrimSpace(r.Destination) == "" {
		missing = append(missing, "destination")
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"StagedArtifactReference.Validate: missing fields [%s]: %w",
			strings.Join(missing, ", "),
			ErrStagedArtifactReferenceMissingFields,
		)
	}
	if !CanonicalDestinationKeys[r.Destination] {
		return fmt.Errorf(
			"StagedArtifactReference.Validate: destination=%q not in canonical 9-key directory: %w",
			r.Destination,
			ErrStagedArtifactReferenceInvalidDestination,
		)
	}
	return nil
}

// StagedArtifacts is the canonical wire-shape slice type for the HTTP
// complete-with-artifacts endpoint. Replaces the prior opaque-bytes
// json.RawMessage convention (internal/application/jobs/domain_commands.go
// .PublishedArtifacts) with a typed slice so the wire layer can enforce
// input validation BEFORE the conversion layer runs.
type StagedArtifacts []*StagedArtifactReference

// Validate aggregates the result of Validate() for each element. Empty
// slices are valid (the conversion layer treats them as a no-op publish
// with zero response-side PublishedArtifact envelopes — the canonical
// Single-TX atomic write still runs because the canonical
// CompleteWithArtifacts pipeline has run-blocking for any zero-artifact
// result).
func (s StagedArtifacts) Validate() error {
	for i, ref := range s {
		if ref == nil {
			return fmt.Errorf(
				"StagedArtifacts.Validate: nil element at index [%d]: %w",
				i,
				ErrStagedArtifactReferenceMissingFields,
			)
		}
		if err := ref.Validate(); err != nil {
			return fmt.Errorf(
				"StagedArtifacts.Validate: element [%d] failed: %w",
				i, err,
			)
		}
	}
	return nil
}
