package mediacert

// OperationalOwnershipReport applies the canonical ownership rules to a
// live result whose scenario-specific semantic spec is supplied elsewhere.
// It exists for the operational runner so shell code does not reimplement
// query/asset provenance or cross-scene reuse with ad-hoc jq expressions.
func OperationalOwnershipReport(result MediaResult) Report {
	spec := Spec{AllowCrossSceneAssetReuse: false}
	for _, segment := range result.Segments {
		spec.SegmentsExpected = append(spec.SegmentsExpected, SpecSegment{ID: segment.SegmentID})
	}
	checks := []CheckResult{
		ruleQueryOwnership(spec, result),
		ruleAssetOwnership(spec, result),
		ruleCrossSceneReuse(spec, result),
		ruleCrossContamination(spec, result),
	}
	certified := true
	for _, check := range checks {
		certified = certified && check.Passed
	}
	return Report{JobStatus: result.JobStatus, Certified: certified, Checks: checks}
}
