package mediacert

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuleImageFanoutSkipsWhenNoRequirement(t *testing.T) {
	spec := Spec{ImagesPerSegment: 0, SegmentsExpected: []SpecSegment{{ID: "segment-1"}}}
	result := MediaResult{Segments: []ResultSegment{{SegmentID: "segment-1"}}}
	check := ruleImageFanout(spec, result)
	require.True(t, check.Skipped)
	require.False(t, check.Passed)
	require.Equal(t, "no image requirement", check.SkipReason)
}

func TestCertifyAcceptsSkippedImageFanout(t *testing.T) {
	spec := mediterraneanSpec()
	spec.ImagesPerSegment = 0
	result := MediaResult{JobStatus: "SUCCEEDED"}
	for i, expected := range spec.SegmentsExpected {
		terms := expected.RequiredConcepts
		if len(terms) > 3 {
			terms = terms[:3]
		}
		result.Segments = append(result.Segments,
			correctMediterraneanSegment(expected.ID, i, expected.Subject, expected.Subject, terms))
	}

	report := Certify(spec, result)
	require.True(t, report.Certified, "a skipped IMAGE FANOUT must not fail certification: %+v", report.Checks)

	var fanout CheckResult
	for _, check := range report.Checks {
		if check.Name == CheckImageFanout {
			fanout = check
			break
		}
	}
	require.True(t, fanout.Skipped)
	require.Equal(t, "no image requirement", fanout.SkipReason)
	require.Contains(t, HumanReport(report), "IMAGE FANOUT")
	require.Contains(t, HumanReport(report), "SKIP (no image requirement)")
}
