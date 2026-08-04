// Package delivery — canonical Drive publish contract types
// (PR-MEDIATRANSFORMER-RENAME step 2, July 2026).
//
// PR-MEDIATRANSFORMER-RENAME step 2 (July 2026): the canonical flow
// is expressed as
//
//	RenditionSet + DestinationSpec → PublicationReceipt
//
// where:
//   - `DestinationSpec`     — what the caller wants to publish and
//     where on Drive it should land (a typed
//     seam: DestinationKey + logical metadata).
//   - `PublicationReceipt`  — what the publisher actually did on
//     Drive (FileID, web view link, download
//     URL, MD5 checksum, folder ID, path
//     segments, action).
//
// godlike/06 SSOT: `DestinationSpec` and `PublicationReceipt` are
// type aliases for the canonical wire types `PublishRequest` and
// `PublishResult`. The rename is a hygiene improvement: the
// orchestrator + legacy Processor speak in terms of the new contract
// (RenditionSet + DestinationSpec → PublicationReceipt) while
// `PublishRequest` / `PublishResult` remain the wire types consumed
// by the `delivery.Publisher` implementation. The two names refer to
// the same underlying type, so a struct literal `DestinationSpec{...}`
// and a function signature `Publish(ctx, DestinationSpec)` work
// interchangeably with `PublishRequest` / `PublishResult`.
//
// The canonical contract is the publisher's responsibility:
// folder resolution (via `delivery.DestinationRegistry` +
// `delivery.Publisher.ResolveFolder`) and the Drive upload (via
// `delivery.Publisher.Publish`) are owned by the publisher. The
// MediaTransformer is a strictly local transformer and does NOT
// touch `drive.Admin`, `drive.Uploader`, or any Drive SDK type
// (godlike/06 SSOT).
package delivery

import "errors"

// ErrDestinationParentMismatch is returned when a live Drive upload
// verification reports that the uploaded file has no parent equal to
// the already-resolved destination folder. Publishers and callers must
// fail the operation; they must not repair the location by moving the
// file after upload.
var ErrDestinationParentMismatch = errors.New("delivery: uploaded file parent does not match resolved destination")

// DestinationSpec is the typed seam for the canonical Drive publish
// flow: RenditionSet + DestinationSpec → PublicationReceipt.
//
// It is a type alias for `PublishRequest` (the canonical wire shape
// consumed by `delivery.Publisher.Publish`). Callers populate the
// `LocalPath` from the `RenditionSet.LocalPath` (the canonical
// mezzanine file) and the `Destination` key from the logical
// destination (e.g. `delivery.DestinationArtlist`,
// `delivery.DestinationYouTubeClip`).
//
// The `FolderID` / `DestinationFolderID` / `RootFolderOverride`
// fields on `PublishRequest` are threaded through transparently
// (they are part of the canonical `DestinationSpec` surface). The
// publisher's `DestinationRegistry` resolves the folder hierarchy
// per the typed `DestinationKey`; explicit folder overrides are
// honoured for sidecar flows (e.g. `delivery.DestinationClipMetadata`).
type DestinationSpec = PublishRequest

// PublicationReceipt is the canonical receipt for a Drive publish.
// It is a type alias for `PublishResult` (the canonical wire shape
// returned by `delivery.Publisher.Publish`). Callers read the
// Drive-side metadata (FileID, web view link, download URL, MD5
// checksum, folder ID, path segments, action) from this struct.
type PublicationReceipt = PublishResult
