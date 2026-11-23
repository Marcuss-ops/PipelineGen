// Package gc owns the bounded-retention janitor for clip.render local state.
// It never deletes media_assets or published artifacts: it only removes files
// under the staging/sequence roots and, when wired, stale cache rows.
package gc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrInvalidConfig is returned for a missing root/retention/cache contract.
var ErrInvalidConfig = errors.New("cliprender gc: invalid configuration")

// Root is one local filesystem root and its retention.
type Root struct {
	Name      string
	Path      string
	Retention time.Duration
}

// CachePruner is the optional cache retention port. dryRun counts without
// deleting; apply deletes and returns the number of removed rows.
type CachePruner interface {
	PruneOlderThan(ctx context.Context, cutoff time.Time, dryRun bool) (int64, error)
}

// Config is the janitor invocation.
type Config struct {
	Roots          []Root
	Cache          CachePruner
	CacheRetention time.Duration
	Resume         *ResumeConfig
	Now            time.Time
	Apply          bool
}

// RootReport is the bounded inventory and the action taken for one root.
type RootReport struct {
	Name          string     `json:"name"`
	Path          string     `json:"path"`
	Retention     string     `json:"retention"`
	Cutoff        time.Time  `json:"cutoff"`
	Files         int64      `json:"files"`
	Bytes         int64      `json:"bytes"`
	Oldest        *time.Time `json:"oldest,omitempty"`
	EligibleFiles int64      `json:"eligible_files"`
	EligibleBytes int64      `json:"eligible_bytes"`
	DeletedFiles  int64      `json:"deleted_files"`
	DeletedBytes  int64      `json:"deleted_bytes"`
	Errors        []string   `json:"errors,omitempty"`
}

// Report is the machine-readable janitor result. CacheRows is the number of
// eligible rows (or deleted rows when Apply=true).
type Report struct {
	Applied        bool          `json:"applied"`
	GeneratedAt    time.Time     `json:"generated_at"`
	Roots          []RootReport  `json:"roots"`
	CacheRetention string        `json:"cache_retention,omitempty"`
	CacheRows      int64         `json:"cache_rows"`
	Resume         *ResumeReport `json:"resume,omitempty"`
	Errors         []string      `json:"errors,omitempty"`
}

// Run scans every root, deletes eligible regular files only when Apply is
// true, then prunes empty directories bottom-up. It never follows symlinks.
func Run(ctx context.Context, cfg Config) (Report, error) {
	now := cfg.Now
	if now.IsZero() {
		now = time.Now()
	}
	report := Report{Applied: cfg.Apply, GeneratedAt: now.UTC()}
	if len(cfg.Roots) == 0 && cfg.Cache == nil && cfg.Resume == nil {
		return report, fmt.Errorf("%w: at least one root, cache pruner or resume pass is required", ErrInvalidConfig)
	}

	for _, root := range cfg.Roots {
		rr, err := scanRoot(ctx, root, now, cfg.Apply)
		if err != nil {
			return report, err
		}
		report.Roots = append(report.Roots, rr)
	}

	if cfg.Cache != nil {
		if cfg.CacheRetention <= 0 {
			return report, fmt.Errorf("%w: cache retention must be > 0", ErrInvalidConfig)
		}
		report.CacheRetention = cfg.CacheRetention.String()
		rows, err := cfg.Cache.PruneOlderThan(ctx, now.Add(-cfg.CacheRetention), !cfg.Apply)
		if err != nil {
			report.Errors = append(report.Errors, "cache: "+err.Error())
		} else {
			report.CacheRows = rows
		}
	}
	if cfg.Resume != nil {
		resume, err := pruneResumeCAS(ctx, *cfg.Resume, now, cfg.Apply)
		if err != nil {
			return report, err
		}
		report.Resume = resume
	}
	return report, nil
}

func scanRoot(ctx context.Context, root Root, now time.Time, apply bool) (RootReport, error) {
	rr := RootReport{Name: root.Name, Path: root.Path, Retention: root.Retention.String()}
	if strings.TrimSpace(root.Path) == "" {
		return rr, fmt.Errorf("%w: root %q has an empty path", ErrInvalidConfig, root.Name)
	}
	if root.Retention <= 0 {
		return rr, fmt.Errorf("%w: root %q retention must be > 0", ErrInvalidConfig, root.Name)
	}
	abs, err := filepath.Abs(root.Path)
	if err != nil {
		return rr, fmt.Errorf("%w: resolve root %q: %v", ErrInvalidConfig, root.Name, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			rr.Path = abs
			return rr, nil
		}
		return rr, fmt.Errorf("%w: stat root %q: %v", ErrInvalidConfig, root.Name, err)
	}
	if !info.IsDir() {
		return rr, fmt.Errorf("%w: root %q is not a directory", ErrInvalidConfig, root.Name)
	}
	rr.Path = abs
	rr.Cutoff = now.Add(-root.Retention).UTC()

	var dirs []string
	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			rr.Errors = append(rr.Errors, path+": "+err.Error())
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			rr.Errors = append(rr.Errors, path+": "+err.Error())
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		rr.Files++
		rr.Bytes += fi.Size()
		if rr.Oldest == nil || fi.ModTime().Before(*rr.Oldest) {
			oldest := fi.ModTime().UTC()
			rr.Oldest = &oldest
		}
		if !fi.ModTime().Before(rr.Cutoff) {
			return nil
		}
		rr.EligibleFiles++
		rr.EligibleBytes += fi.Size()
		if !apply {
			return nil
		}
		if err := os.Remove(path); err != nil {
			rr.Errors = append(rr.Errors, path+": "+err.Error())
			return nil
		}
		rr.DeletedFiles++
		rr.DeletedBytes += fi.Size()
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return rr, walkErr
		}
		rr.Errors = append(rr.Errors, walkErr.Error())
	}
	if apply {
		pruneEmptyDirs(abs, dirs)
	}
	return rr, nil
}

// pruneEmptyDirs removes only empty child directories. The root itself is
// never removed. os.Remove is used (not RemoveAll), so a directory that gained
// a file after the scan is left intact.
func pruneEmptyDirs(root string, dirs []string) {
	sort.Slice(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], string(filepath.Separator)) > strings.Count(dirs[j], string(filepath.Separator))
	})
	for _, dir := range dirs {
		if dir == root {
			continue
		}
		_ = os.Remove(dir)
	}
}

// HasErrors reports whether any root or cache step recorded a failure.
func (r Report) HasErrors() bool {
	if len(r.Errors) > 0 {
		return true
	}
	for _, root := range r.Roots {
		if len(root.Errors) > 0 {
			return true
		}
	}
	if r.Resume != nil && len(r.Resume.Errors) > 0 {
		return true
	}
	return false
}
