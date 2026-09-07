package stockpipeline

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStockExtractClips_CanonicalStateConstraints is the migration-189
// regression guard (July 2026). Migration 189 installed SQLite triggers
// (trg_media_assets_state_valid_insert/update) that ABORT any
// media_assets INSERT/UPDATE carrying a lifecycle_state outside the
// canonical allowlist — the zero value "" aborts, which surfaced in
// production as stock jobs failing with "atomic dispatch failed: outbox
// write aborted mid-transaction".
//
// The write produced by buildRichStockAsset must therefore carry:
//   - MediaType == "video" (canonical stock clip type)
//   - LifecycleState == StatePublished for non-youtube sources,
//     StateActive for youtube-sourced clips (mirrors the canonical
//     finalizer convention in asset_finalizer_committer.go)
//   - CreatedAt stamped non-zero (UpsertClipTx persists clip.CreatedAt
//     verbatim; a zero time writes an empty created_at column that
//     breaks time-bucketed queries and recency ordering)
func TestStockExtractClips_CanonicalStateConstraints(t *testing.T) {
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.mp4")
	outputPath := filepath.Join(tmpDir, "clip.mp4")
	for _, p := range []string{sourcePath, outputPath} {
		if err := os.WriteFile(p, []byte("fake-state-bytes-"+p), 0o644); err != nil {
			t.Fatalf("seed file %s: %v", p, err)
		}
	}

	// Three plans: youtube-sourced (direct URL), a named non-youtube
	// provider, and an empty-provider plan (the canonical "stock"
	// fallback).
	plans := []ClipPlan{
		{
			SourceID:        "https://www.youtube.com/watch?v=abc123",
			SourceProvider:  SourceProviderYouTube,
			SourceVideoID:   "abc123",
			OutputLogicalID: "planner:state:yt:0",
			StartSec:        0,
			EndSec:          5,
			Title:           "YT clip",
			PolicyVersion:   "test-policy-v1",
		},
		{
			SourceID:        "https://cdn.example.com/stock.mp4",
			SourceProvider:  "pexels",
			OutputLogicalID: "planner:state:stock:0",
			StartSec:        0,
			EndSec:          5,
			Title:           "Stock clip",
			PolicyVersion:   "test-policy-v1",
		},
		{
			SourceID:        "https://cdn2.example.com/empty.mp4",
			SourceProvider:  "",
			OutputLogicalID: "planner:state:empty:0",
			StartSec:        0,
			EndSec:          5,
			Title:           "Fallback clip",
			PolicyVersion:   "test-policy-v1",
		},
	}

	cutter := &batchRecordingCutter{}
	writer := &recordingWriter{}

	state := &RunState{
		Plan: plans,
		StagedAssets: []*assets.StagedAsset{
			{SourceID: plans[0].SourceID, LocalPath: sourcePath, DurationSec: 60},
			{SourceID: plans[1].SourceID, LocalPath: sourcePath, DurationSec: 60},
			{SourceID: plans[2].SourceID, LocalPath: sourcePath, DurationSec: 60},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{
				{URL: plans[0].SourceID, StartSec: 0, EndSec: 5},
				{URL: plans[1].SourceID, StartSec: 0, EndSec: 5},
				{URL: plans[2].SourceID, StartSec: 0, EndSec: 5},
			},
			ClipDuration: 5,
			TotalMinutes: 1,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: "test-policy-v1",
		},
		state: state,
	}
	runner := &extractClipsFakeRunner{
		fakeStepRunner: base,
		writer:         writer,
		cutter:         cutter,
	}

	step := StockExtractClipsStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("step.Run: unexpected error: %v", err)
	}

	if writer.calls != len(plans) {
		t.Fatalf("writer.calls = %d, want %d", writer.calls, len(plans))
	}

	// youtube-sourced clip → ACTIVE (canonical finalizer convention).
	yt := writer.byLogicalID("planner:state:yt:0")
	if yt == nil {
		t.Fatal("youtube clip was not written")
	}
	if got := string(yt.MediaType); got != "video" {
		t.Errorf("youtube clip MediaType = %q, want %q (migration 189 canonical state write)", got, "video")
	}
	if yt.LifecycleState != asset.StateActive {
		t.Errorf("youtube clip LifecycleState = %q, want %q (canonical finalizer convention: youtube → ACTIVE)", yt.LifecycleState, asset.StateActive)
	}
	if yt.CreatedAt.IsZero() {
		t.Error("youtube clip CreatedAt is zero — UpsertClipTx persists CreatedAt verbatim and an empty created_at breaks time-bucketed queries")
	}
	if got := string(yt.Source); got != "youtube" {
		t.Errorf("youtube clip Source = %q, want %q (provider identity preserved for direct URLs)", got, "youtube")
	}

	// named non-youtube provider (pexels) → PUBLISHED.
	stock := writer.byLogicalID("planner:state:stock:0")
	if stock == nil {
		t.Fatal("stock clip was not written")
	}
	if got := string(stock.MediaType); got != "video" {
		t.Errorf("stock clip MediaType = %q, want %q", got, "video")
	}
	if stock.LifecycleState != asset.StatePublished {
		t.Errorf("stock clip LifecycleState = %q, want %q (non-youtube → PUBLISHED)", stock.LifecycleState, asset.StatePublished)
	}
	if stock.CreatedAt.IsZero() {
		t.Error("stock clip CreatedAt is zero")
	}
	if got := string(stock.Source); got != "pexels" {
		t.Errorf("stock clip Source = %q, want %q (provider identity preserved)", got, "pexels")
	}

	// empty-provider plan → canonical "stock" source fallback + PUBLISHED.
	fallback := writer.byLogicalID("planner:state:empty:0")
	if fallback == nil {
		t.Fatal("fallback clip was not written")
	}
	if got := string(fallback.Source); got != "stock" {
		t.Errorf("empty-provider clip Source = %q, want %q (canonical fallback)", got, "stock")
	}
	if fallback.LifecycleState != asset.StatePublished {
		t.Errorf("empty-provider clip LifecycleState = %q, want %q (non-youtube → PUBLISHED)", fallback.LifecycleState, asset.StatePublished)
	}
	if fallback.CreatedAt.IsZero() {
		t.Error("empty-provider clip CreatedAt is zero")
	}
}

// TestStockExtractClips_UploadWorkerPoolLimitsConcurrency asserts that
// artifact uploads are performed by a bounded worker pool (max 3
// concurrent Prepare calls per source group) and that the resulting
// chunks remain ordered by clip index with deterministic clip_001.mp4
// filenames.
func TestStockExtractClips_UploadWorkerPoolLimitsConcurrency(t *testing.T) {
	tmpDir := t.TempDir()

	sourcePath := filepath.Join(tmpDir, "source.mp4")
	if err := os.WriteFile(sourcePath, []byte("fake-source-bytes"), 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}

	// 5 clips on the same source so the worker pool has work to do.
	plans := []ClipPlan{
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:0", StartSec: 0, EndSec: 5, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:1", StartSec: 5, EndSec: 10, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:2", StartSec: 10, EndSec: 15, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:3", StartSec: 15, EndSec: 20, PolicyVersion: "test-policy-v1"},
		{SourceID: "yt-upload-pool", OutputLogicalID: "planner:upload:4", StartSec: 20, EndSec: 25, PolicyVersion: "test-policy-v1"},
	}

	prep := &concurrencyTrackingArtifactPrep{delay: 50 * time.Millisecond}
	cutter := &batchRecordingCutter{}
	writer := &recordingWriter{}

	state := &RunState{
		Plan: plans,
		StagedAssets: []*assets.StagedAsset{
			{SourceID: "yt-upload-pool", LocalPath: sourcePath, DurationSec: 60},
		},
	}

	base := &fakeStepRunner{
		runInput: &RunInput{
			Clips: []ClipSpec{
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 0, EndSec: 5},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 5, EndSec: 10},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 10, EndSec: 15},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 15, EndSec: 20},
				{URL: "https://www.youtube.com/watch?v=upload-pool", StartSec: 20, EndSec: 25},
			},
			ClipDuration: 5,
			TotalMinutes: 1,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: "test-policy-v1",
		},
		state: state,
	}
	runner := &extractClipsFakeRunner{
		fakeStepRunner: base,
		writer:         writer,
		cutter:         cutter,
		artifactPrep:   prep,
	}

	step := StockExtractClipsStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("step.Run: unexpected error: %v", err)
	}

	// Assert: all clips were uploaded plus one metadata.json for the
	// single timestamp group (5 clips + 1 metadata = 6 Prepare calls).
	wantCalls := len(plans) + 1
	if prep.calls != wantCalls {
		t.Errorf("Prepare calls = %d, want %d", prep.calls, wantCalls)
	}

	// Assert: concurrency never exceeded 3 and the pool actually
	// parallelized work (max concurrent must be exactly 3 when there
	// are more than 3 clips to upload).
	if prep.maxConcurrent > 3 {
		t.Errorf("max concurrent uploads = %d, want <= 3", prep.maxConcurrent)
	}
	if prep.maxConcurrent < 3 {
		t.Errorf("max concurrent uploads = %d, want 3 (pool did not parallelize)", prep.maxConcurrent)
	}

	// Assert: published chunks are in clip-index order and use the
	// deterministic clip_001.mp4, clip_002.mp4, ... filenames.
	published := runner.State().Published
	if len(published) != len(plans) {
		t.Fatalf("published chunks = %d, want %d", len(published), len(plans))
	}
	for i, chunk := range published {
		wantFilename := fmt.Sprintf("clip_%03d.mp4", i+1)
		if chunk.Filename != wantFilename {
			t.Errorf("published[%d].Filename = %q, want %q", i, chunk.Filename, wantFilename)
		}
		if chunk.Index != i {
			t.Errorf("published[%d].Index = %d, want %d", i, chunk.Index, i)
		}
	}
}
