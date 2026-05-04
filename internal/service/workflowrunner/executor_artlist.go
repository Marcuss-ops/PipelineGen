package workflowrunner

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/service/artlist"
)

// artlistExecutor executes artlist.run workflow steps
type artlistExecutor struct {
	svc  *artlist.Service
	log  *zap.Logger
}

// newArtlistExecutor creates a new artlist step executor
func newArtlistExecutor(svc *artlist.Service, log *zap.Logger) StepExecutor {
	return &artlistExecutor{
		svc:  svc,
		log:  log.With(zap.String("executor", "artlist.run")),
	}
}

// Execute runs the artlist step
func (e *artlistExecutor) Execute(ctx context.Context, input *StepInput) (*StepOutput, error) {
	// Parse step with parameters
	term, _ := input.Step.With["term"].(string)
	limit := 0
	if l, ok := input.Step.With["limit"].(int); ok {
		limit = l
	} else if l, ok := input.Step.With["limit"].(float64); ok {
		limit = int(l)
	}
	strategy, _ := input.Step.With["strategy"].(string)
	dryRun, _ := input.Step.With["dry_run"].(bool)

	if term == "" {
		return nil, fmt.Errorf("artlist.run: term is required")
	}

	req := &artlist.RunTagRequest{
		Term:     term,
		Limit:    limit,
		Strategy: strategy,
		DryRun:   dryRun,
	}

	// Start the artlist run (creates async job)
	resp, err := e.svc.StartRunTag(ctx, req)
	if err != nil {
		return &StepOutput{
			OK:     false,
			Status: "failed",
			Error:  fmt.Sprintf("failed to start artlist run: %v", err),
		}, nil
	}

	// If no wait requested, return immediately with run ID
	if input.Step.Wait == nil {
		return e.runTagResponseToStepOutput(resp), nil
	}

	// Wait for job completion
	finalResp, err := e.waitForRun(ctx, resp.RunID, input.Step.Wait)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for artlist run: %w", err)
	}

	return e.runTagResponseToStepOutput(finalResp), nil
}

// waitForRun polls the artlist run status until terminal
func (e *artlistExecutor) waitForRun(ctx context.Context, runID string, waitCfg *WaitConfig) (*artlist.RunTagResponse, error) {
	timeout := 900 // default 15 minutes
	if waitCfg.TimeoutSeconds > 0 {
		timeout = waitCfg.TimeoutSeconds
	}
	interval := 2000 // default 2 seconds
	if waitCfg.IntervalMS > 0 {
		interval = waitCfg.IntervalMS
	}

	timeoutDuration := time.Duration(timeout) * time.Second
	intervalDuration := time.Duration(interval) * time.Millisecond
	deadline := time.Now().Add(timeoutDuration)

	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for artlist run %s", runID)
		}

		resp, err := e.svc.GetRunTag(ctx, runID)
		if err != nil {
			e.log.Warn("failed to get run status", zap.String("run_id", runID), zap.Error(err))
		// Wait for interval or context cancellation
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while waiting for artlist run %s: %w", runID, ctx.Err())
		case <-time.After(intervalDuration):
			// Continue to next status check
		}
			continue
		}

		// Check terminal status
		switch resp.Status {
		case "completed":
			return resp, nil
		case "failed", "cancelled", "zombie":
			return resp, fmt.Errorf("artlist run %s ended with status: %s, error: %s", runID, resp.Status, resp.Error)
		}

		time.Sleep(intervalDuration)
	}
}

// runTagResponseToStepOutput converts artlist RunTagResponse to standardized StepOutput
func (e *artlistExecutor) runTagResponseToStepOutput(resp *artlist.RunTagResponse) *StepOutput {
	output := &StepOutput{
		OK:     resp.OK,
		Status: resp.Status,
		RunID:  resp.RunID,
		Error:  resp.Error,
	}

	// Convert RunTagItem to AssetItem
	for _, item := range resp.Items {
		output.Items = append(output.Items, AssetItem{
			ID:        item.ClipID,
			Title:     item.Name,
			Source:    "artlist",
			DriveLink: item.DriveLink,
			LocalPath: item.LocalPath,
			Hash:      item.FileHash,
			Status:    item.Status,
			Error:     item.Error,
		})
	}

	// Set folder ID if available
	if resp.TagFolderID != "" {
		output.FolderID = resp.TagFolderID
	}

	// Add raw response for debugging
	output.Raw = map[string]interface{}{
		"term":      resp.Term,
		"strategy":  resp.Strategy,
		"found":     resp.Found,
		"processed": resp.Processed,
		"skipped":   resp.Skipped,
		"failed":    resp.Failed,
	}

	return output
}
