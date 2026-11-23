package gc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeAgedFile(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestRunDryRunDoesNotDeleteAndApplyPrunes(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	oldDir := filepath.Join(root, "job-old")
	old := filepath.Join(oldDir, "clip.mp4")
	fresh := filepath.Join(root, "job-fresh", "clip.mp4")
	writeAgedFile(t, old, now.Add(-72*time.Hour))
	writeAgedFile(t, fresh, now.Add(-time.Hour))

	cfg := Config{
		Roots: []Root{{Name: "staging", Path: root, Retention: 48 * time.Hour}},
		Now:   now,
	}
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(report.Roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(report.Roots))
	}
	rr := report.Roots[0]
	if rr.Files != 2 || rr.EligibleFiles != 1 || rr.DeletedFiles != 0 {
		t.Fatalf("dry-run report = files:%d eligible:%d deleted:%d", rr.Files, rr.EligibleFiles, rr.DeletedFiles)
	}
	if report.Applied {
		t.Fatal("dry run must not report Applied")
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("dry run deleted the old file: %v", err)
	}

	cfg.Apply = true
	report, err = Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	rr = report.Roots[0]
	if rr.DeletedFiles != 1 || rr.DeletedBytes == 0 {
		t.Fatalf("apply report = deleted:%d bytes:%d", rr.DeletedFiles, rr.DeletedBytes)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old file still present: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("empty directory still present: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh file was removed: %v", err)
	}
}

type fakePruner struct {
	dryRun bool
	calls  int
	rows   int64
}

func (f *fakePruner) PruneOlderThan(_ context.Context, _ time.Time, dryRun bool) (int64, error) {
	f.calls++
	f.dryRun = dryRun
	return f.rows, nil
}

func TestRunCacheRetentionPassesDryRunFlag(t *testing.T) {
	pruner := &fakePruner{rows: 7}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	report, err := Run(context.Background(), Config{
		Roots:          []Root{{Name: "staging", Path: t.TempDir(), Retention: time.Hour}},
		Cache:          pruner,
		CacheRetention: 30 * 24 * time.Hour,
		Now:            now,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !pruner.dryRun || pruner.calls != 1 || report.CacheRows != 7 {
		t.Fatalf("cache pruner dryRun=%v calls=%d rows=%d", pruner.dryRun, pruner.calls, report.CacheRows)
	}

	_, err = Run(context.Background(), Config{
		Roots:          []Root{{Name: "staging", Path: t.TempDir(), Retention: time.Hour}},
		Cache:          pruner,
		CacheRetention: 30 * 24 * time.Hour,
		Now:            now,
		Apply:          true,
	})
	if err != nil {
		t.Fatalf("Run apply: %v", err)
	}
	if pruner.dryRun {
		t.Fatal("apply must pass dryRun=false")
	}
}

func TestRunRejectsInvalidRetention(t *testing.T) {
	if _, err := Run(context.Background(), Config{Roots: []Root{{Name: "staging", Path: t.TempDir()}}}); err == nil {
		t.Fatal("zero retention must be rejected")
	}
}
