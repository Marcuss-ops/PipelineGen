// Package config provides configuration management for the VeloxEditing system.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
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
	ReadTimeout  int    `yaml:"read_timeout" env:"VELOX_READ_TIMEOUT" default:"30"`
	WriteTimeout int    `yaml:"write_timeout" env:"VELOX_WRITE_TIMEOUT" default:"30"`
	GinMode      string `yaml:"gin_mode" env:"GIN_MODE" default:"release"`
}

// StorageConfig holds storage configuration
type StorageConfig struct {
	DataDir       string `yaml:"data_dir" env:"VELOX_DATA_DIR" default:"./data"`
	QueueFile     string `yaml:"queue_file" env:"VELOX_QUEUE_FILE" default:"queue.json"`
	WorkersFile   string `yaml:"workers_file" env:"VELOX_WORKERS_FILE" default:"workers.json"`
	BackupDir     string `yaml:"backup_dir" env:"VELOX_BACKUP_DIR" default:"./backups"`
	AutoSaveInterval int `yaml:"auto_save_interval" env:"VELOX_AUTO_SAVE_INTERVAL" default:"60"`
}

// JobsConfig holds job-related configuration
type JobsConfig struct {
	MaxParallelPerProject int    `yaml:"max_parallel_per_project" env:"VELOX_MAX_PARALLEL_PER_PROJECT" default:"2"`
	LeaseTTLSeconds       int    `yaml:"lease_ttl_seconds" env:"VELOX_LEASE_TTL_SECONDS" default:"300"`
	MaxRetries            int    `yaml:"max_retries" env:"VELOX_MAX_RETRIES" default:"3"`
	AutoCleanupHours      int    `yaml:"auto_cleanup_hours" env:"VELOX_AUTO_CLEANUP_HOURS" default:"72"`
	ZombieCheckInterval   int    `yaml:"zombie_check_interval" env:"VELOX_ZOMBIE_CHECK_INTERVAL" default:"120"`
	CleanupInterval       int    `yaml:"cleanup_interval" env:"VELOX_CLEANUP_INTERVAL" default:"3600"`
	NewJobsPaused         bool   `yaml:"new_jobs_paused" env:"VELOX_NEW_JOBS_PAUSED" default:"false"`
}

// WorkersConfig holds worker-related configuration
type WorkersConfig struct {
	HeartbeatTimeout        int      `yaml:"heartbeat_timeout" env:"VELOX_HEARTBEAT_TIMEOUT" default:"120"`
	WorkerFailWindowSeconds int      `yaml:"worker_fail_window_seconds" env:"VELOX_WORKER_FAIL_WINDOW_SECONDS" default:"300"`
	WorkerFailThreshold     int      `yaml:"worker_fail_threshold" env:"VELOX_WORKER_FAIL_THRESHOLD" default:"5"`
	MaxWorkerLogs           int      `yaml:"max_worker_logs" env:"VELOX_MAX_WORKER_LOGS" default:"100"`
	MaxWorkerErrors         int      `yaml:"max_worker_errors" env:"VELOX_MAX_WORKER_ERRORS" default:"15"`
	AllowedIPs              []string `yaml:"allowed_ips" env:"VELOX_ALLOWED_WORKER_IPS" default:"[]"`
	AutoDrainThresholdGB    float64  `yaml:"auto_drain_threshold_gb" env:"VELOX_AUTO_DRAIN_THRESHOLD_GB" default:"5.0"`
	AutoRepairCooldownSeconds int   `yaml:"auto_repair_cooldown" env:"VELOX_AUTO_REPAIR_COOLDOWN" default:"600"`
}

// SecurityConfig holds security-related configuration
type SecurityConfig struct {
	AdminToken    string `yaml:"admin_token" env:"VELOX_ADMIN_TOKEN" default:""`
	WorkerToken   string `yaml:"worker_token" env:"VELOX_WORKER_TOKEN" default:""`
	EnableAuth    bool   `yaml:"enable_auth" env:"VELOX_ENABLE_AUTH" default:"false"`
	CORSOrigins   []string `yaml:"cors_origins" env:"VELOX_CORS_ORIGINS" default:"[\"*\"]"`
	RateLimitEnabled bool `yaml:"rate_limit_enabled" env:"VELOX_RATE_LIMIT_ENABLED" default:"true"`
	RateLimitRequests int `yaml:"rate_limit_requests" env:"VELOX_RATE_LIMIT_REQUESTS" default:"100"`
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
	// TempDir is the base temporary directory for all intermediate files
	TempDir string `yaml:"temp_dir" env:"VELOX_TEMP_DIR" default:"/tmp/velox"`

	// VoiceoverDir is the output directory for TTS voiceover files
	VoiceoverDir string `yaml:"voiceover_dir" env:"VELOX_VOICEOVER_DIR" default:"/tmp/velox/voiceovers"`

	// VideoWorkDir is the working directory for video processing
	VideoWorkDir string `yaml:"video_work_dir" env:"VELOX_VIDEO_WORK_DIR" default:"/tmp/velox/video"`

	// StockDir is the root directory for stock video management
	StockDir string `yaml:"stock_dir" env:"VELOX_STOCK_DIR" default:"/tmp/velox"`

	// DownloadDir is the output directory for downloaded videos
	DownloadDir string `yaml:"download_dir" env:"VELOX_DOWNLOAD_DIR" default:"/tmp/velox/downloads"`

	// YouTubeDir is the working directory for YouTube integration
	YouTubeDir string `yaml:"youtube_dir" env:"VELOX_YOUTUBE_DIR" default:"/tmp/velox/youtube"`

	// EffectsDir is the directory for video effects definitions
	EffectsDir string `yaml:"effects_dir" env:"VELOX_EFFECTS_DIR" default:"/tmp/velox/effects"`

	// OutputDir is the default output directory for finished videos
	OutputDir string `yaml:"output_dir" env:"VELOX_OUTPUT_DIR" default:"/tmp/velox/output"`

	// WhisperDir is the output directory for Whisper transcription files
	WhisperDir string `yaml:"whisper_dir" env:"VELOX_WHISPER_DIR" default:"/tmp/whisper"`

	// CredentialsFile is the path to the Google OAuth credentials file
	CredentialsFile string `yaml:"credentials_file" env:"VELOX_CREDENTIALS_FILE" default:"credentials.json"`

	// TokenFile is the path to the Google OAuth token file
	TokenFile string `yaml:"token_file" env:"VELOX_TOKEN_FILE" default:"token.json"`

	// ClipRootFolder is the Google Drive root folder ID for clip management
	ClipRootFolder string `yaml:"clip_root_folder" env:"VELOX_CLIP_ROOT_FOLDER" default:"root"`

	// VideoStockCreatorBinary is the path to the video-stock-creator binary
	VideoStockCreatorBinary string `yaml:"video_stock_creator_binary" env:"VELOX_VIDEO_STOCK_CREATOR_BINARY" default:"video-stock-creator"`

	// ArtlistDBPath is the path to the Artlist SQLite database
	ArtlistDBPath string `yaml:"artlist_db_path" env:"VELOX_ARTLIST_DB_PATH" default:""`

	// YtDlpPath is the path to the yt-dlp binary
	YtDlpPath string `yaml:"ytdlp_path" env:"VELOX_YTDLP_PATH" default:"yt-dlp"`
}

// DriveConfig holds Google Drive folder IDs
type DriveConfig struct {
	StockRootFolderID string `yaml:"stock_root_folder" env:"VELOX_STOCK_ROOT_FOLDER" default:"1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh"`
	ClipsRootFolderID string `yaml:"clips_root_folder" env:"VELOX_CLIPS_ROOT_FOLDER" default:"root"`
	ArtlistFolderID   string `yaml:"artlist_folder" env:"VELOX_ARTLIST_FOLDER" default:"1ktDuzVYvA1xfpja78VAEWt9KhwsthPod"`
	BoxeFolderID      string `yaml:"boxe_folder" env:"VELOX_BOXE_FOLDER" default:"14HWILTg8L9ST0bnorgmHzZknel9buJjb"`
	CrimineFolderID   string `yaml:"crimine_folder" env:"VELOX_CRIMINE_FOLDER" default:"1KhJ6bSty9r4EP_2gVpTzz4BWdKhsI0pG"`
	DiscoveryFolderID string `yaml:"discovery_folder" env:"VELOX_DISCOVERY_FOLDER" default:"11-O6LvlcL0Hj_ktiUOJDnpPYerSpWNiW"`
	WweFolderID       string `yaml:"wwe_folder" env:"VELOX_WWE_FOLDER" default:"1_7U8yEeQZEH7vxgDIRketFL85F96O_Ws"`
	HipHopFolderID    string `yaml:"hiphop_folder" env:"VELOX_HIPHOP_FOLDER" default:"16D3qvbv3Y4TlNahQ3sWq6N7ITgwWm6DD"`
	MusicaFolderID    string `yaml:"musica_folder" env:"VELOX_MUSICA_FOLDER" default:"1_PQj7fok1UEzzQgTnUcTP3FHnZBnwv9t"`
}

// SchedulerConfig holds StockJob Scheduler configuration
type SchedulerConfig struct {
	Interval       int      `yaml:"interval" env:"VELOX_SCHEDULER_INTERVAL" default:"86400"` // seconds (24h)
	SearchQueries  []string `yaml:"search_queries" env:"VELOX_SCHEDULER_QUERIES" default:"[]"`
	MaxResults     int      `yaml:"max_results" env:"VELOX_SCHEDULER_MAX_RESULTS" default:"10"`
	MinDurationSec int      `yaml:"min_duration_sec" env:"VELOX_SCHEDULER_MIN_DURATION" default:"30"`
	MaxDurationSec int      `yaml:"max_duration_sec" env:"VELOX_SCHEDULER_MAX_DURATION" default:"300"`
}

// DriveSyncConfig holds DriveSync configuration
type DriveSyncConfig struct {
	Interval    int `yaml:"interval" env:"VELOX_DRIVESYNC_INTERVAL" default:"86400"` // seconds (24h)
	SyncTimeout int `yaml:"sync_timeout" env:"VELOX_DRIVESYNC_TIMEOUT" default:"600"` // seconds (10m)
}

// ClipApprovalConfig holds clip approval workflow configuration
type ClipApprovalConfig struct {
	MinScore             float64 `yaml:"min_score" env:"VELOX_CLIP_MIN_SCORE" default:"20.0"`
	MaxClipsPerScene     int     `yaml:"max_clips_per_scene" env:"VELOX_CLIP_MAX_PER_SCENE" default:"5"`
	AutoApproveThreshold float64 `yaml:"auto_approve_threshold" env:"VELOX_CLIP_AUTO_APPROVE" default:"85.0"`
}

// ClipIndexConfig holds clip index scanner configuration
type ClipIndexConfig struct {
	ScanInterval int `yaml:"scan_interval" env:"VELOX_CLIP_SCAN_INTERVAL" default:"3600"` // seconds (1h)
}

// TextGenConfig holds text generator configuration
type TextGenConfig struct {
	DefaultModel string `yaml:"default_model" env:"VELOX_TEXTGEN_MODEL" default:"gemma3:4b"`
	Timeout      int    `yaml:"timeout" env:"VELOX_TEXTGEN_TIMEOUT" default:"60"` // seconds
}

// GetStockFolders returns all Stock category folders as a map of ID
func (d *DriveConfig) GetStockFolders() map[string]string {
	return map[string]string{
		"boxe":      d.BoxeFolderID,
		"crimine":   d.CrimineFolderID,
		"discovery": d.DiscoveryFolderID,
		"wwe":       d.WweFolderID,
		"hiphop":    d.HipHopFolderID,
		"musica":    d.MusicaFolderID,
		"artlist":   d.ArtlistFolderID,
	}
}

// StockFolderEntry represents a Stock folder with full metadata
type StockFolderEntry struct {
	ID   string
	Name string
	URL  string
}

// GetStockFolderEntries returns Stock folders with full metadata
func (d *DriveConfig) GetStockFolderEntries() map[string]StockFolderEntry {
	folders := d.GetStockFolders()
	result := make(map[string]StockFolderEntry, len(folders))
	names := map[string]string{
		"boxe": "Stock/Boxe", "crimine": "Stock/Crimine", "discovery": "Stock/Discovery",
		"wwe": "Stock/Wwe", "hiphop": "Stock/HipHop", "musica": "Stock/Musica", "artlist": "Stock/Artlist",
	}
	for k, id := range folders {
		name := names[k]
		result[k] = StockFolderEntry{
			ID:   id,
			Name: name,
			URL:  fmt.Sprintf("https://drive.google.com/drive/folders/%s", id),
		}
	}
	return result
}

var (
	instance *Config
	once     sync.Once
)

// Get returns the singleton Config instance
func Get() *Config {
	once.Do(func() {
		instance = load()
	})
	return instance
}

// Reload forces a configuration reload
func Reload() *Config {
	instance = load()
	return instance
}

// load loads configuration from environment and config file
func load() *Config {
	cfg := &Config{}

	// Set defaults first
	setDefaults(cfg)

	// Load from config file if exists
	configPath := getEnv("VELOX_CONFIG", "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err == nil {
			yaml.Unmarshal(data, cfg)
		}
	}

	// Override with environment variables
	loadFromEnv(cfg)

	return cfg
}

func setDefaults(cfg *Config) {
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = 8080
	cfg.Server.ReadTimeout = 600
	cfg.Server.WriteTimeout = 600
	cfg.Server.GinMode = "release"

	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"

	cfg.Storage.DataDir = "./data"
	cfg.Storage.QueueFile = "queue.json"
	cfg.Storage.WorkersFile = "workers.json"
	cfg.Storage.BackupDir = "./backups"
	cfg.Storage.AutoSaveInterval = 60

	cfg.Jobs.MaxParallelPerProject = 2
	cfg.Jobs.LeaseTTLSeconds = 300
	cfg.Jobs.MaxRetries = 3
	cfg.Jobs.AutoCleanupHours = 72
	cfg.Jobs.ZombieCheckInterval = 120
	cfg.Jobs.CleanupInterval = 3600

	cfg.Workers.HeartbeatTimeout = 120
	cfg.Workers.WorkerFailWindowSeconds = 300
	cfg.Workers.WorkerFailThreshold = 5
	cfg.Workers.MaxWorkerLogs = 100
	cfg.Workers.MaxWorkerErrors = 15
	cfg.Workers.AutoDrainThresholdGB = 5.0
	cfg.Workers.AutoRepairCooldownSeconds = 600

	cfg.Security.EnableAuth = false
	cfg.Security.CORSOrigins = []string{"*"}
	cfg.Security.RateLimitEnabled = true
	cfg.Security.RateLimitRequests = 100

	cfg.External.OllamaURL = "http://localhost:11434"

	cfg.Paths.TempDir = "/tmp/velox"
	cfg.Paths.VoiceoverDir = "/tmp/velox/voiceovers"
	cfg.Paths.VideoWorkDir = "/tmp/velox/video"
	cfg.Paths.StockDir = "/tmp/velox"
	cfg.Paths.DownloadDir = "/tmp/velox/downloads"
	cfg.Paths.YouTubeDir = "/tmp/velox/youtube"
	cfg.Paths.EffectsDir = "/tmp/velox/effects"
	cfg.Paths.OutputDir = "/tmp/velox/output"
	cfg.Paths.WhisperDir = "/tmp/whisper"
	cfg.Paths.CredentialsFile = "credentials.json"
	cfg.Paths.TokenFile = "token.json"
	cfg.Paths.ClipRootFolder = "root"
	cfg.Paths.VideoStockCreatorBinary = "video-stock-creator"
	cfg.Paths.ArtlistDBPath = "" // Empty by default, will check common locations
	cfg.Paths.YtDlpPath = "yt-dlp" // Look up in PATH by default

	cfg.Scraper.Dir = "../../src/node-scraper"
	cfg.Scraper.NodeBin = "node"

	// Scheduler defaults
	cfg.Scheduler.Interval = 86400 // 24h
	cfg.Scheduler.MaxResults = 10
	cfg.Scheduler.MinDurationSec = 30
	cfg.Scheduler.MaxDurationSec = 300

	// DriveSync defaults
	cfg.DriveSync.Interval = 86400 // 24h
	cfg.DriveSync.SyncTimeout = 600 // 10m

	// Clip approval defaults
	cfg.ClipApproval.MinScore = 20.0
	cfg.ClipApproval.MaxClipsPerScene = 5
	cfg.ClipApproval.AutoApproveThreshold = 85.0

	// Clip index defaults
	cfg.ClipIndex.ScanInterval = 3600 // 1h

	// Text gen defaults
	cfg.TextGen.DefaultModel = "gemma3:4b"
	cfg.TextGen.Timeout = 60
}

func loadFromEnv(cfg *Config) {
	// Server
	setStringFromEnv(&cfg.Server.Host, "VELOX_HOST")
	setIntFromEnv(&cfg.Server.Port, "VELOX_PORT")
	setIntFromEnv(&cfg.Server.ReadTimeout, "VELOX_READ_TIMEOUT")
	setIntFromEnv(&cfg.Server.WriteTimeout, "VELOX_WRITE_TIMEOUT")
	setStringFromEnv(&cfg.Server.GinMode, "GIN_MODE")

	// Logging
	setStringFromEnv(&cfg.Logging.Level, "VELOX_LOG_LEVEL")
	setStringFromEnv(&cfg.Logging.Format, "VELOX_LOG_FORMAT")

	// Storage
	setStringFromEnv(&cfg.Storage.DataDir, "VELOX_DATA_DIR")
	setStringFromEnv(&cfg.Storage.QueueFile, "VELOX_QUEUE_FILE")
	setStringFromEnv(&cfg.Storage.WorkersFile, "VELOX_WORKERS_FILE")

	// Jobs
	setIntFromEnv(&cfg.Jobs.MaxParallelPerProject, "VELOX_MAX_PARALLEL_PER_PROJECT")
	setIntFromEnv(&cfg.Jobs.LeaseTTLSeconds, "VELOX_LEASE_TTL_SECONDS")
	setIntFromEnv(&cfg.Jobs.MaxRetries, "VELOX_MAX_RETRIES")
	setIntFromEnv(&cfg.Jobs.AutoCleanupHours, "VELOX_AUTO_CLEANUP_HOURS")

	// Workers
	setIntFromEnv(&cfg.Workers.HeartbeatTimeout, "VELOX_HEARTBEAT_TIMEOUT")
	setIntFromEnv(&cfg.Workers.MaxWorkerLogs, "VELOX_MAX_WORKER_LOGS")

	// Security
	setStringFromEnv(&cfg.Security.AdminToken, "VELOX_ADMIN_TOKEN")
	setBoolFromEnv(&cfg.Security.EnableAuth, "VELOX_ENABLE_AUTH")

	// External
	setStringFromEnv(&cfg.External.YouTubeAPIBaseURL, "VELOX_YOUTUBE_API_URL")
	setStringFromEnv(&cfg.External.WorkerBackendURL, "VELOX_WORKER_BACKEND")
	setStringFromEnv(&cfg.External.OllamaURL, "OLLAMA_ADDR")

	// Paths
	setStringFromEnv(&cfg.Paths.TempDir, "VELOX_TEMP_DIR")
	setStringFromEnv(&cfg.Paths.VoiceoverDir, "VELOX_VOICEOVER_DIR")
	setStringFromEnv(&cfg.Paths.VideoWorkDir, "VELOX_VIDEO_WORK_DIR")
	setStringFromEnv(&cfg.Paths.StockDir, "VELOX_STOCK_DIR")
	setStringFromEnv(&cfg.Paths.DownloadDir, "VELOX_DOWNLOAD_DIR")
	setStringFromEnv(&cfg.Paths.YouTubeDir, "VELOX_YOUTUBE_DIR")
	setStringFromEnv(&cfg.Paths.EffectsDir, "VELOX_EFFECTS_DIR")
	setStringFromEnv(&cfg.Paths.OutputDir, "VELOX_OUTPUT_DIR")
	setStringFromEnv(&cfg.Paths.WhisperDir, "VELOX_WHISPER_DIR")
	setStringFromEnv(&cfg.Paths.CredentialsFile, "VELOX_CREDENTIALS_FILE")
	setStringFromEnv(&cfg.Paths.TokenFile, "VELOX_TOKEN_FILE")
	setStringFromEnv(&cfg.Paths.ClipRootFolder, "VELOX_CLIP_ROOT_FOLDER")
	setStringFromEnv(&cfg.Paths.VideoStockCreatorBinary, "VELOX_VIDEO_STOCK_CREATOR_BINARY")
	setStringFromEnv(&cfg.Paths.ArtlistDBPath, "VELOX_ARTLIST_DB_PATH")
	setStringFromEnv(&cfg.Paths.YtDlpPath, "VELOX_YTDLP_PATH")

	// Drive
	setStringFromEnv(&cfg.Drive.StockRootFolderID, "VELOX_STOCK_ROOT_FOLDER")
	setStringFromEnv(&cfg.Drive.ClipsRootFolderID, "VELOX_CLIPS_ROOT_FOLDER")
	setStringFromEnv(&cfg.Drive.ArtlistFolderID, "VELOX_ARTLIST_FOLDER")
	setStringFromEnv(&cfg.Drive.BoxeFolderID, "VELOX_BOXE_FOLDER")
	setStringFromEnv(&cfg.Drive.CrimineFolderID, "VELOX_CRIMINE_FOLDER")
	setStringFromEnv(&cfg.Drive.DiscoveryFolderID, "VELOX_DISCOVERY_FOLDER")
	setStringFromEnv(&cfg.Drive.WweFolderID, "VELOX_WWE_FOLDER")
	setStringFromEnv(&cfg.Drive.HipHopFolderID, "VELOX_HIPHOP_FOLDER")
	setStringFromEnv(&cfg.Drive.MusicaFolderID, "VELOX_MUSICA_FOLDER")

	// Scraper
	setStringFromEnv(&cfg.Scraper.Dir, "VELOX_SCRAPER_DIR")
	setStringFromEnv(&cfg.Scraper.NodeBin, "VELOX_NODE_BIN")

	// Scheduler
	setIntFromEnv(&cfg.Scheduler.Interval, "VELOX_SCHEDULER_INTERVAL")
	setIntFromEnv(&cfg.Scheduler.MaxResults, "VELOX_SCHEDULER_MAX_RESULTS")
	setIntFromEnv(&cfg.Scheduler.MinDurationSec, "VELOX_SCHEDULER_MIN_DURATION")
	setIntFromEnv(&cfg.Scheduler.MaxDurationSec, "VELOX_SCHEDULER_MAX_DURATION")

	// DriveSync
	setIntFromEnv(&cfg.DriveSync.Interval, "VELOX_DRIVESYNC_INTERVAL")
	setIntFromEnv(&cfg.DriveSync.SyncTimeout, "VELOX_DRIVESYNC_TIMEOUT")

	// Clip Approval
	setFloat64FromEnv(&cfg.ClipApproval.MinScore, "VELOX_CLIP_MIN_SCORE")
	setIntFromEnv(&cfg.ClipApproval.MaxClipsPerScene, "VELOX_CLIP_MAX_PER_SCENE")
	setFloat64FromEnv(&cfg.ClipApproval.AutoApproveThreshold, "VELOX_CLIP_AUTO_APPROVE")

	// Clip Index
	setIntFromEnv(&cfg.ClipIndex.ScanInterval, "VELOX_CLIP_SCAN_INTERVAL")

	// Text Gen
	setStringFromEnv(&cfg.TextGen.DefaultModel, "VELOX_TEXTGEN_MODEL")
	setIntFromEnv(&cfg.TextGen.Timeout, "VELOX_TEXTGEN_TIMEOUT")
}

func setStringFromEnv(target *string, envKey string) {
	if v := os.Getenv(envKey); v != "" {
		*target = v
	}
}

func setIntFromEnv(target *int, envKey string) {
	if v := os.Getenv(envKey); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			*target = i
		}
	}
}

func setBoolFromEnv(target *bool, envKey string) {
	if v := os.Getenv(envKey); v != "" {
		*target = strings.ToLower(v) == "true" || v == "1"
	}
}

func setFloat64FromEnv(target *float64, envKey string) {
	if v := os.Getenv(envKey); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*target = f
		}
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// GetDataPath returns the full path for a data file
func (c *Config) GetDataPath(filename string) string {
	return filepath.Join(c.Storage.DataDir, filename)
}

// GetQueuePath returns the full path for the queue file
func (c *Config) GetQueuePath() string {
	return c.GetDataPath(c.Storage.QueueFile)
}

// GetWorkersPath returns the full path for the workers file
func (c *Config) GetWorkersPath() string {
	return c.GetDataPath(c.Storage.WorkersFile)
}

// GetVoiceoverDir returns the voiceover output directory
func (c *Config) GetVoiceoverDir() string {
	return c.Paths.VoiceoverDir
}

// GetVideoWorkDir returns the video processing working directory
func (c *Config) GetVideoWorkDir() string {
	return c.Paths.VideoWorkDir
}

// GetStockDir returns the stock video management directory
func (c *Config) GetStockDir() string {
	return c.Paths.StockDir
}

// GetDownloadDir returns the download output directory
func (c *Config) GetDownloadDir() string {
	return c.Paths.DownloadDir
}

// GetYouTubeDir returns the YouTube integration directory
func (c *Config) GetYouTubeDir() string {
	return c.Paths.YouTubeDir
}

// GetEffectsDir returns the effects definitions directory
func (c *Config) GetEffectsDir() string {
	return c.Paths.EffectsDir
}

// GetOutputDir returns the default output directory for finished videos
func (c *Config) GetOutputDir() string {
	return c.Paths.OutputDir
}

// GetWhisperDir returns the Whisper transcription output directory
func (c *Config) GetWhisperDir() string {
	return c.Paths.WhisperDir
}

// GetCredentialsPath returns the full path to the Google OAuth credentials file
func (c *Config) GetCredentialsPath() string {
	return c.Paths.CredentialsFile
}

// GetTokenPath returns the full path to the Google OAuth token file
func (c *Config) GetTokenPath() string {
	return c.Paths.TokenFile
}

// GetClipRootFolder returns the Google Drive root folder ID for clip management
func (c *Config) GetClipRootFolder() string {
	return c.Paths.ClipRootFolder
}

// GetVideoStockCreatorBinary returns the path to the video-stock-creator binary
func (c *Config) GetVideoStockCreatorBinary() string {
	return c.Paths.VideoStockCreatorBinary
}

// GetArtlistDBPath returns the path to the Artlist SQLite database
func (c *Config) GetArtlistDBPath() string {
	return c.Paths.ArtlistDBPath
}

// GetYtDlpPath returns the path to the yt-dlp binary
func (c *Config) GetYtDlpPath() string {
	return c.Paths.YtDlpPath
}

// GetLogLevel returns the configured log level
func (c *Config) GetLogLevel() string {
	return c.Logging.Level
}

// GetLogFormat returns the configured log format
func (c *Config) GetLogFormat() string {
	return c.Logging.Format
}

// GetOutputPath returns the full path for an output video file by name
func (c *Config) GetOutputPath(name string) string {
	safe := name
	if safe == "" {
		safe = "output"
	}
	return filepath.Join(c.Paths.OutputDir, safe+".mp4")
}

// Save saves the current configuration to a YAML file
func (c *Config) Save(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}