// Package config provides configuration management for the VeloxEditing system.
package config

import "sync"

// Config holds all configuration for the application.
type Config struct {
	mu sync.RWMutex

	Server    ServerConfig    `yaml:"server"`
	Logging   LoggingConfig   `yaml:"logging"`
	Storage   StorageConfig   `yaml:"storage"`
	Security  SecurityConfig  `yaml:"security"`
	External  ExternalConfig  `yaml:"external"`
	Paths     PathsConfig     `yaml:"paths"`
	Drive     DriveConfig     `yaml:"drive"`
	Harvester HarvesterConfig `yaml:"harvester"`
	Jobs      JobsConfig      `yaml:"jobs"`
	Workers   WorkersConfig   `yaml:"workers"`
}

// HarvesterConfig holds settings for the content harvester.
type HarvesterConfig struct {
	Enabled                bool     `yaml:"enabled" default:"true"`
	CheckInterval          string   `yaml:"check_interval" default:"1h"`
	SearchQueries          []string `yaml:"search_queries" default:"[]"`
	MaxResultsPerQuery     int      `yaml:"max_results_per_query" default:"20"`
	MinViews               int      `yaml:"min_views" default:"10000"`
	Timeframe              string   `yaml:"timeframe" default:"month"`
	MaxConcurrentDownloads int      `yaml:"max_concurrent_downloads" default:"3"`
	DownloadDir            string   `yaml:"download_dir" default:"./downloads"`
	ProcessClips           bool     `yaml:"process_clips" default:"true"`
	DriveFolderID          string   `yaml:"drive_folder_id" env:"VELOX_ARTLIST_ROOT_FOLDER" default:""`
}

// DriveConfig holds Google Drive configuration.
type DriveConfig struct {
	StockRootFolder string `yaml:"stock_root_folder" env:"VELOX_DRIVE_STOCK_ROOT" default:""`
	ClipsRootFolder string `yaml:"clips_root_folder" env:"VELOX_DRIVE_CLIPS_ROOT" default:""`
}

// LoggingConfig holds logger-specific configuration.
type LoggingConfig struct {
	Level  string `yaml:"level" env:"VELOX_LOG_LEVEL" default:"info"`
	Format string `yaml:"format" env:"VELOX_LOG_FORMAT" default:"json"`
}

// ServerConfig holds server-specific configuration.
type ServerConfig struct {
	Host         string `yaml:"host" env:"VELOX_HOST" default:"0.0.0.0"`
	Port         int    `yaml:"port" env:"VELOX_PORT" default:"8080"`
	ReadTimeout  int    `yaml:"read_timeout" env:"VELOX_READ_TIMEOUT" default:"600"`
	WriteTimeout int    `yaml:"write_timeout" env:"VELOX_WRITE_TIMEOUT" default:"600"`
	GinMode      string `yaml:"gin_mode" env:"GIN_MODE" default:"release"`
}

// StorageConfig holds storage configuration.
type StorageConfig struct {
	DataDir string `yaml:"data_dir" env:"VELOX_DATA_DIR" default:"./data"`
}

// SecurityConfig holds security-related configuration.
type SecurityConfig struct {
	AdminToken        string   `yaml:"admin_token" env:"VELOX_ADMIN_TOKEN" default:""`
	WorkerToken       string   `yaml:"worker_token" env:"VELOX_WORKER_TOKEN" default:""`
	EnableAuth        bool     `yaml:"enable_auth" env:"VELOX_ENABLE_AUTH" default:"false"`
	CORSOrigins       []string `yaml:"cors_origins" env:"VELOX_CORS_ORIGINS" default:"[\"*\"]"`
	RateLimitEnabled  bool     `yaml:"rate_limit_enabled" env:"VELOX_RATE_LIMIT_ENABLED" default:"true"`
	RateLimitRequests int      `yaml:"rate_limit_requests" env:"VELOX_RATE_LIMIT_REQUESTS" default:"100"`
}

// ExternalConfig holds external service configuration.
type ExternalConfig struct {
	OllamaURL string `yaml:"ollama_url" env:"OLLAMA_ADDR" default:"http://localhost:11434"`
}

// PathsConfig holds the few filesystem paths still used by the minimal server.
type PathsConfig struct {
	CredentialsFile  string `yaml:"credentials_file" env:"VELOX_CREDENTIALS_FILE" default:"credentials.json"`
	TokenFile        string `yaml:"token_file" env:"VELOX_TOKEN_FILE" default:"token.json"`
	ClipTextDir      string `yaml:"clip_text_dir" env:"VELOX_CLIP_TEXT_DIR" default:""`
	PythonScriptsDir string `yaml:"python_scripts_dir" env:"VELOX_PYTHON_SCRIPTS_DIR" default:"../../python"`
	NodeScraperDir   string `yaml:"node_scraper_dir" env:"VELOX_NODE_SCRAPER_DIR" default:"../node-scraper"`
}

// JobsConfig holds job-related configuration.
type JobsConfig struct {
	NewJobsPaused         bool `yaml:"new_jobs_paused" default:"false"`
	LeaseTTLSeconds       int  `yaml:"lease_ttl_seconds" default:"300"`
	MaxParallelPerProject int  `yaml:"max_parallel_per_project" default:"2"`
	AutoCleanupHours      int  `yaml:"auto_cleanup_hours" default:"24"`
}

// WorkersConfig holds worker-related configuration.
type WorkersConfig struct {
	AllowedIPs              []string `yaml:"allowed_ips" default:"[]"`
	HeartbeatTimeout        int      `yaml:"heartbeat_timeout" default:"30"`
	WorkerFailWindowSeconds int      `yaml:"worker_fail_window_seconds" default:"300"`
	WorkerFailThreshold     int      `yaml:"worker_fail_threshold" default:"3"`
}
