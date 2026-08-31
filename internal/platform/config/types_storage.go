package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

type StorageConfig struct {
	// DataDir is the root for ALL persisted data (DBs + blobs).
	DataDir string `yaml:"data_dir" env:"VELOX_DATA_DIR" default:"./data"`
	// PrimaryDBPath is the file path for the unified media DB
	// (jobs, assets, scripts, search_queries, worker_nodes, media_assets,
	//  clip_folders, voiceovers, etc.). Defaults preserve legacy
	// `<DataDir>/media/media.db.sqlite`.
	PrimaryDBPath string `yaml:"primary_db_path" env:"VELOX_PRIMARY_DB_PATH" default:""`
	// ObservabilityDBPath is the file path for the API request log DB
	// (`api_requests` table + indexes). Distinct from PrimaryDBPath so
	// log retention doesn't churn the schema-versioned primary DB.
	// Default: `<DataDir>/observability/api_requests.db.sqlite`.
	ObservabilityDBPath string `yaml:"observability_db_path" env:"VELOX_OBSERVABILITY_DB_PATH" default:""`
	// WorkspaceDir is for transient job scratch space.
	WorkspaceDir string `yaml:"workspace_dir" env:"VELOX_WORKSPACE_DIR" default:""`
	// CacheDir is for derived artifacts (re-rendered thumbnails, etc.).
	CacheDir string `yaml:"cache_dir" env:"VELOX_CACHE_DIR" default:""`
	// ExportDir is for one-off exports (download bundles, audit dumps).
	ExportDir string `yaml:"export_dir" env:"VELOX_EXPORT_DIR" default:""`
	// ObservabilityMaxAgeDays is the retention cutoff for the
	// observability DB (`admin db rotate`). Rows with ts older than
	// this are offloaded to <DataDir>/backups/observability-<DATE>.db.sqlite
	// then DELETEd from the live DB. 0 disables rotation. See
	// ARCHITECTURE.md §12 (observability retention policy).
	ObservabilityMaxAgeDays int `yaml:"observability_max_age_days" env:"VELOX_OBSERVABILITY_MAX_AGE_DAYS" default:"7"`
	// MediaDir / TempDir are kept for backward-compat with the legacy
	// on-disk filesystem layout (voiceovers, images, youtube, etc.).
	MediaDir string `yaml:"media_dir" env:"PIPELINEGEN_MEDIA_DIR" default:"media"`
	TempDir  string `yaml:"temp_dir" env:"VELOX_TEMP_DIR" default:"tmp"`
	// StagingDir is the canonical root for the FASE 3 Spina Dorsale
	// staging workspace. The artifact_stages pipeline writes each
	// Stage-verify request under `{StagingDir}/{job_id}/{stage_id}`;
	// the publisher worker then reads + uploads the file to Drive
	// before marking the row PUBLISHED. Default: `/var/lib/pipelinegen/staging`
	// (an ABSOLUTE path, NOT relative to DataDir — staging is a
	// production-grade durable root, distinct from the per-deployment
	// DataDir layout). Operators MAY set this to a different path via
	// the PIPELINEGEN_STAGING_WORKSPACE env var; a relative path is
	// kept as-is (no DataDir prepend) so an operator-configured relative
	// path resolves against the cwd of the running process.
	StagingDir string `yaml:"staging_dir" env:"PIPELINEGEN_STAGING_WORKSPACE" default:"/var/lib/pipelinegen/staging"`
}

func (s StorageConfig) MediaPath() string { return s.FullPath(s.MediaDir) }

// TempPath returns the full path to the temporary directory.
func (s StorageConfig) TempPath() string { return s.FullPath(s.TempDir) }

// AbsDataDir returns the canonical absolute form of s.DataDir. An empty
// s.DataDir falls back to the struct's documented default ("./data")
// and is then resolved via filepath.Abs against the current working
// directory.
//
// godlike/06 SSOT: every disk-layout helper on StorageConfig MUST route
// through this method so callers receive an absolute path regardless of
// the operator's process cwd or whether the configured DataDir was
// relative. A relative "./data" returned to filepath.EvalSymlinks stays
// as "data" (the relative form) and breaks the IsLocalFolderAllowed
// symlink-canonicalization guard at
// internal/application/clips/bulk_upload_helpers.go (FASE-N EvalSymlinks
// audit). The DataDir field itself remains a raw string so existing
// callers that compose cfg.Storage.DataDir into ad-hoc paths (192+
// matches) continue to compile; new callers that feed the value into
// filesystem APIs preferring an absolute root SHOULD prefer AbsDataDir().
//
// Empty DataDir or an Abs() failure falls back to the raw input so the
// caller surfaces a meaningful error downstream — never silently
// rewrite to "/".
func (s StorageConfig) AbsDataDir() string { return s.absDataDir() }

// absDataDir is the private resolver underlying AbsDataDir. Empty
// s.DataDir is treated as "./data" (the struct field's `default:` tag
// resolves to "./data" at config-load time; this fallback covers the
// case where the struct is constructed manually without going through
// the env-var loader).
func (s StorageConfig) absDataDir() string {
	raw := s.DataDir
	if raw == "" {
		raw = "./data"
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return raw // best-effort: caller surfaces Err downstream
	}
	return abs
}

// FullPath returns the absolute path to a subdirectory within DataDir.
// The DataDir root is canonicalized via absDataDir() so the result is
// always absolute regardless of the operator's process cwd or whether
// the configured DataDir was relative. An ALREADY-ABSOLUTE subDir is
// returned verbatim (operator override semantics — see StagingPath for
// the analogous contract on the staging workspace).
func (s StorageConfig) FullPath(subDir string) string {
	if filepath.IsAbs(subDir) {
		return subDir
	}
	return filepath.Join(s.absDataDir(), subDir)
}

// mediaSubPath returns the absolute path to a subdirectory under
// MediaDir. Routed through absDataDir() for the same cwd-absolute
// invariant that FullPath provides.
func (s StorageConfig) mediaSubPath(sub string) string {
	return filepath.Join(s.absDataDir(), s.MediaDir, sub)
}

// VoiceoversPath returns the full path to the voiceovers directory.
func (s StorageConfig) VoiceoversPath() string { return s.mediaSubPath("voiceovers") }

// AssetsPath returns the full path to the main assets directory.
func (s StorageConfig) AssetsPath() string { return s.mediaSubPath("assets") }

// DownloadsPath returns the full path to the downloads directory.
func (s StorageConfig) DownloadsPath() string { return s.mediaSubPath("downloads") }

// BackupsPath returns the full path to the backups directory.
func (s StorageConfig) BackupsPath() string { return s.FullPath("backups") }

// AnimationsPath returns the full path to the animations directory.
func (s StorageConfig) AnimationsPath() string { return s.mediaSubPath("animations") }

// YoutubeClipsPath returns the full path to the youtube clips directory.
func (s StorageConfig) YoutubeClipsPath() string { return s.mediaSubPath("youtube") }

// ArtlistPath returns the full path to the artlist directory.
func (s StorageConfig) ArtlistPath() string { return s.mediaSubPath("artlist") }

// ImagesPath returns the full path to the images directory.
func (s StorageConfig) ImagesPath() string { return s.mediaSubPath("images") }

// SubtitlesPath returns the full path to the YouTube subtitle cache
// directory. Used by the wired SubtitleFetcherAdapter (infrastructure
// layer) at PR-WIRE-SUBTITLE-FETCHER-ADAPTER (2026-07-06) to cache
// per-videoID .vtt files for SliceSubtitles lookups.
func (s StorageConfig) SubtitlesPath() string { return s.mediaSubPath("subtitles") }

// StagingPath returns the canonical FASE 3 Spina Dorsale staging
// workspace root. If StagingDir is empty, defaults to
// `/var/lib/pipelinegen/staging` (per the `default:` tag on the
// struct field — the env-var loader applies it at startup; this
// fallback covers the case where the struct is constructed in a
// test or admin CLI without going through the env loader).
//
// godlike/06 SSOT: the single canonical resolution of the staging
// workspace dir. The composition root passes the returned value to
// staging.NewStoreService(repo, workspace); consumers downstream
// (publisher worker pool, finalizer) MUST NOT re-resolve the path
// independently — they receive the LocalPath via StageReceipt and
// rely on the Service's idGen to keep paths unique.
func (s StorageConfig) StagingPath() string {
	if s.StagingDir == "" {
		return "/var/lib/pipelinegen/staging"
	}
	return s.StagingDir
}

func (s StorageConfig) ToDatabaseStorageConfig() interface {
	DataDir() string
	PrimaryDBPath() string
	ObservabilityDBPath() string
	WorkspaceDir() string
	CacheDir() string
	ExportDir() string
	StagingDir() string
} {
	return storageSetAdapter{s: s}
}

// CanonicalPrimaryDBPath returns the only primary SQLite path accepted by
// the runtime: <AbsDataDir>/media/media.db.sqlite.
func (s StorageConfig) CanonicalPrimaryDBPath() string {
	return filepath.Join(s.AbsDataDir(), "media", "media.db.sqlite")
}

// ValidatePrimaryDBPath rejects legacy, relative, and arbitrary primary DB
// paths before they reach the runtime database opener. Tests and migration
// tools may still open isolated databases directly through the SQLite package;
// this gate applies to the configured operational primary only.
func (s StorageConfig) ValidatePrimaryDBPath() error {
	configured := strings.TrimSpace(s.PrimaryDBPath)
	if configured == "" {
		return nil
	}
	configuredAbs, err := filepath.Abs(filepath.Clean(configured))
	if err != nil {
		return fmt.Errorf("primary SQLite path %q cannot be resolved: %w", configured, err)
	}
	canonical := filepath.Clean(s.CanonicalPrimaryDBPath())
	if configuredAbs != canonical {
		return fmt.Errorf("non-canonical primary SQLite path %q; use %q", configured, canonical)
	}
	return nil
}

// Path resolution helpers — used by internal/app/bootstrap.go and any
// subsystem that needs the canonical disk layout under DataDir.
func (s StorageConfig) PrimaryDBFullPath() string {
	if err := s.ValidatePrimaryDBPath(); err != nil {
		return ""
	}
	return s.CanonicalPrimaryDBPath()
}
func (s StorageConfig) ObservabilityDBFullPath() string {
	if s.ObservabilityDBPath != "" {
		return s.ObservabilityDBPath
	}
	return s.FullPath("observability/api_requests.db.sqlite")
}
func (s StorageConfig) WorkspaceFullPath() string {
	if s.WorkspaceDir != "" {
		return s.WorkspaceDir
	}
	return s.FullPath("workspace")
}
func (s StorageConfig) CacheFullPath() string {
	if s.CacheDir != "" {
		return s.CacheDir
	}
	return s.FullPath("cache")
}
func (s StorageConfig) ExportFullPath() string {
	if s.ExportDir != "" {
		return s.ExportDir
	}
	return s.FullPath("export")
}

type storageSetAdapter struct {
	s StorageConfig
}

func (a storageSetAdapter) DataDir() string {
	if a.s.DataDir == "" {
		return "data"
	}
	return a.s.DataDir
}
func (a storageSetAdapter) PrimaryDBPath() string       { return a.s.PrimaryDBFullPath() }
func (a storageSetAdapter) ObservabilityDBPath() string { return a.s.ObservabilityDBFullPath() }
func (a storageSetAdapter) WorkspaceDir() string        { return a.s.WorkspaceFullPath() }
func (a storageSetAdapter) CacheDir() string            { return a.s.CacheFullPath() }
func (a storageSetAdapter) ExportDir() string           { return a.s.ExportFullPath() }
func (a storageSetAdapter) StagingDir() string          { return a.s.StagingPath() }
