package editorialcomposition

import (
	"errors"
	"fmt"
)

// Error codes. Every rejection is classified: a caller can assert on the code
// instead of a message, and a corpus fixture pins the code it expects.
const (
	CodeUnsupportedSchema   = "unsupported_schema"
	CodeUnknownField        = "unknown_field"
	CodeMissingField        = "missing_field"
	CodeCatalogMissing      = "catalog_missing"
	CodeDuplicateMotion     = "duplicate_motion"
	CodeInvalidWindow       = "invalid_window"
	CodeMemberOutsideWindow = "member_outside_window"
	CodeDuplicateMember     = "duplicate_member"
	CodeUnknownMemberKind   = "unsupported_member_kind"

	CodeBackgroundModeInvalid       = "background_mode_invalid"
	CodeBackgroundSourceMissing     = "background_source_missing"
	CodeBackgroundSourceUnexpected  = "background_source_unexpected"
	CodeBackgroundSourceNotPersist  = "background_source_not_persistent"
	CodeSnapshotNotContentAddressed = "snapshot_not_content_addressed"

	CodeAssetDigestInvalid        = "asset_digest_invalid"
	CodeAttachmentRoleUnsupported = "unsupported_attachment_role"
	CodeAttachmentValueInvalid    = "attachment_value_invalid"
	CodeAttachmentPositionInvalid = "attachment_position_invalid"
	CodeAttachmentDuplicate       = "duplicate_attachment"
	CodeAttachmentEvidenceInvalid = "attachment_evidence_invalid"
	CodeDuplicateDateOverlay      = "duplicate_date_overlay"

	CodeLaneInvalid           = "lane_invalid"
	CodeLinkOrderDuplicate    = "link_order_duplicate"
	CodeMotionNotInCatalog    = "motion_not_in_catalog"
	CodeMotionNotChainCapable = "motion_not_chain_capable"
	CodeMotionNotHoldCapable  = "motion_not_hold_capable"
	CodeMotionTargetMismatch  = "motion_target_mismatch"

	CodeChartLayoutUnsupported = "unsupported_chart_layout"
	CodeChartPointsMissing     = "chart_points_missing"
	CodeChartPointInvalid      = "chart_point_invalid"
	CodeChartUnitMissing       = "chart_unit_missing"
	CodeChartDuplicatePeriod   = "chart_duplicate_period"
	CodeChartSeriesInvalid     = "chart_series_invalid"

	CodeMapPinsMissing              = "map_pins_missing"
	CodeMapPinInvalid               = "map_pin_invalid"
	CodeMapPinDuplicate             = "duplicate_map_pin"
	CodeMapCalloutInvalid           = "map_callout_invalid"
	CodeMapCalloutAssetUnverified   = "map_callout_asset_unverified"
	CodeCalloutConnectorUnsupported = "unsupported_callout_connector"
	CodeCalloutPlacementUnsupported = "unsupported_callout_placement"
	CodeCalloutChoreographyUnknown  = "callout_choreography_unknown"
	CodeMapChainForbidden           = "map_chain_forbidden"

	CodeSummaryRevealInvalid      = "summary_reveal_invalid"
	CodeSummaryPointsInsufficient = "summary_points_insufficient"
	CodeSummaryOverlap            = "summary_overlap"
	CodeSummaryDuplicateText      = "summary_duplicate_text"

	CodeChapterModeInvalid      = "unsupported_chapter_mode"
	CodeChapterTitlesMissing    = "chapter_titles_missing"
	CodeChapterTitlesUnexpected = "chapter_titles_unexpected"
	CodeChapterGroundingInvalid = "chapter_grounding_invalid"
	CodeChapterOnlyHasMembers   = "chapter_only_has_members"
	CodeChapterDurationInvalid  = "chapter_duration_invalid"
	CodeChapterStyleUnsupported = "chapter_style_unsupported"
	CodeChapterTitleInvalid     = "chapter_title_invalid"
)

// Error is one fail-closed contract rejection.
type Error struct {
	// Code is the stable classification, see the Code* constants.
	Code string
	// Path is the JSON path of the offending value.
	Path string
	// Detail is the concrete rejected value, when there is one.
	Detail string
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail == "" {
		return fmt.Sprintf("%s at %s", e.Code, e.Path)
	}
	return fmt.Sprintf("%s at %s: %s", e.Code, e.Path, e.Detail)
}

// CodeOf returns the contract code of an error, or an empty string when the
// error did not come from this contract. A wrapped rejection keeps its code.
func CodeOf(err error) string {
	var contractErr *Error
	if errors.As(err, &contractErr) {
		return contractErr.Code
	}
	return ""
}
