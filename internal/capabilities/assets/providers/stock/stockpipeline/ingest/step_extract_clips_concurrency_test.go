package assets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

type boundedSourceCutter struct {
	mu         sync.Mutex
	active     int
	maxActive  int
	calls      []string
	delay      time.Duration
	failSource string
	started    chan string
}

func (c *boundedSourceCutter) Cut(ctx context.Context, req CutRequest) (CutBatchResult, error) {
	c.mu.Lock()
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	c.calls = append(c.calls, req.SourcePath)
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active--
		c.mu.Unlock()
	}()

	if c.started != nil {
		select {
		case c.started <- req.SourcePath:
		default:
		}
	}
	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
		return CutBatchResult{SourcePath: req.SourcePath}, ctx.Err()
	}
	if c.failSource != "" && req.SourcePath == c.failSource {
		items := make([]CutItemResult, len(req.Jobs))
		for i, job := range req.Jobs {
			items[i] = CutItemResult{JobID: job.OutputPath, Status: CutItemStatusFailed, Err: errors.New("injected cut failure")}
		}
		return CutBatchResult{SourcePath: req.SourcePath, Items: items}, errors.New("injected source cut failure")
	}

	items := make([]CutItemResult, len(req.Jobs))
	for i, job := range req.Jobs {
		if err := os.WriteFile(job.OutputPath, []byte(req.SourcePath), 0o644); err != nil {
			return CutBatchResult{}, fmt.Errorf("write fake cut: %w", err)
		}
		items[i] = CutItemResult{
			JobID:       job.OutputPath,
			OutputPath:  job.OutputPath,
			Status:      CutItemStatusSucceeded,
			SizeBytes:   int64(len(req.SourcePath)),
			DurationSec: job.EndSec - job.StartSec,
		}
	}
	return CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
}

func (c *boundedSourceCutter) maxConcurrency() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxActive
}

func (c *boundedSourceCutter) callOrder() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

func newBoundedExtractionRunner(t *testing.T, plans []ClipPlan, staged []*assets.StagedAsset, cutter VideoCutter, maxConcurrent int) *extractClipsFakeRunner {
	t.Helper()
	return &extractClipsFakeRunner{
		fakeStepRunner: &fakeStepRunner{
			runInput: &RunInput{ClipDuration: 5, TotalMinutes: 1},
			cfg:      OrchestratorConfig{PolicyVersion: "test-policy-v1", MaxConcurrentJobs: maxConcurrent},
			state:    &RunState{Plan: plans, StagedAssets: staged},
		},
		cutter: cutter,
		writer: &recordingWriter{},
	}
}

func TestStockExtractClips_BoundedMultiSourceCutsPreservePlanOrder(t *testing.T) {
	tmpDir := t.TempDir()
	plans := make([]ClipPlan, 0, 3)
	staged := make([]*assets.StagedAsset, 0, 3)
	for _, source := range []string{"source-a", "source-b", "source-c"} {
		path := filepath.Join(tmpDir, source+".mp4")
		if err := os.WriteFile(path, []byte("source"), 0o644); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, ClipPlan{
			SourceID:        source,
			OutputLogicalID: "logical-" + source,
			StartSec:        0,
			EndSec:          5,
			PolicyVersion:   "test-policy-v1",
		})
		staged = append(staged, &assets.StagedAsset{SourceID: source, LocalPath: path, DurationSec: 30})
	}

	cutter := &boundedSourceCutter{delay: 40 * time.Millisecond}
	runner := newBoundedExtractionRunner(t, plans, staged, cutter, 2)
	writer := runner.writer.(*recordingWriter)

	if err := (StockExtractClipsStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := cutter.maxConcurrency(); got != 2 {
		t.Fatalf("max concurrent cuts = %d, want 2", got)
	}
	if got := len(cutter.callOrder()); got != len(plans) {
		t.Fatalf("cut calls = %d, want %d", got, len(plans))
	}
	if got := len(runner.State().CutPaths); got != len(plans) {
		t.Fatalf("CutPaths = %d, want %d", got, len(plans))
	}
	workspaceDir, err := filepath.Abs(filepath.Join("data", "stock", "workspaces", "test-job", "extracted"))
	if err != nil {
		t.Fatalf("resolve expected workspace: %v", err)
	}
	for i, path := range runner.State().CutPaths {
		want := filepath.Join(workspaceDir, fmt.Sprintf("stock_cut_test-job_%d_0.mp4", i))
		if path != want {
			t.Errorf("CutPaths[%d] = %q, want %q (stable plan-order source index)", i, path, want)
		}
	}
	if writer.calls != len(plans) {
		t.Fatalf("writer calls = %d, want %d", writer.calls, len(plans))
	}
}

func TestStockExtractClips_CutFailureCancelsFanout(t *testing.T) {
	tmpDir := t.TempDir()
	plans := []ClipPlan{
		{SourceID: "source-a", OutputLogicalID: "logical-a", StartSec: 0, EndSec: 5},
		{SourceID: "source-b", OutputLogicalID: "logical-b", StartSec: 0, EndSec: 5},
	}
	staged := make([]*assets.StagedAsset, 0, len(plans))
	for _, plan := range plans {
		path := filepath.Join(tmpDir, plan.SourceID+".mp4")
		if err := os.WriteFile(path, []byte("source"), 0o644); err != nil {
			t.Fatal(err)
		}
		staged = append(staged, &assets.StagedAsset{SourceID: plan.SourceID, LocalPath: path, DurationSec: 30})
	}
	cutter := &boundedSourceCutter{delay: 5 * time.Millisecond, failSource: staged[0].LocalPath}
	runner := newBoundedExtractionRunner(t, plans, staged, cutter, 2)

	err := (StockExtractClipsStep{}).Run(context.Background(), runner)
	if err == nil || !strings.Contains(err.Error(), "injected source cut failure") {
		t.Fatalf("Run error = %v, want injected source cut failure", err)
	}
	if len(runner.State().CutPaths) != 0 {
		t.Fatalf("CutPaths = %v after failed fanout, want empty state", runner.State().CutPaths)
	}
}

func TestStockExtractClips_ContextCancellationStopsFanout(t *testing.T) {
	tmpDir := t.TempDir()
	plans := []ClipPlan{{SourceID: "source-a", OutputLogicalID: "logical-a", StartSec: 0, EndSec: 5}}
	path := filepath.Join(tmpDir, "source-a.mp4")
	if err := os.WriteFile(path, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	cutter := &boundedSourceCutter{delay: time.Second}
	runner := newBoundedExtractionRunner(t, plans, []*assets.StagedAsset{{SourceID: "source-a", LocalPath: path, DurationSec: 30}}, cutter, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := (StockExtractClipsStep{}).Run(ctx, runner)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}
