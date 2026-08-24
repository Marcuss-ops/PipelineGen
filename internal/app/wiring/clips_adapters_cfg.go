package wiring

import (
	"time"

	clips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// clipsCfgAdapter wraps *config.Config to satisfy clips.ClipConfigPort.
// Each method exposes exactly the field the handler reads (Pattern 0:
// minimal — never return the whole *Config). The delegation order
// matches the canonical accessors on *config.Config:
//
//	cfg.Drive.X   for ClipsDriveFolder / ArtlistDriveFolder / StockDriveFolder
//	cfg.Storage.X for MediaPath / TempPath / DataDir / YoutubeClipsPath / AssetsPath
//
// HC-1 (June 2026): `timeouts appjobs.TimeoutResolver` is the typed
// config-port for per-job-type execution timeouts (replaces the pre-HC-1
// package-level `var jobTimeoutRegistry` global in worker.go + the
// hard-coded `context.WithTimeout(ctx, 2*time.Hour)` in bulk_upload_worker.go).
// The adapter delegates JobTimeout(t) to the resolver; production wiring
// passes jobs.Compose() (canonical *jobs.Registry).
type clipsCfgAdapter struct {
	cfg      *config.Config
	timeouts appjobs.TimeoutResolver
}

// Compile-time assertion: clipsCfgAdapter satisfies clips.ClipConfigPort.
var _ clips.ClipConfigPort = (*clipsCfgAdapter)(nil)

// newClipsCfgAdapter constructs the typed-config-port adapter. Both
// arguments are required for production wiring; the timeouts resolver
// may be nil only in test fixtures that don't exercise the JobTimeout
// path (the adapter falls back to the canonical 10-minute default).
//
// HC-1 signature change vs the pre-HC-1 single-arg ctor: `timeouts` is
// the new 2nd positional argument. Composition root callers (in
// internal/app/clips_adapters_index.go + internal/app/module_media.go)
// have been updated to pass appjobs.Compose() explicitly.
func newClipsCfgAdapter(cfg *config.Config, timeouts appjobs.TimeoutResolver) clips.ClipConfigPort {
	if cfg == nil && timeouts == nil {
		return nil
	}
	return &clipsCfgAdapter{cfg: cfg, timeouts: timeouts}
}

func (a *clipsCfgAdapter) ClipsDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ClipsFolder()
}

func (a *clipsCfgAdapter) RootFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.RootFolder()
}

func (a *clipsCfgAdapter) ArtlistDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ArtlistFolder()
}

func (a *clipsCfgAdapter) StockDriveFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.StockFolder()
}

func (a *clipsCfgAdapter) MediaPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.MediaPath()
}

func (a *clipsCfgAdapter) TempPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.TempPath()
}

func (a *clipsCfgAdapter) DataDir() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.DataDir
}

func (a *clipsCfgAdapter) YoutubeClipsPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.YoutubeClipsPath()
}

func (a *clipsCfgAdapter) AssetsPath() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Storage.AssetsPath()
}

func (a *clipsCfgAdapter) AssetsStoragePath() string {
	return a.AssetsPath()
}

// JobTimeout delegates to the typed TimeoutResolver port (HC-1, June 2026).
// Replaces the pre-HC-1 hard-coded `context.WithTimeout(ctx, 2*time.Hour)` in
// bulk_upload_worker.go. Falls back to the canonical 10-minute default when
// (a) the adapter receiver is nil (typed-nil interface case often produced
// by test fixtures that pass `cfg=nil, timeouts=nil`),
// (b) the resolver is nil, or
// (c) the resolver returns zero.
//
// HC-1 code-review cleanup: trust the typed-port contract in production
// (composition root unconditionally passes non-nil cfg + non-nil
// appjobs.Compose()), but defend against the typed-nil receiver pattern
// that Go interfaces inherit — a nil interface VALUE carrying a nil
// pointer dereferences on first field access without this guard.
func (a *clipsCfgAdapter) JobTimeout(jobType string) time.Duration {
	const defaultTimeout = 10 * time.Minute
	if a == nil {
		return defaultTimeout
	}
	if a.timeouts == nil {
		return defaultTimeout
	}
	d := a.timeouts.JobTimeout(jobType)
	if d <= 0 {
		return defaultTimeout
	}
	return d
}
