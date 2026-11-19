package rustexec

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// PreflightRequest is the read-only pre-render check payload carried by the
// mediaexec.v2 `preflight` operation. It has no output path: the Rust engine
// only reads assets and returns a report, and Go is the component that decides
// whether a job may proceed.
//
// VideoDurationMS is a pointer so "unknown" stays distinct from a declared
// zero (which the engine rejects). When it is nil, the end-of-video checks are
// skipped rather than guessed.
type PreflightRequest struct {
	VideoDurationMS *int64             `json:"video_duration_ms,omitempty"`
	Assets          []PreflightAsset   `json:"assets,omitempty"`
	Subtitles       []PreflightCue     `json:"subtitles,omitempty"`
	Overlays        []PreflightOverlay `json:"overlays,omitempty"`
}

type PreflightAsset struct {
	AssetID string `json:"asset_id"`
	Path    string `json:"path"`
}

type PreflightCue struct {
	ItemID string `json:"item_id"`
	InUS   int64  `json:"in_us"`
	OutUS  int64  `json:"out_us"`
	Text   string `json:"text,omitempty"`
}

type PreflightOverlay struct {
	ItemID string `json:"item_id"`
	Track  uint32 `json:"track"`
	InUS   int64  `json:"in_us"`
	OutUS  int64  `json:"out_us"`
}

type PreflightSeverity string

const (
	PreflightSeverityError   PreflightSeverity = "error"
	PreflightSeverityWarning PreflightSeverity = "warning"
)

type PreflightIssue struct {
	Severity PreflightSeverity `json:"severity"`
	// Code is the stable machine discriminant (`asset_missing`, `cue_overlap`,
	// `cue_reading_too_fast`, …). Detail is prose for operators only.
	Code   string `json:"code"`
	ItemID string `json:"item_id,omitempty"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail"`
}

// PreflightReport mirrors pipelinegen_muscles::preflight::PreflightReport.
type PreflightReport struct {
	OK     bool             `json:"ok"`
	Issues []PreflightIssue `json:"issues"`
}

// PreflightDecision is the Go-owned verdict. Blocked is derived here from the
// error-severity issues, never from a request to Rust: the engine reports
// facts, PipelineGen owns the policy.
type PreflightDecision struct {
	Report        PreflightReport
	Blocked       bool
	BlockingCodes []string
	WarningCodes  []string
}

// Preflight runs one read-only preflight check. A transport failure is an
// error; a report with blocking issues is a successful call whose decision has
// Blocked=true, so the caller still receives every reason.
func (c *Client) Preflight(ctx context.Context, req PreflightRequest) (PreflightDecision, error) {
	if c == nil {
		return PreflightDecision{}, fmt.Errorf("rust media preflight: client is nil")
	}
	result, err := c.call(ctx, request{
		Operation: OperationPreflight,
		Preflight: &req,
	})
	if err != nil {
		return PreflightDecision{}, err
	}
	if result.Preflight == nil {
		return PreflightDecision{}, fmt.Errorf("rust media preflight: response carries no report")
	}
	decision := decidePreflight(*result.Preflight)
	if decision.Blocked {
		c.log.Warn("rust media preflight blocked the render",
			zap.Strings("codes", decision.BlockingCodes))
	} else if len(decision.WarningCodes) > 0 {
		c.log.Info("rust media preflight warnings",
			zap.Strings("codes", decision.WarningCodes))
	}
	return decision, nil
}

// decidePreflight maps severities to the blocking policy. A report that
// disagrees with its own issues (ok=false without one error) is treated as
// blocking, so a drifted worker cannot let an invalid plan through.
func decidePreflight(report PreflightReport) PreflightDecision {
	decision := PreflightDecision{Report: report}
	for _, issue := range report.Issues {
		switch issue.Severity {
		case PreflightSeverityError:
			decision.Blocked = true
			decision.BlockingCodes = append(decision.BlockingCodes, issue.Code)
		case PreflightSeverityWarning:
			decision.WarningCodes = append(decision.WarningCodes, issue.Code)
		}
	}
	if !report.OK && !decision.Blocked {
		decision.Blocked = true
		decision.BlockingCodes = append(decision.BlockingCodes, "preflight_report_not_ok")
	}
	return decision
}
