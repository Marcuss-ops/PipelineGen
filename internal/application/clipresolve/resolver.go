package clipresolve

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Resolver is the canonical binding-side inverse of clipview/CandidateView.
// It takes Gemma's opaque pick (`{slot_ref, candidate_ref}`) and
// returns the AssetMapping the binding layer downstream of the model
// needs to actually use the clip (asset_id + drive_link + folder_path +
// normalized_group).
//
// godlike/06 SSOT: this is the SOLE canonical binding-side resolver.
// Future code that wants to project a ref back to an asset MUST
// import this Resolver (constructed at the composition root) and not
// build an ad-hoc local one. The two ports (SlotIndex + AssetHydrator)
// are the only seams — there is no parallel "fast path" or fallback
// implementation.
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure path surfaces a
// typed sentinel (ErrInvalidCandidateRef, ErrUnknownSlot,
// ErrUnknownIndex, ErrAssetNotHydrated, ErrAssetMappingIncomplete).
// The resolver NEVER invents a placeholder (no fake drive_link, no
// synthetic asset_id, no clamped index). Callers probe via errors.Is
// on the canonical envelope.
//
// godlike/06 SSOT (composition pattern): the resolver takes its two
// dependencies via constructor injection. Test fixtures inject
// in-memory stand-ins (map-based SlotIndex + map-based Hydrator);
// production wires the SlotsSearchResult-backed SlotIndex and the
// media_assets-backed Hydrator at the composition root. There is no
// global state, no init-time wiring, no lookup-by-name.
type Resolver struct {
	index    SlotIndex
	hydrator AssetHydrator
}

// NewResolver returns a Resolver that resolves Gemma's opaque pick
// via the supplied SlotIndex and AssetHydrator.
//
// godlike/06 SSOT: this is the ONLY canonical constructor. Direct
// struct literals of Resolver would bypass the godlike/07 typed
// envelope contract via the nil-port deref; tests are written to
// ensure NewResolver is the call site.
func NewResolver(idx SlotIndex, hyd AssetHydrator) *Resolver {
	return &Resolver{index: idx, hydrator: hyd}
}

// Resolve maps Gemma's opaque pick back to the backend binding
// surface. The canonical flow:
//
//  1. parse the `candidate_ref` (canonical shape
//     `<slotRef>:candidate-<index>`);
//  2. cross-check the optional `slot_ref` Gemma supplied alongside
//     (mismatch is fail-closed, NOT a silent override);
//  3. look up the asset_id via SlotIndex.LookupCandidate;
//  4. hydrate drive_link / folder_path / normalized_group via
//     AssetHydrator.Hydrate;
//  5. validate the composed mapping (AssetID + DriveLink non-empty,
//     fail-closed otherwise) and return.
//
// godlike/07 NO-FAKE-AVAILABILITY: every step that could silently
// degrade the binding surface (mismatch, miss, hydrate failure,
// incomplete hydration) surfaces a typed sentinel so the binding
// layer can branch on errors.Is without parsing error strings.
//
// godlike/06 SSOT (slotRef cross-check semantics): Gemma emits
// `{slot_ref, candidate_ref}` as separate keys (forward-pointer to
// the engine prompt). The candidate_ref ALREADY contains the slot
// embedded, so slotRefMaybe is REDUNDANT input — but it lets the
// resolver catch adversarial transcription bugs where the model
// emits a stale slotRef with a fresh candidate_ref. Mismatch is
// treated as input corruption.
func (r *Resolver) Resolve(ctx context.Context, slotRefMaybe, candidateRef string) (AssetMapping, error) {
	if r == nil || r.index == nil || r.hydrator == nil {
		// godlike/07 NO-FAKE-AVAILABILITY at the constructor
		// boundary: never silently fall through with a nil
		// dependency. Wrap the typed envelope so callers can
		// distinguish "config bug" from "data miss".
		return AssetMapping{}, fmt.Errorf(
			"%w: resolver, SlotIndex, and AssetHydrator must all be non-nil",
			ErrInvalidCandidateRef,
		)
	}

	parsedSlot, idx, err := parseCandidateRef(candidateRef)
	if err != nil {
		return AssetMapping{}, err
	}

	// Cross-check the slot Gemma emitted alongside. Empty
	// slotRefMaybe = permissive (the resolver trusts the embedded
	// slot on the ref); non-empty = strict (mismatch is fail-closed).
	if slotRefMaybe != "" && parsedSlot != slotRefMaybe {
		return AssetMapping{}, fmt.Errorf(
			"%w: cross-check failed (ref embeds %q but Gemma emitted slotRef %q)",
			ErrInvalidCandidateRef,
			parsedSlot,
			slotRefMaybe,
		)
	}

	assetID, lookupErr := r.index.LookupCandidate(parsedSlot, idx)
	if lookupErr != nil {
		// SlotIndex.LookupCandidate already returns typed
		// ErrUnknownSlot / ErrUnknownIndex envelope; we wrap
		// only to add the ref context for operator audit logs.
		return AssetMapping{}, fmt.Errorf(
			"clipresolve: LookupCandidate(%q, %d): %w",
			parsedSlot, idx, lookupErr,
		)
	}

	meta, hydrateErr := r.hydrator.Hydrate(ctx, assetID)
	if hydrateErr != nil {
		return AssetMapping{}, fmt.Errorf(
			"clipresolve: Hydrate(%q): %w", assetID, hydrateErr,
		)
	}

	mapping := AssetMapping{
		AssetID:         assetID,
		DriveLink:       meta.DriveLink,
		FolderPath:      meta.FolderPath,
		NormalizedGroup: meta.NormalizedGroup,
	}

	// godlike/07 NO-FAKE-AVAILABILITY: validate the composed
	// mapping. DriveLink empty is a HARD FAIL — binding to an
	// asset without a canonical Drive URL is unrecoverable
	// downstream (the worker UI cannot seed a missing Drive
	// link from the binding layer's surface; the operator has
	// to backfill on the canonical ingest path).
	if mapping.AssetID == "" {
		return AssetMapping{}, fmt.Errorf(
			"%w: AssetID is empty after LookupCandidate (likely a programming error in the SlotIndex implementation)",
			ErrUnknownIndex,
		)
	}
	if mapping.DriveLink == "" {
		return AssetMapping{}, fmt.Errorf(
			"%w: asset %q has no DriveLink on media_assets",
			ErrAssetMappingIncomplete,
			mapping.AssetID,
		)
	}

	return mapping, nil
}

// parseCandidateRef is the canonical parser for the opaque ref.
// Shape: `<slotRef>` + CandidateRefPrefix + decimal non-negative index.
//
// godlike/06 SSOT (zero-drift boundary with clipview/builder.go):
// the SAME CandidateRefPrefix is used to BUILD the ref in clipview
// and to PARSE it here. A future drift between these two packages
// surfaces as a roundtrip test failure in clipview
// (forward-pointer to a dedicated CI gate).
//
// godlike/07 NO-FAKE-AVALIDATE (input shape): the parser rejects
//
//   - empty ref;
//   - ref without a CandidateRefPrefix;
//   - ref with an empty slotRef (":candidate-3" is invalid);
//   - ref with an empty index ("slot-1:candidate-" is invalid);
//   - ref with a non-decimal index ("slot-1:candidate-abc");
//   - ref with a negative index ("slot-1:candidate--1") — the
//     strconv.Atoi on "-1" would parse but we explicitly reject
//     negatives since SlotIndex.LookupCandidate would raise
//     ErrUnknownIndex anyway; rejecting here surfaces the typed
//     envelope faster.
//
// Each rejection wraps ErrInvalidCandidateRef so the typed envelope
// is uniform across all malformed-shape cases.
func parseCandidateRef(ref string) (string, int, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", 0, fmt.Errorf("%w: ref is empty", ErrInvalidCandidateRef)
	}

	idx := strings.Index(ref, CandidateRefPrefix)
	if idx < 0 {
		return "", 0, fmt.Errorf(
			"%w: missing %q delimiter in %q",
			ErrInvalidCandidateRef, CandidateRefPrefix, ref,
		)
	}

	// godlike/06 SSOT invariant: the canonical `":candidate-"`
	// delimiter and the surrounding slotRef/index shapes are
	// part of the ref SSOT, NOT free-text zones. We trim the
	// OUTER whitespace (some upstream map<string, candidate>
	// stores roundtrip via fmt.Sprintf with stray padding) but
	// NEVER trim inside the delimiter bounds — an internal
	// whitespace inside the canonical shape means the ref is
	// malformed, not sloppy. This asymmetric tolerance keeps the
	// SSOT format stable while staying friendly to upstream
	// storage rounding.
	slotRef := ref[:idx]
	tail := ref[idx+len(CandidateRefPrefix):]

	if slotRef == "" {
		return "", 0, fmt.Errorf(
			"%w: slotRef is empty before %q in %q",
			ErrInvalidCandidateRef, CandidateRefPrefix, ref,
		)
	}
	if tail == "" {
		return "", 0, fmt.Errorf(
			"%w: index is empty after %q in %q",
			ErrInvalidCandidateRef, CandidateRefPrefix, ref,
		)
	}
	// Hard-pin the SSOT shape: stray whitespace inside the
	// slotRef or tail is rejected even when strtol would parse
	// it (e.g. "slot-1 :candidate-0" → slotRef="slot-1 " has
	// internal space; failures-fast makes the envelope visible
	// rather than silently coercing).
	if strings.ContainsAny(slotRef, " \t\n\r") {
		return "", 0, fmt.Errorf(
			"%w: slotRef %q has internal whitespace (canonical delimiter shape violated)",
			ErrInvalidCandidateRef, slotRef,
		)
	}
	if strings.ContainsAny(tail, " \t\n\r") {
		return "", 0, fmt.Errorf(
			"%w: index %q has internal whitespace (canonical delimiter shape violated)",
			ErrInvalidCandidateRef, tail,
		)
	}

	n, convErr := strconv.Atoi(tail)
	if convErr != nil {
		return "", 0, fmt.Errorf(
			"%w: index %q is not a decimal integer in %q",
			ErrInvalidCandidateRef, tail, ref,
		)
	}
	if n < 0 {
		return "", 0, fmt.Errorf(
			"%w: index %d is negative in %q",
			ErrInvalidCandidateRef, n, ref,
		)
	}

	return slotRef, n, nil
}
