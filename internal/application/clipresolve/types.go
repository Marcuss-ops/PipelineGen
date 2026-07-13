// Package clipresolve — canonical SSOT for the backend-private binding
// surface that maps Gemma's opaque per-candidate pick
// (`{slot_ref, candidate_ref}`) back to the underlying infrastructure
// identifiers + provenance metadata that the model does NOT see.
//
// godlike/06 SSOT (one canonical owner per fact): this package is the
// SOLE owner of the AssetMapping wire shape, the AssetMetadata wire
// shape, and the typed envelopes (ParseCandidateRef + Resolve). A
// future package that needs to rebuild any of these facts imports the
// types from here, not a parallel fork.
//
// godlike/06 SSOT (sister to clipview): clipview/ projects
// slotRef+index → opaque ref (model-facing). clipresolve/ inverts
// that projection — opaque ref → {asset_id, drive_link, folder_path,
// normalized_group} (backend-facing). Both packages MUST stay in
// sync about the ref format `<slotRef>:candidate-<index>`; the
// shared parse + build is identified by constant CandidateRefPrefix
// below.
//
// godlike/07 NO-FAKE-AVAILABILITY (typed fail-closed boundary):
//
//   - ErrInvalidCandidateRef        : malformed ref shape
//   - ErrUnknownSlot                : slot_ref absent from the in-memory Index
//   - ErrUnknownIndex               : index out of bounds for that slot
//   - ErrAssetNotHydrated           : Hydrator-level failure (DB / IO)
//   - ErrAssetMappingIncomplete     : DriveLink missing on hydrated asset
//
// Each sentinel is wrapped with %w in the resolver so callers probe
// the typed envelope via errors.Is, NOT string-match. A future
// archcheck rule can promote these to a CI gate across all
// `clipresolve.Resolve` call sites (forward-pointer).
//
// Rationale for keeping the resolver private to the application
// layer (no LLM side, no worker side):
//
//	backend receives Gemma's pick  →  Resolve()  →  AssetMapping
//	                                              │
//	                                              ▼
//	                                  binding layer (private map
//	                                  ref→{A,D,FN} lives here)
//
// The model never sees AssetMapping. The worker UI never sees
// CandidateView. The two surfaces stay disjoint; the SSOT for each
// is its respective package.
package clipresolve

import (
	"context"
	"errors"
)

// CandidateRefPrefix is the canonical substring in the opaque ref
// that separates slotRef from index. clipview/builder.go uses the
// SAME prefix when constructing refs:
//
//	ref = slotRef + CandidateRefPrefix + strconv.Itoa(index)
//
// godlike/06 SSOT: this constant is the SINGLE source of truth for
// the delimiter. Both packages (clipview + clipresolve) reference
// it; a future drift surfaces as a test failure on the roundtrip
// fixture in clipview (forward-pointer).
const CandidateRefPrefix = ":candidate-"

// AssetMetadata is the hydration-side output: the per-asset
// provenance fields that the model does NOT see and the widget
// backend DOES need (drive_link for binding, folder_path for the
// asset tree view, normalized_group for folder routing).
//
// AssetMetadata intentionally OMITS AssetID — the resolver owns
// AssetID after intersecting SlotIndex.LookupCandidate with the
// hydration result. A future package that adds a field here must
// audit the redaction-leak catalogue (clipresolve does not expose
// this struct to a model, but the sibling clipview package's audit
// list (clipview.ForbiddenCandidateViewJSONFields) is the canonical
// reference.
type AssetMetadata struct {
	// DriveLink is the canonical Drive webViewLink for the asset.
	// Empty is HARD-FAIL (ErrAssetMappingIncomplete) — binding to
	// an asset without a Drive link is unrecoverable downstream.
	DriveLink string

	// FolderPath is the canonical folder_path on the asset (Drill-
	// down path string the operator sees in the asset tree view).
	FolderPath string

	// NormalizedGroup is the lowercase routing key (e.g. "boxe",
	// "hiphop") used as the canonical Qdrant filter target. NOT a
	// model-facing value — the resolver returns it for the binding
	// layer + audit trail, never to feed Gemma.
	NormalizedGroup string
}

// AssetMapping is the composed binding-side output: AssetID joined
// with the AssetMetadata hydration. This is the SOLE shape that the
// binding layer downstream of Gemma accepts.
//
// godlike/06 SSOT: caller code that builds an AssetMapping MUST go
// through (*Resolver).Resolve. Direct struct literals would bypass
// the fail-closed validation (AssetID + DriveLink non-empty), and
// would lose the typed-sentinel error envelope on missing fields.
// A future archcheck rule can promote "no AssetMapping literals
// outside this package" to a forward-prevention gate.
type AssetMapping struct {
	// AssetID is the canonical media_assets.id reference. The
	// resolver copies this verbatim from SlotIndex.LookupCandidate
	// — the Index is the authority on which asset an opaque ref
	// points to.
	AssetID string

	// DriveLink / FolderPath / NormalizedGroup mirror AssetMetadata.
	// Documented here for grep-ability; the canonical doc lives on
	// AssetMetadata to avoid drift.
	DriveLink       string
	FolderPath      string
	NormalizedGroup string
}

// SlotIndex is the canonical port for the in-memory per-plan
// slot→candidate map. The production implementation reads from
// `ports.SlotsSearchResult.ByRef` (live, per-call) — see
// internal/application/scripts/ports/clip_search_port.go for the
// upstream type. The test implementation uses a hard-coded map.
//
// godlike/06 SSOT: this port is the SINGLE seam between the
// resolver and the slots-search shape; a future agent that wants
// to swap a different in-memory Index (e.g. a persistent slot
// table) implements THIS port, not a parallel seam.
//
// godlike/07 NO-FAKE-AVAILABILITY: LookupCandidate MUST return a
// non-empty asset_id on success. An empty asset_id on the success
// path is unambiguously a programming error (the slot's candidate
// list was set without an asset_id), so the resolver surfaces it
// as wrapped ErrUnknownIndex, NOT a silent zero-value.
type SlotIndex interface {
	// LookupCandidate returns the asset_id bound to slotRef[idx].
	// Returns ErrUnknownSlot if slotRef is not present.
	// Returns ErrUnknownIndex if idx is out of bounds for the slot.
	LookupCandidate(slotRef string, index int) (assetID string, err error)
}

// AssetHydrator is the canonical port for fetching per-asset
// provenance (Drive link, folder path, normalized group) keyed by
// asset_id. The production implementation reads from media_assets
// + asset_locations (see internal/infrastructure/database/sqlite/
// assets/clip_metadata_writer_payload.go for the read surface);
// the test implementation uses hard-coded fixtures.
//
// godlike/06 SSOT: this port is the SINGLE seam between the
// resolver and the durable asset-metadata surface. A future agent
// that swaps in a different durable store (e.g. a separate
// catalog DB) implements THIS port, not a parallel seam.
//
// godlike/07 NO-FAKE-AVAILABILITY: Hydrate MUST return a non-nil
// AssetMetadata on success. A nil AssetMetadata on the success
// path is unambiguously an upstream bug, so the surface treats it
// as an error.
type AssetHydrator interface {
	// Hydrate returns AssetMetadata for the given asset_id. Returns
	// ErrAssetNotHydrated-driven errors when the asset_id cannot be
	// resolved (DB miss / IO failure / canonical row gone).
	Hydrate(ctx context.Context, assetID string) (AssetMetadata, error)
}

// ── Typed error envelope (godlike/07 fail-closed) ─────────────────

var (
	// ErrInvalidCandidateRef: the opaque ref did not match the
	// canonical `<slotRef>:candidate-<index>` shape, OR
	// slotRefMaybe was supplied and did not match the slot embedded
	// in the ref (mismatch is treated as input corruption, NOT a
	// silent override).
	ErrInvalidCandidateRef = errors.New(
		"clipresolve: invalid candidate_ref (must match `<slotRef>:candidate-<index>`, slotRef must match embedded slot)",
	)

	// ErrUnknownSlot: SlotIndex.LookupCandidate does not contain
	// the supplied slotRef. Caller must fix the upstream
	// slots_search call (or the in-memory Index lifecycle).
	ErrUnknownSlot = errors.New(
		"clipresolve: slot_ref absent from in-memory Index (callers must verify the Index is built before invoking Resolve)",
	)

	// ErrUnknownIndex: the slot is present but idx is out of
	// bounds for the slot's candidate list. Echoes the per-slot
	// PerSlotCandidateLimit semi-graphically: a Gemma pick with
	// an idx >= limit is treated as input corruption, NOT a
	// silent clamp.
	ErrUnknownIndex = errors.New(
		"clipresolve: candidate index out of bounds for slot (Gemma emitted an idx exceeding PerSlotCandidateLimit or the in-memory Index)",
	)

	// ErrAssetNotHydrated: AssetHydrator.Hydrate returned an
	// error (DB miss, IO, canonical row gone). Wrapped with %w
	// so the underlying cause is preserved for audit replay.
	ErrAssetNotHydrated = errors.New(
		"clipresolve: AssetHydrator returned no metadata for the asset_id (DB miss / IO)",
	)

	// ErrAssetMappingIncomplete: Hydrate succeeded but
	// DriveLink="" (or another mandatory field is empty). Godlike/07
	// NO-FAKE-AVAILABILITY at the binding boundary — binding to an
	// asset without a Drive link is unrecoverable downstream; we
	// fail-closed instead of inventing a placeholder URL.
	ErrAssetMappingIncomplete = errors.New(
		"clipresolve: AssetMapping incomplete — drive_link missing (no silent placeholder: binding requires the canonical Drive URL)",
	)
)
