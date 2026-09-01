package dr

import "time"

type VerifyReport struct {
	Ready           bool     `json:"ready"`
	ExpectedPoints  int      `json:"expected_points"`
	ActualPoints    int      `json:"actual_points"`
	MissingCount    int      `json:"missing_count"`
	OrphanCount     int      `json:"orphan_count"`
	PayloadIssues   int      `json:"payload_issues"`
	VersionMismatch int      `json:"version_mismatch"`
	DeadLetterOpen  int      `json:"dead_letter_open"`
	Errors          []string `json:"errors,omitempty"`
}

type noopMetrics struct{}

func (noopMetrics) RecordAliasSwitch(string, float64) {}
func (noopMetrics) SetAliasCurrent(string, string)    {}

var NowFunc = time.Now
