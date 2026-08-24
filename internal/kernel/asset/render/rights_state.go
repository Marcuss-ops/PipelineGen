// Package asset — rights_state.go (PR-CLIPINGEST-PIPELINE Step 10, July 2026).
//
// RightsStatus + ReviewStatus are the canonical, EXPLICIT enums for
// the per-asset rights surface added by migration 158. This file is
// the single source of truth for the alphabets + helper predicates;
// no other package declares a `RightsStatus` / `ReviewStatus` enum
// or shadowed constant.
//
// godlike/06 SSOT (non-negotiable): this file is the SOLE canonical
// owner of the 6 RightsStatus values and 4 ReviewStatus values.
// No convenience method like `IsPublishable()` is allowed at any
// other layer — such a method would smuggle a pre-applied filter
// back into the SSOT and is a godlike/06 SSOT violation.
//
// godlike/07 fail-closed contract: the zero-value
// RightsStatus("") MUST NOT pass any IsPublishable /
// IsReviewGateRequired check. The Clip Pre-Planner (via
// SlotSearchPort.SearchSlots) uses IsRightsRestrictedPredicate as
// the canonical "must skip" surface; a missing rights_status on a
// legacy row defaults to "review_required" via migration 158's
// DEFAULT clause so the filter applies to every row that has not
// been explicitly classified.
//
// Relationship to the existing state machines (godlike/06 SSOT
// orthogonality):
//
//   - LifecycleState (lifecycle_state.go) — orthogonal; tracks
//     deletion/online semantics. An asset can be
//     LifecycleState=ACTIVE and RightsStatus=owned simultaneously;
//     or LifecycleState=DELETE_REQUESTED while the rights surface
//     stays at its persistent value until the soft-delete stamp
//     lands.
//
//   - AssetState (asset_state_values.go, 14-state machine) — orthogonal;
//     tracks the asset's journey from discovery.
//     RightsStatus stays as the operator-declared publishing
//     permission surface; AssetState stays as the worker's
//     progress view.
//
//   - ReviewStatus (this file) — left intentionally separate from
//     RightsStatus. RightsStatus answers "is this asset usable in
//     editorial right now?"; ReviewStatus answers "is the operator
//     actively reviewing a rights claim?". An asset can be
//     RightsStatus=owned AND ReviewStatus=pending while the
//     operator audits a counter-party claim; the planner should
//     still skip the asset in this window per the IsPublishable
//     predicate.
//
// Step 10 user spec: "Il Clip Pre-Planner ignora automaticamente
// asset blocked o review_required salvo override esplicito."
// Translation: the planner auto-skips assets whose rights surface
// says "do not use right now" — namely rights_status IN (blocked,
// review_required). The override flag (SlotsSearchOptions.
// IncludeRightRestricted, declared in
// internal/capabilities/scripts/ports/clip_search_port.go) lets
// operators explicitly opt into the restricted set; the default
// is the safe side (skip).
package render

// RightsStatus is the canonical per-asset rights posture enum.
// 6 values, all lowercase, matching the canonical wire alphabet
// declared in step 10.
type RightsStatus string

const (
	// RightsStatusOwned — the operator owns the asset outright
	// (uploaded by the operator from a personal archive, captured
	// in-house, etc.). Planning paths default to allowing this.
	RightsStatusOwned RightsStatus = "owned"

	// RightsStatusLicensed — the asset is covered by a per-call
	// license the operator has on file. Tied to the
	// internal/kernel/asset/license_release.go::AssetLicense
	// surface via the license_basis column (the canonical
	// pointer to AssetLicense.id; operator workflow).
	RightsStatusLicensed RightsStatus = "licensed"

	// RightsStatusCreativeCommons — the asset is published under a
	// Creative Commons license (CC-BY, CC0, etc.). Planning
	// paths default to allowing this; the specific CC variant is
	// carried by license_basis (freeform string set by the
	// operator — e.g. "CC-BY-4.0").
	RightsStatusCreativeCommons RightsStatus = "creative_commons"

	// RightsStatusPermissionGranted — the operator has explicit,
	// stored permission for this asset (a written grant, e.g.
	// counter-party mailed release). Distinct from
	// RightsStatusLicensed (which is a paid commercial license).
	// Planning paths default to allowing this.
	RightsStatusPermissionGranted RightsStatus = "permission_granted"

	// RightsStatusReviewRequired — pending operator review; the
	// asset is NOT auto-included in planning until the operator
	// upgrades to one of the publishable values above. This is
	// the migration 158 DEFAULT for fresh / legacy rows so a
	// missing rights posture fails CLOSED on the planner side.
	RightsStatusReviewRequired RightsStatus = "review_required"

	// RightsStatusBlocked — explicit forbid; the operator has
	// flagged this asset as legally or ethically restricted. The
	// planner MUST skip it even when IncludeRightRestricted=true
	// is requested, because a rights-DENY explicitly overrides
	// any opt-in (godlike/07 fail-closed: "do not represent an
	// unavailable backend as a successful no-op"; here the
	// unavailable surface is "legal permission to publish").
	RightsStatusBlocked RightsStatus = "blocked"
)

// CanonicalRightsStatusValues returns the closed enumeration of
// canonical RightsStatus strings, in canonical-declaration order.
// Callers use this as the canonical source-of-truth list for
// migrations (CHECK constraint), dashboards, and
// percheck_rights_status_canonical_6 forward-prevention gate.
func CanonicalRightsStatusValues() []RightsStatus {
	return []RightsStatus{
		RightsStatusOwned,
		RightsStatusLicensed,
		RightsStatusCreativeCommons,
		RightsStatusPermissionGranted,
		RightsStatusReviewRequired,
		RightsStatusBlocked,
	}
}

// validRightsStatusSet is the O(1) membership set backing
// Valid(). Built once at init from CanonicalRightsStatusValues().
var validRightsStatusSet = func() map[RightsStatus]struct{} {
	m := make(map[RightsStatus]struct{}, len(CanonicalRightsStatusValues()))
	for _, s := range CanonicalRightsStatusValues() {
		m[s] = struct{}{}
	}
	return m
}()

// Valid returns true if s is one of the canonical RightsStatus
// values. Defensive against ad-hoc string values; mirrors the
// pattern in LifecycleState.Valid / AssetState.Valid.
func (s RightsStatus) Valid() bool {
	_, ok := validRightsStatusSet[s]
	return ok
}

// String makes RightsStatus satisfy fmt.Stringer so the canonical
// log/diagnostic tag (zap.Stringer(...) rendering) shows the
// wire-format value without explicit casts.
func (s RightsStatus) String() string { return string(s) }

// IsPublishable reports whether the rights posture permits the
// Clip Pre-Planner to include the asset in
// SlotSearchPort.SearchSlots by default (godlike/07 fail-closed:
// returns false on the zero value so a legacy row with NULL
// rights_status correctly fails the planner-side filter).
//
// Mirrors the user-spec "ignora automaticamente asset blocked o
// review_required" rule at the Type SSOT level. A future
// follow-up can wire the inverse (IsRestricted) explicitly if
// the planner-side filter wants to be explicit about why.
func (s RightsStatus) IsPublishable() bool {
	if !s.Valid() {
		// Zero value + unknown values BOTH fail closed —
		// legitimate decision for a defensive default.
		return false
	}
	switch s {
	case RightsStatusOwned,
		RightsStatusLicensed,
		RightsStatusCreativeCommons,
		RightsStatusPermissionGranted:
		return true
	case RightsStatusReviewRequired, RightsStatusBlocked:
		return false
	}
	return false
}

// RestrictedRightsStatuses returns the canonical set of
// RightsStatus values that MUST be skipped by the planning-tier
// filter when the safe default is in effect (IncludeRightRestricted=false).
// Namesake of `OutFolder* / Outdated*` style enumerations — the
// caller partition is canonical at the TYPE SSOT, the caller
// composes the predicate from this slice.
//
// godlike/06 SSOT (one canonical owner per fact): this function
// is the SOLE definition of the "must skip" set. slots_search.go's
// per-slot filter pulls from this slice via the adapter-level
// composition; a future addition to RightsStatus automatically
// inherits the correct membership-classification without a
// parallel update.
//
// The function-form is preferred over a package-level slice so
// the membership list stays locked to the canonical alphabet
// (a future addition to RightsStatus automatically inherits the
// correct membership-classification without a parallel update).
func RestrictedRightsStatuses() []RightsStatus {
	out := make([]RightsStatus, 0, 2)
	for _, v := range CanonicalRightsStatusValues() {
		if !v.IsPublishable() {
			out = append(out, v)
		}
	}
	return out
}

// ── Review Status (parallel surface, distinct semantics) ─────────────

// ReviewStatus tracks whether the operator is actively auditing
// a rights claim against this asset. 4 values.
//
// Distinct from RightsStatus: RightsStatus answers "what is the
// publishing permission?"; ReviewStatus answers "is a human
// reviewing the claim right now?". An asset can be
// RightsStatus=owned AND ReviewStatus=pending while the operator
// audits a counter-party claim; the planner still skips this
// asset in this window — the IsReviewGateRequired predicate
// (below) is the canonical planner-side gate.
type ReviewStatus string

const (
	// ReviewStatusNone — no review in flight; this is the
	// fallback DEFAULT for fresh rows. The asset is allowed to
	// pass the planner filter IF and only if the parallel
	// RightsStatus is publishable.
	ReviewStatusNone ReviewStatus = "none"

	// ReviewStatusPending — operator is actively reviewing a
	// rights claim (audit, dispute resolution, etc.). The
	// planner MUST skip the asset regardless of
	// IncludeRightRestricted overrides; a review is a rights
	// gate analogous to blocked / review_required.
	ReviewStatusPending ReviewStatus = "pending"

	// ReviewStatusApproved — the review concluded positively;
	// downstream rights decisions may proceed. The planner
	// filter does NOT skip an Approved row, regardless of the
	// ReviewStatus itself; the planner filter is solely about
	// RightsStatus. The two surfaces compose: ReviewStatus=approved
	// + RightsStatus=licensed → planner-allow.
	ReviewStatusApproved ReviewStatus = "approved"

	// ReviewStatusRejected — the review concluded negatively;
	// the asset is permanently restricted. Combines with
	// RightsStatus=Blocked in the planner-side filter (the
	// planner still skips the asset, for either surface
	// dimension).
	ReviewStatusRejected ReviewStatus = "rejected"
)

// CanonicalReviewStatusValues returns the closed enumeration of
// canonical ReviewStatus strings, in canonical-declaration order.
// Callers use this for migrations (CHECK constraint) and
// percheck_review_status_canonical_4 forward-prevention gate.
func CanonicalReviewStatusValues() []ReviewStatus {
	return []ReviewStatus{
		ReviewStatusNone,
		ReviewStatusPending,
		ReviewStatusApproved,
		ReviewStatusRejected,
	}
}

// validReviewStatusSet is the O(1) membership set backing Valid().
var validReviewStatusSet = func() map[ReviewStatus]struct{} {
	m := make(map[ReviewStatus]struct{}, len(CanonicalReviewStatusValues()))
	for _, s := range CanonicalReviewStatusValues() {
		m[s] = struct{}{}
	}
	return m
}()

// Valid returns true if s is one of the canonical ReviewStatus
// values.
func (s ReviewStatus) Valid() bool {
	_, ok := validReviewStatusSet[s]
	return ok
}

// String makes ReviewStatus satisfy fmt.Stringer so the canonical
// log/diagnostic tag shows the wire-format value verbatim.
func (s ReviewStatus) String() string { return string(s) }

// IsReviewGateRequired reports whether an asset with the given
// review state should be excluded from planner-side selection.
// False on the zero value so a legacy row with NULL review_status
// (migrating from a pre-Step-10 schema) does NOT spuriously fail
// the planner-side gate; the RightsStatus surface is the
// authoritative gate (IsPublishable), and this is a SECONDARY
// surface that ONLY filters out Pending+Rejected rows.
//
// godlike/06 SSOT: this is the canonical "review-restricted"
// predicate; slots_search.go's planner filter composes
// (RightsStatus not in {blocked, review_required}) AND
// (ReviewStatus not in {pending, rejected}) into the Qdrant
// MustNot clause.
func (s ReviewStatus) IsReviewGateRequired() bool {
	if !s.Valid() {
		return false
	}
	switch s {
	case ReviewStatusPending, ReviewStatusRejected:
		return true
	case ReviewStatusNone, ReviewStatusApproved:
		return false
	}
	return false
}

// DefaultRightsStatus is the migration 158 DEFAULT for fresh
// rows / legacy no-value rows. "review_required" is the
// fail-closed choice (matches canonical.go's existing
// rights_status column default from migration 152). godlike/07:
// newly-minted rows are NOT publishable until an operator
// explicitly upgrades them.
const DefaultRightsStatus = RightsStatusReviewRequired

// DefaultReviewStatus is the migration 158 DEFAULT for the new
// review_status column. "none" is the fail-OPEN choice for the
// review dimension (a missing review_status does NOT spuriously
// gate the planner; RightsStatus is the authoritative surface).
const DefaultReviewStatus = ReviewStatusNone
