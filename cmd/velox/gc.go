package main

// gc.go — reclaim the disk the pipeline accumulates.
//
// Why (2026-11-23): the host was at 98% (7 GB free) with 5,0 GB of
// final-job-fragments, 4,6 GB of tmp and 5,5 GB of cas. Every run needs room
// for the fragments it assembles plus the returned MP4, so a full volume fails
// late and opaquely. Nothing here touches the media catalog, the job ledger or
// the SQLite stores: only regenerable scratch directories.
//
// Dry-run by default: an operator reads the reclaimable total first, then
// re-runs with --apply. Entries newer than the cutoff are never touched,
// because a running job may still own them.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type gcTarget struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	Entries       int    `json:"entries"`
	ReclaimBytes  int64  `json:"reclaim_bytes"`
	KeptEntries   int    `json:"kept_entries"`
	KeptBytes     int64  `json:"kept_bytes"`
	Enabled       bool   `json:"enabled"`
	IncludeByFlag bool   `json:"include_by_flag,omitempty"`
}

type gcReport struct {
	DataDir     string     `json:"data_dir"`
	OlderThan   string     `json:"older_than"`
	Cutoff      string     `json:"cutoff"`
	Applied     bool       `json:"applied"`
	Targets     []gcTarget `json:"targets"`
	TotalBytes  int64      `json:"total_bytes"`
	TotalFree   uint64     `json:"total_free_bytes"`
	TotalErrors []string   `json:"errors,omitempty"`
}

func cmdGC(args []string) int {
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox gc: %v\n", err)
		return exitUsage
	}
	olderThan, err := time.ParseDuration(fs.get("older-than", "168h"))
	if err != nil || olderThan <= 0 {
		fmt.Fprintf(os.Stderr, "velox gc: invalid --older-than %q (use a duration like 168h)\n", fs.get("older-than", ""))
		return exitUsage
	}
	dataDir := strings.TrimSpace(fs.get("data-dir", ""))
	if dataDir == "" {
		dataDir = defaultPreflightDataDir()
	}
	info, statErr := os.Stat(dataDir)
	if statErr != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "velox gc: data dir not found: %s\n", dataDir)
		return exitFailure
	}

	targets := []struct {
		name    string
		path    string
		enabled bool
	}{
		{"final-job-fragments", filepath.Join(dataDir, "final-job-fragments"), true},
		{"tmp", filepath.Join(dataDir, "tmp"), true},
		// The CAS is a cache of rendered bytes: an old entry may still be the
		// only copy a future run reuses, so deleting it is opt-in.
		{"cas", filepath.Join(dataDir, "cas"), fs.has("include-cas")},
	}

	cutoff := time.Now().Add(-olderThan)
	apply := fs.has("apply")
	report := gcReport{
		DataDir: dataDir, OlderThan: olderThan.String(),
		Cutoff: cutoff.UTC().Format(time.RFC3339), Applied: apply,
	}
	for _, target := range targets {
		entry := gcTarget{Name: target.name, Path: target.path, Enabled: target.enabled, IncludeByFlag: target.name == "cas"}
		if !target.enabled {
			report.Targets = append(report.Targets, entry)
			continue
		}
		if err := sweepGC(target.path, cutoff, apply, &entry); err != nil {
			report.TotalErrors = append(report.TotalErrors, target.name+": "+err.Error())
		}
		report.TotalBytes += entry.ReclaimBytes
		report.Targets = append(report.Targets, entry)
	}
	report.TotalFree = checkDisk(dataDir, 0).FreeBytes

	if fs.has("json") {
		return emitJSON(report)
	}
	mode := "dry-run"
	if apply {
		mode = "APPLIED"
	}
	fmt.Printf("velox gc (%s) — %s, older than %s (cutoff %s)\n", mode, dataDir, olderThan, report.Cutoff)
	for _, target := range report.Targets {
		if !target.Enabled {
			fmt.Printf("  %-20s skipped (--include-cas)\n", target.Name)
			continue
		}
		fmt.Printf("  %-20s reclaim %8.2f GiB in %d entr%s; keep %8.2f GiB in %d entr%s\n",
			target.Name,
			float64(target.ReclaimBytes)/(1<<30), target.Entries, plural(target.Entries, "y", "ies"),
			float64(target.KeptBytes)/(1<<30), target.KeptEntries, plural(target.KeptEntries, "y", "ies"))
	}
	fmt.Printf("  total reclaimable: %.2f GiB\n", float64(report.TotalBytes)/(1<<30))
	if !apply && report.TotalBytes > 0 {
		fmt.Println("  re-run with --apply to delete")
	}
	for _, problem := range report.TotalErrors {
		fmt.Fprintf(os.Stderr, "  warn: %s\n", problem)
	}
	if len(report.TotalErrors) > 0 && apply {
		return exitFailure
	}
	return exitOK
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// sweepGC removes (or measures) the entries of one scratch directory whose
// modification time is older than the cutoff. Only top-level entries are
// considered: a fragment directory is owned as a whole, so a partially-written
// job is either old (safe) or new (kept).
func sweepGC(root string, cutoff time.Time, apply bool, out *gcTarget) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		size := entrySize(filepath.Join(root, entry.Name()))
		if info.ModTime().After(cutoff) {
			out.KeptEntries++
			out.KeptBytes += size
			continue
		}
		out.Entries++
		out.ReclaimBytes += size
		if !apply {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("remove %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// entrySize sums the regular files below path. Symlinks are not followed: a
// link inside the scratch tree must never make the sweep report or delete
// something outside it.
func entrySize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
