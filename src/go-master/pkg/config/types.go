// Package config provides configuration management for the VeloxEditing system.
package config

import (
	"fmt"
	"sync"
)

// Config holds all configuration for the application
type Config struct {
	mu sync.RWMutex

	// Server configuration
	Server ServerConfig `yaml:"server"`

	// Logging configuration
	Logging LoggingConfig `yaml:"logging"`

	// Storage configuration
	Storage StorageConfig `yaml:"storage"`

	// Job configuration
	Jobs JobsConfig `yaml:"jobs"`

	// Worker configuration
	Workers WorkersConfig `yaml:"workers"`

	// Security configuration
	Security SecurityConfig `yaml:"security"`

	// External services
	External ExternalConfig `yaml:"external"`

	// Scraper configuration
	Scraper ScraperConfig `yaml:"scraper"`

	// File paths configuration
	Paths PathsConfig `yaml:"paths"`

	// Drive folder configuration
	Drive DriveConfig `yaml:"drive"`

	// Scheduler configuration
	Scheduler SchedulerConfig `yaml:"scheduler"`

	// DriveSync configuration
	DriveSync DriveSyncConfig `yaml:"drivesync"`

	// Clip approval configuration
	ClipApproval ClipApprovalConfig `yaml:"clip_approval"`

	// Clip index configuration
	ClipIndex ClipIndexConfig `yaml:"clip_index"`

	// Text generator configuration
	TextGen TextGenConfig `yaml:"textgen"`
}

// LoggingConfig holds logger-specific configuration
type LoggingConfig struct {
	Level  string `yaml:"level" env:"VELOX_LOG_LEVEL" default:"info"`
	Format string `yaml:"format" env:"VELOX_LOG_FORMAT" default:"json"`
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	Host         string `yaml:"host" env:"VELOX_HOST" default:"0.0.0.0"`
	Port         int    `yaml:"port" env:"VELOX_PORT" default:"8080"`
	ReadTimeout  int    `yaml:"read_timeout" env:"VELOX_READ_TIMEOUT" default:"600"`
	WriteTimeout int    `yaml:"write_timeout" env:"VELOX_WRITE_TIMEOUT" default:"600"`
	GinMode      string `yaml:"gin_mode" env:"GIN_MODE" default:"release"`
}

// StorageConfig holds storage configuration
type StorageConfig struct {
	DataDir          string `yaml:"data_dir" env:"VELOX_DATA_DIR" default:"./data"`
	QueueFile        string `yaml:"queue_file" env:"VELOX_QUEUE_FILE" default:"queue.json"`
	WorkersFile      string `yaml:"workers_file" env:"VELOX_WORKERS_FILE" default:"workers.json"`
	BackupDir        string `yaml:"backup_dir" env:"VELOX_BACKUP_DIR" default:"./backups"`
	AutoSaveInterval int    `yaml:"auto_save_interval" env:"VELOX_AUTO_SAVE_INTERVAL" default:"60"`
}

// JobsConfig holds job-related configuration
type JobsConfig struct {
	MaxParallelPerProject int  `yaml:"max_parallel_per_project" env:"VELOX_MAX_PARALLEL_PER_PROJECT" default:"2"`
	LeaseTTLSeconds       int  `yaml:"lease_ttl_seconds" env:"VELOX_LEASE_TTL_SECONDS" default:"300"`
	MaxRetries            int  `yaml:"max_retries" env:"VELOX_MAX_RETRIES" default:"3"`
	AutoCleanupHours      int  `yaml:"auto_cleanup_hours" env:"VELOX_AUTO_CLEANUP_HOURS" default:"72"`
	ZombieCheckInterval   int  `yaml:"zombie_check_interval" env:"VELOX_ZOMBIE_CHECK_INTERVAL" default:"120"`
	CleanupInterval       int  `yaml:"cleanup_interval" env:"VELOX_CLEANUP_INTERVAL" default:"3600"`
	NewJobsPaused         bool `yaml:"new_jobs_paused" env:"VELOX_NEW_JOBS_PAUSED" default:"false"`
}

// WorkersConfig holds worker-related configuration
type WorkersConfig struct {
	HeartbeatTimeout          int      `yaml:"heartbeat_timeout" env:"VELOX_HEARTBEAT_TIMEOUT" default:"120"`
	WorkerFailWindowSeconds   int      `yaml:"worker_fail_window_seconds" env:"VELOX_WORKER_FAIL_WINDOW_SECONDS" default:"300"`
	WorkerFailThreshold       int      `yaml:"worker_fail_threshold" env:"VELOX_WORKER_FAIL_THRESHOLD" default:"5"`
	MaxWorkerLogs             int      `yaml:"max_worker_logs" env:"VELOX_MAX_WORKER_LOGS" default:"100"`
	MaxWorkerErrors           int      `yaml:"max_worker_errors" env:"VELOX_MAX_WORKER_ERRORS" default:"15"`
	AllowedIPs                []string `yaml:"allowed_ips" env:"VELOX_ALLOWED_WORKER_IPS" default:"[]"`
	AutoDrainThresholdGB      float64  `yaml:"auto_drain_threshold_gb" env:"VELOX_AUTO_DRAIN_THRESHOLD_GB" default:"5.0"`
	AutoRepairCooldownSeconds int      `yaml:"auto_repair_cooldown" env:"VELOX_AUTO_REPAIR_COOLDOWN" default:"600"`
}

// SecurityConfig holds security-related configuration
type SecurityConfig struct {
	AdminToken        string   `yaml:"admin_token" env:"VELOX_ADMIN_TOKEN" default:""`
	WorkerToken       string   `yaml:"worker_token" env:"VELOX_WORKER_TOKEN" default:""`
	EnableAuth        bool     `yaml:"enable_auth" env:"VELOX_ENABLE_AUTH" default:"false"`
	CORSOrigins       []string `yaml:"cors_origins" env:"VELOX_CORS_ORIGINS" default:"[\"*\"]"`
	RateLimitEnabled  bool     `yaml:"rate_limit_enabled" env:"VELOX_RATE_LIMIT_ENABLED" default:"true"`
	RateLimitRequests int      `yaml:"rate_limit_requests" env:"VELOX_RATE_LIMIT_REQUESTS" default:"100"`
}

// ExternalConfig holds external service configuration
type ExternalConfig struct {
	YouTubeAPIBaseURL string `yaml:"youtube_api_base_url" env:"VELOX_YOUTUBE_API_URL" default:"http://localhost:8001"`
	WorkerBackendURL  string `yaml:"worker_backend_url" env:"VELOX_WORKER_BACKEND" default:""`
	OllamaURL         string `yaml:"ollama_url" env:"OLLAMA_ADDR" default:"http://localhost:11434"`
}

// ScraperConfig holds Node.js scraper configuration
type ScraperConfig struct {
	Dir     string `yaml:"dir" env:"VELOX_SCRAPER_DIR" default:"../../src/node-scraper"`
	NodeBin string `yaml:"node_bin" env:"VELOX_NODE_BIN" default:"node"`
}

// PathsConfig holds all configurable file/directory paths
type PathsConfig struct {
	TempDir                 string `yaml:"temp_dir" env:"VELOX_TEMP_DIR" default:"/tmp/velox"`
	VoiceoverDir            string `yaml:"voiceover_dir" env:"VELOX_VOICEOVER_DIR" default:"/tmp/velox/voiceovers"`
	VideoWorkDir            string `yaml:"video_work_dir" env:"VELOX_VIDEO_WORK_DIR" default:"/tmp/velox/video"`
	StockDir                string `yaml:"stock_dir" env:"VELOX_STOCK_DIR" default:"/tmp/velox"`
	DownloadDir             string `yaml:"download_dir" env:"VELOX_DOWNLOAD_DIR" default:"/tmp/velox/downloads"`
	YouTubeDir              string `yaml:"youtube_dir" env:"VELOX_YOUTUBE_DIR" default:"/tmp/velox/youtube"`
	EffectsDir              string `yaml:"effects_dir" env:"VELOX_EFFECTS_DIR" default:"/tmp/velox/effects"`
	OutputDir               string `yaml:"output_dir" env:"VELOX_OUTPUT_DIR" default:"/tmp/velox/output"`
	WhisperDir              string `yaml:"whisper_dir" env:"VELOX_WHISPER_DIR" default:"/tmp/whisper"`
	CredentialsFile         string `yaml:"credentials_file" env:"VELOX_CREDENTIALS_FILE" default:"credentials.json"`
	TokenFile               string `yaml:"token_file" env:"VELOX_TOKEN_FILE" default:"token.json"`
	ClipRootFolder          string `yaml:"clip_root_folder" env:"VELOX_CLIP_ROOT_FOLDER" default:"root"`
	VideoStockCreatorBinary string `yaml:"video_stock_creator_binary" env:"VELOX_VIDEO_STOCK_CREATOR_BINARY" default:"video-stock-creator"`
	ArtlistDBPath           string `yaml:"artlist_db_path" env:"VELOX_ARTLIST_DB_PATH" default:""`
	YtDlpPath               string `yaml:"ytdlp_path" env:"VELOX_YTDLP_PATH" default:"yt-dlp"`
}

// DriveConfig holds Google Drive folder IDs (main roots - subfolders are discovered dynamically)
type DriveConfig struct {
	StockRootFolderID string `yaml:"stock_root_folder" env:"VELOX_STOCK_ROOT_FOLDER" default:"1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh"`
	ClipsRootFolderID string `yaml:"clips_root_folder" env:"VELOX_CLIPS_ROOT_FOLDER" default:"1ID_oFJF15Q5nmiZF0d2NaJeKhsOJpQNS"`
	ArtlistFolderID   string `yaml:"artlist_folder" env:"VELOX_ARTLIST_FOLDER" default:"1aMjQlK9J1mEyT2TOYDNjeynO1GzZS4_S"`
}

// SchedulerConfig holds StockJob Scheduler configuration
type SchedulerConfig struct {
	Interval       int      `yaml:"interval" env:"VELOX_SCHEDULER_INTERVAL" default:"86400"`
	SearchQueries  []string `yaml:"search_queries" env:"VELOX_SCHEDULER_QUERIES" default:"[]"`
	MaxResults     int      `yaml:"max_results" env:"VELOX_SCHEDULER_MAX_RESULTS" default:"10"`
	MinDurationSec int      `yaml:"min_duration_sec" env:"VELOX_SCHEDULER_MIN_DURATION" default:"30"`
	MaxDurationSec int      `yaml:"max_duration_sec" env:"VELOX_SCHEDULER_MAX_DURATION" default:"300"`
}

// DriveSyncConfig holds DriveSync configuration
type DriveSyncConfig struct {
	Interval    int `yaml:"interval" env:"VELOX_DRIVESYNC_INTERVAL" default:"86400"`
	SyncTimeout int `yaml:"sync_timeout" env:"VELOX_DRIVESYNC_TIMEOUT" default:"600"`
}

// ClipApprovalConfig holds clip approval workflow configuration
type ClipApprovalConfig struct {
	MinScore             float64 `yaml:"min_score" env:"VELOX_CLIP_MIN_SCORE" default:"20.0"`
	MaxClipsPerScene     int     `yaml:"max_clips_per_scene" env:"VELOX_CLIP_MAX_PER_SCENE" default:"5"`
	AutoApproveThreshold float64 `yaml:"auto_approve_threshold" env:"VELOX_CLIP_AUTO_APPROVE" default:"85.0"`
}

// ClipIndexConfig holds clip index scanner configuration
type ClipIndexConfig struct {
	ScanInterval int `yaml:"scan_interval" env:"VELOX_CLIP_SCAN_INTERVAL" default:"3600"`
}

// TextGenConfig holds text generator configuration
type TextGenConfig struct {
	DefaultModel string `yaml:"default_model" env:"VELOX_TEXTGEN_MODEL" default:"gemma3:4b"`
	Timeout      int    `yaml:"timeout" env:"VELOX_TEXTGEN_TIMEOUT" default:"60"`
}

// GetStockFolders returns Stock root - subfolders are discovered dynamically from Drive
func (d *DriveConfig) GetStockFolders() map[string]string {
	return map[string]string{
		"stock": d.StockRootFolderID,
		"clips": d.ClipsRootFolderID,
	}
}

// StockFolderEntry represents a Stock folder with full metadata
type StockFolderEntry struct {
	ID   string
	Name string
	URL  string
}

// GetStockFolderEntries returns Stock folders - subfolders are discovered dynamically at runtime
func (d *DriveConfig) GetStockFolderEntries() map[string]StockFolderEntry {
	return map[string]StockFolderEntry{
		"stock": {
			ID:   d.StockRootFolderID,
			Name: "Stock",
			URL:  fmt.Sprintf("https://drive.google.com/drive/folders/%s", d.StockRootFolderID),
		},
		"clips": {
			ID:   d.ClipsRootFolderID,
			Name: "Clips",
			URL:  fmt.Sprintf("https://drive.google.com/drive/folders/%s", d.ClipsRootFolderID),
		},
	}
}
