package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/storage"
)

// PostgreSQLMediaConfig configures the PostgreSQL + pgvector database that
// owns the media domain. It is intentionally explicit: an empty DSN is not
// interpreted as SQLite compatibility or a Qdrant fallback.
type PostgreSQLMediaConfig struct {
	Enabled                bool   `yaml:"enabled" env:"PIPELINEGEN_MEDIA_POSTGRES_ENABLED" default:"false"`
	DSN                    string `yaml:"dsn" env:"PIPELINEGEN_MEDIA_POSTGRES_DSN" default:""`
	MaxOpenConnections     int    `yaml:"max_open_connections" env:"PIPELINEGEN_MEDIA_POSTGRES_MAX_OPEN_CONNECTIONS" default:"10"`
	MaxIdleConnections     int    `yaml:"max_idle_connections" env:"PIPELINEGEN_MEDIA_POSTGRES_MAX_IDLE_CONNECTIONS" default:"5"`
	ConnMaxLifetimeSeconds int    `yaml:"conn_max_lifetime_seconds" env:"PIPELINEGEN_MEDIA_POSTGRES_CONN_MAX_LIFETIME_SECONDS" default:"300"`
}

// Validate enforces fail-closed PostgreSQL selection. The DSN is required
// whenever media PostgreSQL is enabled; invalid pool settings are rejected.
func (c PostgreSQLMediaConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.DSN) == "" {
		return fmt.Errorf("media PostgreSQL is enabled but PIPELINEGEN_MEDIA_POSTGRES_DSN is empty")
	}
	if c.MaxOpenConnections <= 0 {
		return fmt.Errorf("media PostgreSQL max_open_connections must be greater than zero")
	}
	if c.MaxIdleConnections < 0 || c.MaxIdleConnections > c.MaxOpenConnections {
		return fmt.Errorf("media PostgreSQL max_idle_connections must be between 0 and max_open_connections")
	}
	if c.ConnMaxLifetimeSeconds < 0 {
		return fmt.Errorf("media PostgreSQL conn_max_lifetime_seconds must not be negative")
	}
	return nil
}

type StorageConfig struct {
	// DataDir is the root for ALL persisted data (DBs + blobs).
	DataDir string `yaml:"data_dir" env:"VELOX_DATA_DIR" default:"./data"`
	// ObservabilityDBPath is the file path for the API request log DB
	// (`api_requests` table + indexes), distinct from the primary database.
	// Log retention doesn't churn the schema-versioned primary DB.
	// Default: `<DataDir>/observability/api_requests.db.sqlite`.
	ObservabilityDBPath string `yaml:"observability_db_path" env:"VELOX_OBSERVABILITY_DB_PATH" default:""`
	// CacheDBPath optionally overrides the disposable cache database location.
	// An unavailable cache is a miss and never blocks business-state startup.
	CacheDBPath string `yaml:"cache_db_path" env:"VELOX_CACHE_DB_PATH" default:""`
	// WorkspaceDir is for transient job scratch space.
	WorkspaceDir string `yaml:"workspace_dir" env:"VELOX_WORKSPACE_DIR" default:""`
	// CacheDir is for derived artifacts (re-rendered thumbnails, etc.).
	CacheDir string `yaml:"cache_dir" env:"VELOX_CACHE_DIR" default:""`
	// ExportDir is for one-off exports (download bundles, audit dumps).
	ExportDir string `yaml:"export_dir" env:"VELOX_EXPORT_DIR" default:""`
	// SQLiteReaderMaxOpenConns controls the media reader pool size. Values
	// below two use the platform default (runtime.NumCPU, with a minimum of 2).
	SQLiteReaderMaxOpenConns int `yaml:"sqlite_reader_max_open_conns" env:"VELOX_SQLITE_READER_MAX_OPEN_CONNS" default:"0"`

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
func (s StorageConfig) AbsDataDir() string { return s.absDataDir() }

// absDataDir is the private resolver underlying AbsDataDir. Empty
// s.DataDir is treated as "./data" for manually constructed configs.
func (s StorageConfig) absDataDir() string {
	raw := s.DataDir
	if raw == "" {
		raw = "./data"
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return raw
	}
	return abs
}

// FullPath returns the absolute path to a subdirectory within DataDir.
func (s StorageConfig) FullPath(subDir string) string {
	if filepath.IsAbs(subDir) {
		return subDir
	}
	return filepath.Join(s.absDataDir(), subDir)
}

// mediaSubPath returns the absolute path to a subdirectory under MediaDir.
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

// SubtitlesPath returns the full path to the YouTube subtitle cache directory.
func (s StorageConfig) SubtitlesPath() string { return s.mediaSubPath("subtitles") }

// StagingPath returns the configured staging workspace root.
func (s StorageConfig) StagingPath() string {
	if s.StagingDir == "" {
		return "/var/lib/pipelinegen/staging"
	}
	return s.StagingDir
}

func (s StorageConfig) ToDatabaseStorageConfig() interface {
	DataDir() string
	ObservabilityDBPath() string
	WorkspaceDir() string
	CacheDir() string
	ExportDir() string
	StagingDir() string
} {
	return storageSetAdapter{s: s}
}

// CanonicalPrimaryDBPath returns the only primary SQLite path accepted by
// the runtime: <AbsDataDir>/media/media.db.sqlite. The path components
// (media.db.sqlite under the media subdirectory) are owned by
// storage.StorageTopology, the single source of truth for the canonical
// primary store identity; this method only contributes the resolved DataDir.
func (s StorageConfig) CanonicalPrimaryDBPath() string {
	return filepath.Join(s.AbsDataDir(), storage.MediaDBDirectory, storage.MediaDBFilename)
}

// PrimaryDBFullPath derives the primary SQLite path exclusively from DataDir.
// There is no configured path override: changing the deployment root is done
// with DataDir, while the database identity remains fixed.
func (s StorageConfig) PrimaryDBFullPath() string {
	return s.CanonicalPrimaryDBPath()
}

func (s StorageConfig) SQLiteReaderCount() int { return s.SQLiteReaderMaxOpenConns }

func (s StorageConfig) ObservabilityDBFullPath() string {
	if s.ObservabilityDBPath != "" {
		return s.ObservabilityDBPath
	}
	return s.FullPath("observability/api_requests.db.sqlite")
}

func (s StorageConfig) CacheDBFullPath() string {
	if s.CacheDBPath != "" {
		return s.CacheDBPath
	}
	return s.FullPath("cache/cache.db.sqlite")
}

func (s StorageConfig) JobsDBFullPath() string {
	return s.FullPath("jobs/jobs.db.sqlite")
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

func (a storageSetAdapter) ObservabilityDBPath() string { return a.s.ObservabilityDBFullPath() }
func (a storageSetAdapter) WorkspaceDir() string        { return a.s.WorkspaceFullPath() }
func (a storageSetAdapter) CacheDir() string            { return a.s.CacheFullPath() }
func (a storageSetAdapter) ExportDir() string           { return a.s.ExportFullPath() }
func (a storageSetAdapter) StagingDir() string          { return a.s.StagingPath() }
