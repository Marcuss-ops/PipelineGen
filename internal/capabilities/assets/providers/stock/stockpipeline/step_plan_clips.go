// Package stockpipeline — step_plan_clips.go (PR-STOCK-ORCHESTRATOR-SPLIT,
// July 2026).
//
// SOLE owner of StockPlanStep — the canonical implementation of
// the stock.plan step (Step 1 of the 6-step pipeline) per
// godlike/06 SSOT (one canonical owner per fact). The step
// exercises the deterministic ClipPlanner.Plan round-trip for
// every concrete source, populating RunState.Plan for downstream
// steps.
//
// godlike/07 fail-closed contracts:
//   - nil RunInput / empty sources → typed error
//     "no sources to plan (DirectURLs and SearchQueries are empty)".
//   - planner.Plan returns error → typed wrap via %w preserving
//     the planner's underlying typed sentinel for errors.Is traversal.
//   - Successful path → RunState.Plan populated with the
//     concatenated planner output for downstream consumption.
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
	"strings"

	"go.uber.org/zap"
)

// StockPlanStep is the canonical implementation of stock.plan.
// It exercises the deterministic ClipPlanner.Plan round-trip on
// the first source, populating RunState.Plan for downstream steps.
type StockPlanStep struct{}

func (StockPlanStep) Name() string { return StepKeyStockPlan }

func (StockPlanStep) Run(ctx context.Context, runner StepRunner) error {
	in := runner.RunInput()

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.plan: starting",
			zap.Int("explicit_clips", len(in.Clips)),
			zap.Int("search_queries", len(in.SearchQueries)),
			zap.Int("direct_urls", len(in.DirectURLs)))
	}

	// When explicit clips are provided, bypass the deterministic
	// planner and use the timestamp ranges directly. The first
	// per-clip URL is used for the VideoSource; if no clip has a
	// URL, the orchestrator falls back to the root source
	// (DirectURLs > DriveURLs > SearchQueries).
	//
	// Clips with zero-valued StartSec/EndSec are normalised:
	// EndSec defaults to clip_duration (10s if unset) so the
	// cutter gets a valid non-zero range instead of passing
	// Start=0 End=0 to ffmpeg (which would fail with
	// "-to value smaller than -ss").
	if len(in.Clips) > 0 {
		srcURL := ""
		for _, clip := range in.Clips {
			if clip.URL != "" {
				srcURL = clip.URL
				break
			}
		}
		if srcURL == "" {
			if fallback, ok := firstSource(in); ok {
				srcURL = fallback.URL
			} else {
				return errors.New("orchestrator: stock.plan: explicit clips require either a per-clip URL or a root source (direct_urls/drive_urls/search_queries)")
			}
		}

		// Normalise zero-valued timestamp ranges before handing
		// off to the explicit planner. Default clip duration is
		// taken from RunInput (set by the handler); defensive
		// fallback of 10s when unset.
		//
		// Two cases:
		//   Start=0 End=0  → URL-only clip, default to [0, clipDur)
		//   Start>0 End=0  → start provided but no end; default
		//                     to [start, start+clipDur)
		//   Otherwise       → explicit range, keep as-is
		clipDur := in.ClipDuration
		if clipDur <= 0 {
			clipDur = 10
		}
		normalised := make([]ClipSpec, len(in.Clips))
		for i, clip := range in.Clips {
			normalised[i] = clip
			if clip.EndSec == 0 {
				if clip.StartSec == 0 {
					normalised[i].EndSec = float64(clipDur)
				} else {
					normalised[i].EndSec = clip.StartSec + float64(clipDur)
				}
			}
		}
		expanded := expandExplicitClipSpecs(normalised, in.SecondsPerSegment)

		src := VideoSource{URL: srcURL, Title: in.Clips[0].Title, Source: srcURL}
		explicit := NewExplicitPlanner(expanded)
		plans, err := explicit.Plan(ctx, src, 0, 0, runner.Cfg().PolicyVersion)
		if err != nil {
			return fmt.Errorf("orchestrator: stock.plan: explicit planner: %w", err)
		}
		runner.State().Plan = plans
		if in.DownloadMode == "sections_only" {
			for i := range runner.State().Plan {
				runner.State().Plan[i].StageKey = runner.State().Plan[i].OutputLogicalID
			}
		}

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.plan: SUCCEEDED (explicit clips)",
				zap.Int("plan_count", len(plans)))
		}
		return nil
	}

	sources := concreteSources(in)
	if len(sources) == 0 {
		return errors.New("orchestrator: stock.plan: no sources to plan (DirectURLs, DriveURLs, and SearchQueries are empty)")
	}

	planBudget := in.TotalMinutes * 60
	clipDuration := runner.Cfg().ClipDurationSec
	if in.TargetDurationPerSourceSeconds > 0 {
		planBudget = in.TargetDurationPerSourceSeconds
	}
	if in.ClipDurationSeconds > 0 {
		clipDuration = in.ClipDurationSeconds
	}
	if planBudget <= 0 {
		planBudget = runner.Cfg().ChunkDurationSec
	}
	if in.ClipsPerSource > 0 && planBudget != in.ClipsPerSource*clipDuration {
		return fmt.Errorf("orchestrator: stock.plan: duration contract mismatch: per_source=%d clips=%d clip_duration=%d", planBudget, in.ClipsPerSource, clipDuration)
	}
	if in.TargetTotalDurationSeconds > 0 && in.TargetTotalDurationSeconds != planBudget*len(sources) {
		return fmt.Errorf("orchestrator: stock.plan: total duration contract mismatch: total=%d expected=%d for %d sources", in.TargetTotalDurationSeconds, planBudget*len(sources), len(sources))
	}
	allPlans := make([]ClipPlan, 0, len(sources))
	for _, src := range sources {
		plans, err := runner.Planner().Plan(
			ctx, src, planBudget,
			clipDuration, runner.Cfg().PolicyVersion,
		)
		if err != nil {
			return fmt.Errorf("orchestrator: stock.plan: planner.Plan source %q: %w", src.URL, err)
		}
		allPlans = append(allPlans, plans...)
	}
	applyRunMetadataToPlans(allPlans, in.Metadata)
	if in.DownloadMode == "sections_only" {
		for i := range allPlans {
			allPlans[i].StageKey = allPlans[i].OutputLogicalID
		}
	}
	runner.State().Plan = allPlans
	if in.ClipsPerSource > 0 {
		for _, src := range sources {
			count := 0
			var total float64
			for _, plan := range allPlans {
				if plan.SourceID == src.URL {
					count++
					total += plan.EndSec - plan.StartSec
				}
			}
			if count != in.ClipsPerSource || int(total+0.0001) != planBudget {
				return fmt.Errorf("orchestrator: stock.plan: source %q violates duration contract: clips=%d duration=%.3f", src.URL, count, total)
			}
		}
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.plan: SUCCEEDED",
			zap.Int("plan_count", len(allPlans)),
			zap.Int("source_count", len(sources)))
	}
	return nil
}

// applyRunMetadataToPlans carries the operator's stock identity into every
// planned clip. Direct YouTube URLs do not have ClipSpec metadata, but the
// resulting assets still need searchable title/category/tags so a query such
// as "Mike Tyson" does not see anonymous clip_### rows.
func applyRunMetadataToPlans(plans []ClipPlan, metadata *ChunkMetadataInput) {
	if metadata == nil {
		return
	}
	blockDescription := strings.TrimSpace(metadata.BlockDescription)
	if blockDescription == "" {
		blockDescription = strings.TrimSpace(metadata.Description)
	}
	for i := range plans {
		if strings.TrimSpace(plans[i].Title) == "" {
			plans[i].Title = metadata.Title
		}
		if strings.TrimSpace(plans[i].Description) == "" {
			plans[i].Description = blockDescription
		}
		if len(plans[i].Tags) == 0 {
			plans[i].Tags = append([]string(nil), metadata.Tags...)
		}
		if strings.TrimSpace(plans[i].Category) == "" {
			plans[i].Category = metadata.Category
		}
	}
}

// concreteSources returns every source that can be planned independently.
// DirectURLs are the canonical multi-source path after search queries have
// been resolved. DriveURLs and raw SearchQueries remain single-source legacy
// fallbacks until their acquisition adapters project concrete URLs.
func concreteSources(input *RunInput) []VideoSource {
	if input == nil {
		return nil
	}
	if len(input.DirectURLs) > 0 {
		sources := make([]VideoSource, 0, len(input.DirectURLs))
		for _, raw := range input.DirectURLs {
			if raw == "" {
				continue
			}
			// Carry the provider-known duration (populated by
			// resolveInputQueries) so the deterministic planner uses
			// the real source length instead of the budget*10
			// fallback. Unknown durations remain zero and keep the
			// planner's conservative fallback + extract-time bounds
			// check contract.
			duration := float64(0)
			if input.SourceDurations != nil {
				duration = input.SourceDurations[raw]
			}
			sources = append(sources, VideoSource{URL: raw, Title: raw, Source: raw, DurationSec: duration})
		}
		return sources
	}
	if src, ok := firstSource(input); ok {
		return []VideoSource{src}
	}
	return nil
}

func expandExplicitClipSpecs(clips []ClipSpec, secondsPerSegment int) []ClipSpec {
	if len(clips) == 0 {
		return append([]ClipSpec(nil), clips...)
	}
	expanded := make([]ClipSpec, 0, len(clips))
	for _, clip := range clips {
		stepSec := secondsPerSegment
		if stepSec <= 0 && clip.EndSec > clip.StartSec {
			if clip.EndSec-clip.StartSec >= explicitClipAutoSegmentThresholdSec {
				stepSec = explicitClipAutoSegmentSeconds
			}
		}
		if stepSec <= 0 || clip.EndSec <= clip.StartSec {
			expanded = append(expanded, clip)
			continue
		}
		step := float64(stepSec)
		for cursor := clip.StartSec; cursor < clip.EndSec; cursor += step {
			next := cursor + step
			if next > clip.EndSec {
				next = clip.EndSec
			}
			child := clip
			child.ParentSlug = clip.ParentSlug
			if child.ParentSlug == "" {
				child.ParentSlug = clip.Slug
			}
			child.StartSec = cursor
			child.EndSec = next
			if clip.Slug != "" {
				child.Slug = fmt.Sprintf("%s-%s_to_%s", clip.Slug, clipTimestamp(cursor), clipTimestamp(next))
			}
			expanded = append(expanded, child)
		}
	}
	return expanded
}

func clipTimestamp(seconds float64) string {
	total := int(seconds)
	return fmt.Sprintf("%d-%d-%d", total/3600, (total%3600)/60, total%60)
}
