// Package stockpipeline — step_plan_clips.go (PR-STOCK-ORCHESTRATOR-SPLIT,
// July 2026).
//
// SOLE owner of StockPlanStep — the canonical implementation of
// the stock.plan step (Step 1 of the 6-step pipeline) per
// godlike/06 SSOT (one canonical owner per fact). The step
// exercises the deterministic ClipPlanner.Plan round-trip on
// the first source, populating runState.Plan for downstream
// steps.
//
// godlike/07 fail-closed contracts:
//   - nil RunInput / empty sources → typed error
//     "no sources to plan (DirectURLs and SearchQueries are empty)".
//   - planner.Plan returns error → typed wrap via %w preserving
//     the planner's underlying typed sentinel for errors.Is traversal.
//   - Successful path → runState.Plan populated with the
//     planner's []ClipPlan output for downstream consumption.
//
// PR-STOCK-ORCHESTRATOR-SPLIT extracted this from
// orchestrator_steps.go on 2026-07-04. Pre-split: 874 LoC
// monolith. Post-split: this file is the canonical home of
// StockPlanStep; orchestrator_steps.go retains only the package
// doc + Step interface + 6 step key constants.
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
)

// StockPlanStep is the canonical implementation of stock.plan.
// It exercises the deterministic ClipPlanner.Plan round-trip on
// the first source, populating runState.Plan for downstream steps.
type StockPlanStep struct{}

func (StockPlanStep) Name() string { return StepKeyStockPlan }

func (StockPlanStep) Run(ctx context.Context, runner StepRunner) error {
	in := runner.RunInput()

	// When explicit clips are provided, bypass the deterministic
	// planner and use the timestamp ranges directly. Each clip
	// carries its own source URL; the first non-empty URL is used
	// for the VideoSource.
	if len(in.Clips) > 0 {
		srcURL := ""
		for _, clip := range in.Clips {
			if clip.URL != "" {
				srcURL = clip.URL
				break
			}
		}
		if srcURL == "" {
			return errors.New("orchestrator: stock.plan: explicit clips require at least one clip with a non-empty URL")
		}
		src := VideoSource{URL: srcURL, Title: in.Clips[0].Title, Source: srcURL}
		explicit := NewExplicitPlanner(in.Clips)
		plans, err := explicit.Plan(ctx, src, 0, 0, runner.Cfg().PolicyVersion)
		if err != nil {
			return fmt.Errorf("orchestrator: stock.plan: explicit planner: %w", err)
		}
		runner.State().Plan = plans
		return nil
	}

	src, ok := firstSource(in)
	if !ok {
		return errors.New("orchestrator: stock.plan: no sources to plan (DirectURLs, DriveURLs, and SearchQueries are empty)")
	}

	planBudget := in.TotalMinutes * 60
	if planBudget <= 0 {
		planBudget = runner.Cfg().ChunkDurationSec
	}
	plans, err := runner.Planner().Plan(
		ctx, src, planBudget,
		runner.Cfg().ClipDurationSec, runner.Cfg().PolicyVersion,
	)
	if err != nil {
		return fmt.Errorf("orchestrator: stock.plan: planner.Plan: %w", err)
	}
	runner.State().Plan = plans
	return nil
}
