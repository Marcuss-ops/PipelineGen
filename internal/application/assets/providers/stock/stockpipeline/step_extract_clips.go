// Package stockpipeline — step_extract_clips.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockExtractClipsStep — the canonical
// implementation of the stock.extract_clips step (Step 3 of the
// 6-step pipeline) per godlike/06 SSOT. Phase 1 (July 2026):
// rewired to use the real VideoCutter.Cut port instead of
// emitting logical IDs.
//
// The step:
//  1. Builds a sourceID → localPath map from StagedAssets.
//  2. Groups ClipPlan entries by SourceID.
//  3. For each group, constructs CutRequest with the real
//     SourcePath and calls runner.Cutter().Cut(ctx, req).
//  4. Collects OutputPath values from SuccessfulItems() →
//     CutPaths.
//  5. Writes asset/outbox via Writer.WriteAndEnqueue for each
//     successfully cut clip.
//
// godlike/07 fail-closed contracts:
//   - Cutter nil + plans empty → test-fixture path (CutPaths = nil,
//     no error).
//   - Cutter nil + plans non-empty → ErrStockExtractClipsCutterRequired.
//   - plans empty → Debug + return nil (no work to do).
//   - Source not staged → Warn + skip (graceful degradation; other
//     sources may still have staged files).
//   - All cuts fail for a source → error (terminal, typed wrap
//     preserving cutter typed sentinel via %w).
//   - Zero cut files across all sources → terminal error
//     (production gate, closes false-success class).
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ErrStockClipsOutOfRange (PR-STOCK-TIMESTAMP-CLIPS Front 5,
// July 2026) surfaces a clip whose EndSec exceeds the probed
// source duration. The step validates every ClipPlan in the
// group BEFORE invoking VideoCutter.Cut so a malformed timestamp
// fails fast with a typed error instead of silently producing a
// half-broken artifact via ffmpeg. Per user spec literal
// "fallire subito con errore leggibile" — no auto-clamp, no
// truncation, the operator must fix the input. Wrap the typed
// sentinel via fmt.Errorf("...: %w", ErrStockClipsOutOfRange) so
// callers can errors.Is(err, ErrStockClipsOutOfRange) without
// parsing the human-readable prefix.
var ErrStockClipsOutOfRange = errors.New("stock.extract_clips: clip EndSec exceeds source duration — input out of range (PR-STOCK-TIMESTAMP-CLIPS Front 5, godlike/07 NO-FAKE-AVAILABILITY)")

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips. Phase 1 (July 2026): rewired to use the
// real VideoCutter.Cut port instead of emitting logical IDs.
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return StepKeyStockExtractClips }

func (StockExtractClipsStep) Run(ctx context.Context, runner StepRunner) error {
	cutter := runner.Cutter()
	plans := runner.State().Plan

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: starting",
			zap.Int("plan_count", len(plans)),
			zap.Int("staged_sources", len(runner.State().StagedAssets)))
	}

	// Test-fixture path: no cutter wired → skip (downstream
	// compose_chunks handles empty CutPaths gracefully).
	//
	// godlike/07 fail-closed gate (PR-STOCK-FAKE-AVAILABILITY-REMOVAL
	// follow-up, July 2026): when plans is non-empty (the step has
	// work to do) BUT cutter is nil, surface ErrStockExtractClipsCutterRequired
	// instead of silently returning CutPaths=nil. A nil cutter with
	// zero plans is still a valid test-fixture skip (empty plans →
	// zero cutPaths is the correct outcome).
	if cutter == nil {
		if len(plans) > 0 {
			return ErrStockExtractClipsCutterRequired
		}
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: VideoCutter nil + empty plan — skipping cut (test-fixture path)")
		}
		runner.State().CutPaths = nil
		return nil
	}

	if len(plans) == 0 {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: empty plan — nothing to extract")
		}
		runner.State().CutPaths = nil
		return nil
	}

	// Build sourceID → *StagedAsset map from StagedAssets. We
	// keep the full pointer (not just LocalPath) so the pre-cut
	// probe in the loop can read DurationSec (PR-STOCK-TIMESTAMP-CLIPS
	// Front 5, July 2026) without a second iteration.
	stagedBySource := make(map[string]*assets.StagedAsset)
	for _, sa := range runner.State().StagedAssets {
		if sa.SourceID != "" && sa.LocalPath != "" {
			sa := sa
			stagedBySource[sa.SourceID] = sa
		}
	}

	// Group ClipPlan by SourceID.
	grouped := make(map[string][]ClipPlan)
	for _, plan := range plans {
		grouped[plan.SourceID] = append(grouped[plan.SourceID], plan)
	}

	in := runner.RunInput()
	noAudio := in != nil && in.NoAudio
	writer := runner.Writer()

	var cutPaths []string
	sourceIdx := 0

	for sourceID, groupPlans := range grouped {
		staged := stagedBySource[sourceID]
		if staged == nil {
			// Source not staged — skip gracefully. The upstream
			// stock.stage_sources step logs Warn on stage failure;
			// here we surface the downstream impact without aborting
			// (other sources may still have staged files).
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: source not staged — skipping cuts",
					zap.String("source_id", sourceID),
					zap.Int("clip_count", len(groupPlans)))
			}
			sourceIdx++
			continue
		}
		sourcePath := staged.LocalPath

		// PR-STOCK-TIMESTAMP-CLIPS Front 5 (July 2026): pre-cut
		// duration validation. The step probes the source duration
		// BEFORE invoking VideoCutter.Cut so a clip with EndSec >
		// duration fails fast with a typed sentinel (no auto-clamp
		// per user spec "fallire subito con errore leggibile").
		// Source of truth priority (godlike/06 SSOT):
		//   1. StagedAsset.DurationSec — populated upstream by
		//      stock.stage_sources when known (yt-dlp
		//      --print-duration at staging time). Fast path; no
		//      subprocess call.
		//   2. runner.SourceDurationProbe().ProbeDurationSec() —
		//      ffprobe-backed probe for legacy composition roots
		//      that don't pre-populate DurationSec. Optional;
		//      nil ⇒ skip validation (godlike/07 fail-open
		//      backward-compat; PR-STOCK-SOURCE-DURATION-WIRE
		//      is the forward-pointer for production wiring).
		//   3. Neither available — log Warn + skip validation
		//      (legacy unvalidated path; same backward-compat as
		//      nil probe).
		var durationSec float64
		durationKnown := false
		if staged.DurationSec > 0 {
			durationSec = staged.DurationSec
			durationKnown = true
		} else if probe := runner.SourceDurationProbe(); probe != nil {
			d, probeErr := probe.ProbeDurationSec(ctx, sourcePath)
			if probeErr != nil {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: duration probe failed — skipping bounds check (godlike/07 fail-open)",
						zap.String("source_id", sourceID),
						zap.String("source_path", sourcePath),
						zap.Error(probeErr))
				}
			} else if d > 0 {
				durationSec = d
				durationKnown = true
			}
		} else if runner.Log() != nil {
			// godlike/07 fail-open but loudly: a missing
			// probe AND no DurationSec is a composition-root
			// misconfiguration. Warn (not Debug) so operators
			// notice out-of-range clips are not being caught.
			// Forward-pointer PR-STOCK-SOURCE-DURATION-WIRE
			// closes this gap at the composition root.
			runner.Log().Warn("orchestrator: stock.extract_clips: no DurationSec + no SourceDurationProbe wired — bounds check skipped (out-of-range clips will NOT be caught; compose WithSourceProbe at the composition root)",
				zap.String("source_id", sourceID),
				zap.String("source_path", sourcePath),
				zap.Int("clip_count", len(groupPlans)))
		}

		// Bounds check (godlike/07 NO-FAKE-AVAILABILITY): every clip
		// in the group must have EndSec <= source duration. The
		// step counts out-of-range clips and returns the FIRST
		// violation with the typed sentinel; subsequent violations
		// are surfaced via the wrapped error message (Read-back
		// contract: errors.Is(err, ErrStockClipsOutOfRange) succeeds
		// regardless of which clip was the first violator).
		//
		// Boundary semantics: strict `>` (no epsilon) — EndSec ==
		// sourceDurationSec is treated as in-range. This matches
		// the user spec literal "fallire subito con errore leggibile"
		// (the boundary-equal case is a valid full-length cut, not
		// an out-of-range error). If a future PR needs sub-second
		// tolerance for floating-point drift, add an epsilon constant
		// at the top of this file and re-pin the test.
		if durationKnown {
			for clipIdx, plan := range groupPlans {
				if plan.EndSec > durationSec {
					if runner.Log() != nil {
						runner.Log().Error("orchestrator: stock.extract_clips: clip EndSec exceeds source duration — aborting (no auto-clamp per user spec)",
							zap.String("source_id", sourceID),
							zap.Int("clip_index", clipIdx),
							zap.String("artifact_id", plan.OutputLogicalID),
							zap.Float64("clip_end_sec", plan.EndSec),
							zap.Float64("source_duration_sec", durationSec),
							zap.Float64("overrun_sec", plan.EndSec-durationSec))
					}
					return fmt.Errorf("orchestrator: stock.extract_clips: clip[%d] (artifact=%s) EndSec=%.2f exceeds source duration=%.2f (overrun=%.2fs): %w",
						clipIdx, plan.OutputLogicalID, plan.EndSec, durationSec, plan.EndSec-durationSec, ErrStockClipsOutOfRange)
				}
			}
		}

		// Build CutJobs from ClipPlan entries. Each job gets a
		// unique output path via the per-clip index, avoiding the
		// collision where all clips in a source group shared the
		// same OutputLogicalID prefix.
		jobs := make([]CutJob, 0, len(groupPlans))
		for clipIdx, plan := range groupPlans {
			outputPath := filepath.Join(os.TempDir(),
				fmt.Sprintf("stock_cut_%s_%d_%d.mp4", runner.JobID(), sourceIdx, clipIdx))
			jobs = append(jobs, CutJob{
				StartSec:   plan.StartSec,
				EndSec:     plan.EndSec,
				OutputPath: outputPath,
			})
		}

		req := CutRequest{
			SourcePath: sourcePath,
			Jobs:       jobs,
			Codec:      "libx264",
			Preset:     "medium",
			CRF:        23,
			NoAudio:    noAudio,
			Logger:     runner.Log(),
			SourceIdx:  sourceIdx,
		}

		result, cutErr := cutter.Cut(ctx, req)

		// Process successful items. The port contract guarantees
		// len(Items) == len(req.Jobs) (mai-nil-with-zero-output
		// invariant); SuccessfulItems() filters to file-on-disk-
		// playable outcomes (Succeeded | Validated | ProbeFailed).
		for clipIdx, item := range result.SuccessfulItems() {
			// CutPaths carries REAL file paths (for compose_chunks).
			cutPaths = append(cutPaths, item.OutputPath)

			// Write asset/outbox for this successfully cut clip.
			// Asset ID uses the planner's OutputLogicalID (stable
			// across retries) so retry dedupe works; the real file
			// path is in CutPaths for downstream consumption.
			if writer != nil && clipIdx < len(groupPlans) {
				plan := groupPlans[clipIdx]

				// PR-STOCK-TIMESTAMP-CLIPS Front 4 (July 2026):
				// rich-asset write — compute SHA256 fail-closed
				// (P0 2.4 hardening: a malformed digest short-circuits
				// at the cut step rather than silently reaching the
				// indexer which would terminal-reject it), then build
				// the canonical 10-field asset via buildRichStockAsset
				// and pass the hash to WriteAndEnqueue (replaces the
				// prior "" literal).
				hash, hashErr := job.ComputeSHA256(item.OutputPath)
				if hashErr != nil {
					if runner.Log() != nil {
						runner.Log().Error("orchestrator: stock.extract_clips: SHA256 compute failed — aborting rich-asset write",
							zap.String("source_id", sourceID),
							zap.Int("clip_index", clipIdx),
							zap.String("output_path", item.OutputPath),
							zap.Error(hashErr))
					}
					return fmt.Errorf("orchestrator: stock.extract_clips: chunk %d (artifact=%s) SHA256 compute: %w",
						clipIdx, plan.OutputLogicalID, hashErr)
				}

				clip := buildRichStockAsset(plan, sourceIdx, clipIdx, item.OutputPath, hash)

				if err := writer.WriteAndEnqueue(ctx, clip, hash); err != nil {
					// PR-STOCK-ATLASTORCH-DISPATCH (commit-1 stock fix).
					// Pre-fix: the loop logged + continued on writer
					// error, leaving physical clip on disk but no
					// DB/outbox row — silent-success false-positive.
					// Post-fix: abort the run with the canonical
					// ErrAtomicDispatchFailed sentinel. Surfaced as
					// JobFailed by the broker; ledger writes roll back
					// via the single-TX outbox + SQLite Begin-Immediate
					// path.
					//
					// dual-%w (NEVER %v) preserves the underlying writer
					// error in the typed-error chain so callers can
					// errors.As-recover production causes (e.g.
					// outbox.ErrDispatcherClosed, sqlite3.ErrLocked).
					// Per AGENTS.md godlike/07 typed-error contract +
					// compat_adapters.go precedent — %v would silently
					// drop the chain.
					if runner.Log() != nil {
						runner.Log().Warn("orchestrator: stock.extract_clips: WriteAndEnqueue failed — aborting atomic dispatch",
							zap.String("logical_id", plan.OutputLogicalID),
							zap.String("output_path", item.OutputPath),
							zap.String("source_id", sourceID),
							zap.Int("clip_index", clipIdx),
							zap.Error(err))
					}
					return fmt.Errorf("orchestrator: stock.extract_clips: %w: %w", ErrAtomicDispatchFailed, err)
				}
			}
		}

		if cutErr != nil && len(result.SuccessfulItems()) == 0 {
			return fmt.Errorf("orchestrator: stock.extract_clips: VideoCutter.Cut failed for source %s with zero clips produced: %w",
				sourceID, cutErr)
		}

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.extract_clips: cut batch complete",
				zap.String("source_id", sourceID),
				zap.Int("planned", len(jobs)),
				zap.Int("produced", len(result.SuccessfulItems())))
		}

		sourceIdx++
	}

	// Production gate: cutter wired (non-nil) + zero cut files
	// across all sources → terminal error. This closes the
	// false-success class where extract_clips "succeeds" without
	// producing any real files on disk.
	if len(cutPaths) == 0 {
		return fmt.Errorf("orchestrator: stock.extract_clips: zero cut files produced across %d sources — all sources either unstaged or all cuts failed", len(grouped))
	}

	runner.State().CutPaths = cutPaths

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: SUCCEEDED",
			zap.Int("cut_paths", len(cutPaths)),
			zap.Int("sources_processed", sourceIdx))
	}
	return nil
}

// buildRichStockAsset composes the canonical 10-field asset.Asset
// for a stock-pipeline cut clip. PR-STOCK-TIMESTAMP-CLIPS Front 4
// (July 2026) replaces the prior 4-field literal with this rich
// surface so downstream consumers (Qdrant indexer, asset search,
// media_assets projection) see Title/Description/Round/Tags/Category/
// LocalPath/SHA256/StartSec/EndSec/SearchText at the row level
// instead of just in metadata.json emitted later.
//
// Field routing (godlike/06 SSOT one-canonical-owner-per-fact):
//   - Direct fields (asset.Asset top-level): ID, Name, Filename,
//     Source, MediaType, Category, SourceURL, Duration, Tags,
//     SearchText, LifecycleState.
//   - Metadata map (open-ended, canonical for fields with no
//     direct column): Title, Description, Round, StartSec, EndSec,
//     LocalPath, SHA256 (mirrored as file_hash), Slug.
//
// godlike/06 typed-accessor contract: SetLocalPath + SetFileHash
// are the canonical typed setters in asset_accessors.go — they
// populate BOTH the Metadata map AND any future typed columns
// when they land. Calling them keeps the wire-shape and the
// accessor surface in lockstep.
//
// godlike/07 minimum-blast-radius: sourceIdx + clipIdx are
// retained as parameters for the legacy chunk_%d_%d fallback
// slug (used only when perClipLeafName returns empty); they
// don't appear in the canonical slug derivation otherwise.
//
// forward-pointer PR-STOCK-RICHASSET-GROUP: the Group field is
// left empty today (the planner's per-clip group is the YouTube
// channel name, not a stock-derived concept). A future PR can
// inject runner.RunInput() into buildRichStockAsset and derive
// Group from stockRootFolderName(in) for consistency with
// stock.publish's per-chunk group semantics.
func buildRichStockAsset(plan ClipPlan, sourceIdx, clipIdx int, localPath, sha256 string) *asset.Asset {
	slug := perClipLeafName(plan)
	if strings.TrimSpace(slug) == "" {
		slug = fmt.Sprintf("chunk_%d_%d", sourceIdx, clipIdx)
	}

	durationSec := plan.EndSec - plan.StartSec
	if durationSec < 0 {
		durationSec = 0
	}

	clip := &asset.Asset{
		ID:             plan.OutputLogicalID,
		Name:           slug,
		Filename:       slug + ".mp4",
		Source:         asset.Source("stock"),
		MediaType:      asset.MediaType("video"),
		Category:       plan.Category,
		SourceURL:      plan.SourceID,
		Duration:       time.Duration(durationSec * float64(time.Second)),
		Tags:           append([]string(nil), plan.Tags...),
		SearchText:     composeStockChunkSearchText(plan),
		LifecycleState: asset.StateActive,
		Metadata: asset.Metadata{
			"title":       plan.Title,
			"description": plan.Description,
			"round":       plan.Round,
			"start_sec":   plan.StartSec,
			"end_sec":     plan.EndSec,
			"local_path":  localPath,
			"sha256":      sha256,
			"file_hash":   sha256,
			"slug":        slug,
		},
	}

	clip.SetLocalPath(localPath)
	clip.SetFileHash(sha256)

	return clip
}

// composeStockChunkSearchText composes the canonical Qdrant BM25
// channel text for a stock-pipeline cut clip. Format:
//
//	"Stock video clip | title: <title> | description: <description>
//	 | round <N> | category: <cat> | tags: <t1, t2, ...>
//	 | start_sec: <s> | end_sec: <e>"
//
// godlike/07 minimum-blast-radius: each labelled segment is dropped
// silently when its inputs are empty so the output never contains
// dangling " | title: " or " | tags: " fragments. godlike/06 SSOT:
// this is the SOLE canonical owner of the stock-chunk text format
// used at the asset-row write seam; the strategy function in
// internal/infrastructure/indexing/searchtext/strategies.go is
// the canonical owner for the downstream Qdrant-payload compose
// seam. The two surfaces produce byte-equivalent SearchText for
// the same input (modulo the " | " separator vs " " separator
// chosen at each seam).
//
// forward-pointer PR-STOCK-SEARCHTEXT-PORT: extract this inline
// composition into a port + composition-root wiring so the
// asset-row write and the Qdrant-payload write share a single
// canonical source. Today the duplication is acceptable per
// godlike/07 minimum-blast-radius (test surface locks both
// compositions to the same field set).
func composeStockChunkSearchText(plan ClipPlan) string {
	parts := []string{"Stock video clip"}

	if title := strings.TrimSpace(plan.Title); title != "" {
		parts = append(parts, "title: "+title)
	}
	if desc := strings.TrimSpace(plan.Description); desc != "" {
		parts = append(parts, "description: "+desc)
	}
	if plan.Round > 0 {
		parts = append(parts, "round "+strconv.Itoa(plan.Round))
	}
	if cat := strings.TrimSpace(plan.Category); cat != "" {
		parts = append(parts, "category: "+cat)
	}
	if len(plan.Tags) > 0 {
		// Filter empty tags to avoid dangling "tags: , , " fragments.
		var kept []string
		for _, t := range plan.Tags {
			if t = strings.TrimSpace(t); t != "" {
				kept = append(kept, t)
			}
		}
		if len(kept) > 0 {
			parts = append(parts, "tags: "+strings.Join(kept, ", "))
		}
	}

	parts = append(parts,
		"start_sec: "+strconv.FormatFloat(plan.StartSec, 'f', -1, 64),
		"end_sec: "+strconv.FormatFloat(plan.EndSec, 'f', -1, 64),
	)

	return strings.Join(parts, " | ")
}
