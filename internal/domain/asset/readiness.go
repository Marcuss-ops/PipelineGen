// Package asset — readiness.go (PR-CATALOG-MULTILINGUA step 7,
// July 2026).
//
// READY_MULTILINGUAL gate predicate. The canonical answer to
// "is this asset publishable into the multilingual pipeline
// right now?" requires the AND-conjunction of five sub-checks:
//
//	(a) render_master verified on Drive
//	(b) originals (transcript + description + visual_summary)
//	    present in asset_text_tracks with is_current=1
//	(c) every language in language_registry.EnabledLanguages()
//	    has at least one current track entry
//	(d) Qdrant point updated (qdrant_point_verifier round-trip)
//	(e) durable_outbox empty for this asset
//
// godlike/06 SSOT: this file is the SOLE canonical owner of
// the readiness predicate function signature + the typed
// GateRequirement surface + the MultilingualGateDeps dependency
// tuple + the ReadinessAudit result struct. Callers (write-
// side finalizer, dashboard, operator CLI) consume these
// types verbatim. Duplicating the AND-conjunction at any
// other layer is a godlike/06 SSOT violation.
//
// godlike/07 fail-fast: the predicate returns a structured
// ReadinessAudit (Passed + Missing list + Diagnostic string)
// rather than a bare boolean. A bare bool would force operators
// to retrace the AND-conjunction to discover WHICH gate
// failed; the structured audit surfaces the failing gate
// immediately.
//
// godlike/06 forward-pointer: a future PR replaces each
// MultilingualGateDeps closure with a concrete reader (Drive
// SDK + asset_text_tracks repository + LanguageRegistry +
// qdrant point verifier + outbox drainer). The current
// closure shape is the pure-function SSOT; wired readers
// are orthogonal and assembly-tested via testutil fakes. A
// concrete `MultilingualGateDepsIface` refactor is allowed
// but not required — the current closure shape is SSOT
// because it makes unit tests trivial.
package asset

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// GateRequirement names one of the five sub-checks the
// READY_MULTILINGUAL gate performs. Missing-list surfaces
// call this surface verbatim (gates are reported by name).
type GateRequirement string

const (
	// GateRenderMasterOnDrive — the media_assets.drive_file_id
	// of the canonical render (the highest-quality rendition
	// for the asset) is verified to exist on Drive.
	// Verification is the Drive SDK Files.Get round-trip plus
	// a content-hash check on the server-side if a local hash
	// is provided.
	GateRenderMasterOnDrive GateRequirement = "render_master_verified_on_drive"

	// GateOriginalsPresent — the three required original-
	// language catalogues are present in asset_text_tracks
	// with is_current=1 AND is_original=true:
	//   - transcript
	//   - description
	//   - visual_summary
	// The Diagnostic field surfaces which of the three is
	// missing.
	GateOriginalsPresent GateRequirement = "originals_present"

	// GateRequiredLanguagesPresent — every language in
	// language_registry.EnabledLanguages() with Enabled=true
	// AND TranslateClips=true has at least one current track
	// entry (asset_text_tracks row with that language_code
	// AND is_current=1). The Diagnostic field surfaces the
	// missing language codes (sorted).
	GateRequiredLanguagesPresent GateRequirement = "required_languages_present"

	// GateQdrantUpdated — the media_assets qdrant point
	// round-trip succeeds for the canonical asset_id. The
	// verifier issues a HEAD-equivalent Qdrant point lookup
	// plus a hash check.
	GateQdrantUpdated GateRequirement = "qdrant_updated"

	// GateOutboxEmpty — the durable_outbox has zero pending
	// events for the asset_id. Operator-induced or system-
	// induced outbox events that haven't yet applied are a
	// ready-blocking condition (a publishable asset must
	// have a drained outbox at the moment of
	// READY_MULTILINGUAL).
	GateOutboxEmpty GateRequirement = "outbox_empty"
)

// CanonicalGateRequirementValues returns the closed enumeration
// of GateRequirement values; mirrors the pattern from
// CanonicalAssetStateValues. Test
// TestCanonicalGateRequirementValues pins the count.
func CanonicalGateRequirementValues() []GateRequirement {
	return []GateRequirement{
		GateRenderMasterOnDrive,
		GateOriginalsPresent,
		GateRequiredLanguagesPresent,
		GateQdrantUpdated,
		GateOutboxEmpty,
	}
}

// MultilingualGateDeps is the dependency tuple the readiness
// predicate consumes. Each function returns observed-at-truth
// information; godlike/07 fail-closed means errors propagate
// as a typed ErrReadinessPredicateDependency error.
//
// godlike/06 SSOT: this is the SOLE canonical dependency tuple
// for the readiness predicate. Callers assemble the tuple at
// the composition root; the predicate does NOT construct its
// own collaborators. A nil dep field surfaces as
// ErrReadinessPredicateDepsNil — the predicate does NOT
// silently fall back to a single-gate check.
type MultilingualGateDeps struct {
	// VerifyDriveRenderMaster round-trips to Drive and returns
	// whether the canonical render is verified.
	VerifyDriveRenderMaster func(ctx context.Context, driveFileID string, contentHash string) (verified bool, err error)

	// ListOriginalsPresent returns the three booleans for the
	// is_current=1, is_original=true trio: transcript,
	// description, visual_summary.
	ListOriginalsPresent func(ctx context.Context, assetID string) (transcript bool, description bool, visualSummary bool, err error)

	// ListEnabledLanguages returns the language_registry
	// EnabledLanguages() projection verbatim (the
	// capability-filter contract on LanguageRegistry
	// guarantees no pre-filter at this layer).
	ListEnabledLanguages func() []LanguageSpec

	// ListCurrentTracksForAsset returns the set of language
	// codes that have at least one asset_text_tracks row
	// with is_current=1 for the asset_id.
	ListCurrentTracksForAsset func(ctx context.Context, assetID string) (current map[string]bool, err error)

	// VerifyQdrantPoint returns (pointExists, pointHashMatches).
	// Both must be true for the gate to pass; a drift in
	// either surfaces as a gate failure.
	VerifyQdrantPoint func(ctx context.Context, assetID string) (pointExists bool, pointHashMatches bool, err error)

	// VerifyOutboxEmpty returns the count of pending outbox
	// events for the asset. Zero is required for the gate to
	// pass.
	VerifyOutboxEmpty func(ctx context.Context, assetID string) (pending int, err error)
}

// ReadinessAudit is the structured result of the predicate.
// The Diagnostic field is the canonical "operator sees"
// string; Missing is the canonical "automated retry"
// surface. A bare bool was considered and rejected — the
// structured surface is the godlike/07 fail-fast contract.
type ReadinessAudit struct {
	Passed     bool
	Missing    []GateRequirement
	Diagnostic string
}

// readiness errors. All three are sentinel values for
// godlike/07 callers; wrapped errors retain them via
// fmt.Errorf("%w: ...", ...).
var (
	// ErrReadinessPredicateAssetIDEmpty — the caller passed
	// an empty asset_id. Failure-fast on operator error.
	ErrReadinessPredicateAssetIDEmpty = errors.New("asset: readiness predicate: asset_id is empty")

	// ErrReadinessPredicateDepsNil — a MultilingualGateDeps
	// closure is nil. The predicate refuses to silently
	// treat nil as "this gate is skipped" because the
	// composition root has a typo or a wiring bug; either
	// way the operator must fix it.
	ErrReadinessPredicateDepsNil = errors.New("asset: readiness predicate: dependency closure is nil")

	// ErrReadinessPredicateDependency — a MultilingualGateDeps
	// closure returned a non-nil error. The wrapped error
	// carries the underlying cause; the message names the
	// failing gate.
	ErrReadinessPredicateDependency = errors.New("asset: readiness predicate: dependency error")
)

// EvaluateMultilingualReadiness runs the AND-conjunction of
// the 5 sub-gates and returns the structured audit.
//
//	gate (a) PASSED → render_master verified on Drive.
//	gate (b) PASSED → {transcript, description, visual_summary}
//	                 all present (is_current=1, is_original=true).
//	gate (c) PASSED → every enabled language has a current track.
//	gate (d) PASSED → Qdrant point exists AND hash matches.
//	gate (e) PASSED → outbox has zero pending events.
//
// Returns:
//
//	passed=true  → all 5 gates pass; the asset MAY move to
//	               StateAssetReadyMultilingual via the finalizer
//	               writer. The state machine itself does NOT
//	               auto-advance — the policy is up to the writer
//	               + scheduler.
//
//	passed=false → at least one gate failed. Missing lists
//	               the failing gates; Diagnostic surfaces a
//	               human-readable breakdown. The asset remains
//	               at StateAssetReady (single-locale ready)
//	               until every gate passes.
//
// Errors:
//
//   - ErrReadinessPredicateAssetIDEmpty on empty asset_id.
//   - ErrReadinessPredicateDepsNil on a nil closure (the
//     message names which gate's closure is nil).
//   - ErrReadinessPredicateDependency on a closure returning
//     a non-nil error (the message names which gate failed and
//     wraps the underlying error).
//
// godlike/06 SSOT: this is the SOLE canonical readiness
// predicate. The "is this asset multilingual-ready" boolean
// check is forbidden at every layer below this function; code
// that duplicates the AND-conjunction is a godlike/06 SSOT
// violation requiring an explicit godlike-allowance exception.
//
// godlike/06 forward-pointer: a future "wired-in" version of
// this predicate replaces the MultilingualGateDeps closures
// with concrete readers via the composition root. Until then,
// the closure shape IS the SSOT.
func EvaluateMultilingualReadiness(
	ctx context.Context,
	deps MultilingualGateDeps,
	assetID string,
	driveFileID string,
	contentHash string,
) (ReadinessAudit, error) {
	if assetID == "" {
		return ReadinessAudit{}, ErrReadinessPredicateAssetIDEmpty
	}
	out := ReadinessAudit{
		Passed:  true,
		Missing: []GateRequirement{},
	}
	diags := []string{}

	// (a) render_master verified on Drive.
	if deps.VerifyDriveRenderMaster == nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s", ErrReadinessPredicateDepsNil, GateRenderMasterOnDrive)
	}
	verified, err := deps.VerifyDriveRenderMaster(ctx, driveFileID, contentHash)
	if err != nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s: %v", ErrReadinessPredicateDependency, GateRenderMasterOnDrive, err)
	}
	if !verified {
		out.Passed = false
		out.Missing = append(out.Missing, GateRenderMasterOnDrive)
		diags = append(diags, "render_master not verified on Drive for "+driveFileID)
	}

	// (b) originals (transcript + description + visual_summary).
	if deps.ListOriginalsPresent == nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s", ErrReadinessPredicateDepsNil, GateOriginalsPresent)
	}
	transcript, description, visualSummary, err := deps.ListOriginalsPresent(ctx, assetID)
	if err != nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s: %v", ErrReadinessPredicateDependency, GateOriginalsPresent, err)
	}
	if !transcript || !description || !visualSummary {
		out.Passed = false
		out.Missing = append(out.Missing, GateOriginalsPresent)
		var missingSub []string
		if !transcript {
			missingSub = append(missingSub, "transcript")
		}
		if !description {
			missingSub = append(missingSub, "description")
		}
		if !visualSummary {
			missingSub = append(missingSub, "visual_summary")
		}
		diags = append(diags, fmt.Sprintf("originals missing in asset_text_tracks (is_current=1, is_original=true): %s", strings.Join(missingSub, ", ")))
	}

	// (c) every enabled language has a current track.
	if deps.ListEnabledLanguages == nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s", ErrReadinessPredicateDepsNil, GateRequiredLanguagesPresent)
	}
	enabled := deps.ListEnabledLanguages()
	if len(enabled) == 0 {
		// Empty registry means "multilingual pipeline
		// disabled"; that's a configuration state, NOT a
		// gate failure. Other gates still apply; Passed
		// remains true if every other gate passed. The
		// diagnostic surfaces the disabled-pipeline state
		// so the operator sees the deployment-mode.
		diags = append(diags, "language registry empty (multilingual pipeline disabled at config layer)")
	} else {
		// Non-empty registry: always surface the configured
		// count so the operator sees the configured-vs-actual
		// fan-out status on every invocation, not only on
		// missing-track failure. godlike/07 fail-fast.
		diags = append(diags, fmt.Sprintf("language registry enabled count: %d", len(enabled)))
		if deps.ListCurrentTracksForAsset == nil {
			return ReadinessAudit{}, fmt.Errorf("%w: %s", ErrReadinessPredicateDepsNil, GateRequiredLanguagesPresent)
		}
		current, err := deps.ListCurrentTracksForAsset(ctx, assetID)
		if err != nil {
			return ReadinessAudit{}, fmt.Errorf("%w: %s: %v", ErrReadinessPredicateDependency, GateRequiredLanguagesPresent, err)
		}
		var missingLangs []string
		for _, spec := range enabled {
			if !spec.Enabled {
				continue
			}
			if !current[spec.Code] {
				missingLangs = append(missingLangs, spec.Code)
			}
		}
		if len(missingLangs) > 0 {
			sortStrings(missingLangs)
			out.Passed = false
			out.Missing = append(out.Missing, GateRequiredLanguagesPresent)
			diags = append(diags, fmt.Sprintf("required languages missing current tracks: %s", strings.Join(missingLangs, ", ")))
		}
	}

	// (d) Qdrant point exists and hash matches.
	if deps.VerifyQdrantPoint == nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s", ErrReadinessPredicateDepsNil, GateQdrantUpdated)
	}
	pointExists, pointHashMatches, err := deps.VerifyQdrantPoint(ctx, assetID)
	if err != nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s: %v", ErrReadinessPredicateDependency, GateQdrantUpdated, err)
	}
	if !pointExists || !pointHashMatches {
		out.Passed = false
		out.Missing = append(out.Missing, GateQdrantUpdated)
		diags = append(diags, fmt.Sprintf("qdrant point check failed (exists=%t, hashMatches=%t)", pointExists, pointHashMatches))
	}

	// (e) outbox empty.
	if deps.VerifyOutboxEmpty == nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s", ErrReadinessPredicateDepsNil, GateOutboxEmpty)
	}
	pending, err := deps.VerifyOutboxEmpty(ctx, assetID)
	if err != nil {
		return ReadinessAudit{}, fmt.Errorf("%w: %s: %v", ErrReadinessPredicateDependency, GateOutboxEmpty, err)
	}
	if pending != 0 {
		out.Passed = false
		out.Missing = append(out.Missing, GateOutboxEmpty)
		diags = append(diags, fmt.Sprintf("outbox has %d pending events for asset; expected 0", pending))
	}

	out.Diagnostic = strings.Join(diags, " | ")
	return out, nil
}

// sortStrings is a tiny helper for the gate (c) diagnostic
// ordering; kept here so the predicate file governs its own
// surface and doesn't import `sort` from any other godlike/06
// package. The implementation is intentionally trivial
// (insertion sort); godlike/06 SSOT — the canonical sort
// helper for gate diagnostics lives here. To swap to a
// different algorithm, change this helper and add a
// regression test.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
