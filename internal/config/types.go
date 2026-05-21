// Package config provides configuration management for the PipelineGen system.
package config

import (
	"path/filepath"
	"sync"
)

// Config holds all configuration for the application.
type Config struct {
	mu sync.RWMutex

	Server      ServerConfig      `yaml:"server"`
	Logging     LoggingConfig     `yaml:"logging"`
	Storage     StorageConfig     `yaml:"storage"`
	Security    SecurityConfig    `yaml:"security"`
	External    ExternalConfig    `yaml:"external"`
	Paths       PathsConfig       `yaml:"paths"`
	Drive       DriveConfig       `yaml:"drive"`
	Harvester   HarvesterConfig   `yaml:"harvester"`
	Jobs        JobsConfig        `yaml:"jobs"`
	Workers     WorkersConfig     `yaml:"workers"`
	Video       VideoConfig       `yaml:"video"`
	Features    FeaturesConfig    `yaml:"features"`
	ClipIndexer ClipIndexerConfig `yaml:"clip_indexer"`
	GoogleAccounting GoogleAccountingConfig `yaml:"google_accounting"`
}

// GoogleAccountingConfig holds settings for the Google Accounting FastAPI service.
type GoogleAccountingConfig struct {
	Enabled      bool   `yaml:"enabled" default:"false"`
	ServerURL    string `yaml:"server_url" default:"http://localhost:8000"`
	DownloadDir  string `yaml:"download_dir" default:"./data/google_vids"`
	SessionsPath string `yaml:"sessions_path" default:"./google-accounting/sessions"`
	ScheduleCron string `yaml:"schedule_cron" default:"0 2 * * *"`
}

// VideoConfig holds all video processing parameters shared across the clip, stock,
// and tachyon rendering pipelines. Centralizing these values ensures that every
// stage uses the same codec, resolution, and preset so that ffmpeg can perform
// fast stream-copy concatenation without re-encoding.
type VideoConfig struct {
	Width             int      `yaml:"width" default:"1920"`
	Height            int      `yaml:"height" default:"1080"`
	FPS               int      `yaml:"fps" default:"30"`
	Codec             string   `yaml:"codec" default:"h264_nvenc"`
	Preset            string   `yaml:"preset" default:"p1"`
	CRF               int      `yaml:"crf" default:"23"`
	Duration          int      `yaml:"duration" default:"7"`
	KeyframeInterval  int      `yaml:"keyframe_interval" default:"60"`
	AudioCodec        string   `yaml:"audio_codec" default:"aac"`
	AudioBitrate      string   `yaml:"audio_bitrate" default:"128k"`
	ClipDuration      int      `yaml:"clip_duration" default:"5"`
	ChunkDuration     int      `yaml:"chunk_duration" default:"25"`
	MaxClipsPerSource int      `yaml:"max_clips_per_source" default:"30"`
	SearchCount       int      `yaml:"search_count" default:"25"`
	OverlayOpacity    float64  `yaml:"overlay_opacity" default:"0.25"`
	EffectInterval    int      `yaml:"effect_interval" default:"4"`
	TransitionPresets []string `yaml:"transition_presets" default:"[\"fade\",\"fadeblack\",\"fadewhite\",\"slideleft\",\"slideright\",\"slideup\",\"slidedown\",\"circleclose\",\"circleopen\",\"horzclose\",\"horzopen\",\"vertclose\",\"vertopen\",\"dissolve\",\"pixelize\",\"wipeleft\",\"wiperight\",\"wipeup\",\"wipedown\",\"smoothleft\",\"smoothright\",\"smoothup\",\"smoothdown\",\"radial\",\"hblur\",\"fadegrays\",\"squeezeh\",\"squeezev"]"`
}

// WithDefaults returns a copy of VideoConfig with zero-values replaced by defaults.
func (v VideoConfig) WithDefaults() VideoConfig {
	if v.Width <= 0 {
		v.Width = 1920
	}
	if v.Height <= 0 {
		v.Height = 1080
	}
	if v.FPS <= 0 {
		v.FPS = 30
	}
	if v.Duration <= 0 {
		v.Duration = 7
	}
	if v.Codec == "" {
		v.Codec = "h264_nvenc"
	}
	if v.Preset == "" {
		v.Preset = "p1"
	}
	if v.CRF <= 0 {
		v.CRF = 23
	}
	if v.KeyframeInterval <= 0 {
		v.KeyframeInterval = 60
	}
	if v.AudioCodec == "" {
		v.AudioCodec = "aac"
	}
	if v.AudioBitrate == "" {
		v.AudioBitrate = "128k"
	}
	if v.ClipDuration <= 0 {
		v.ClipDuration = 5
	}
	if v.ChunkDuration <= 0 {
		v.ChunkDuration = 25
	}
	if v.MaxClipsPerSource <= 0 {
		v.MaxClipsPerSource = 30
	}
	if v.SearchCount <= 0 {
		v.SearchCount = 25
	}
	// Note: OverlayOpacity == 0 is valid (no overlay), so we only check < 0
	if v.OverlayOpacity < 0 {
		v.OverlayOpacity = 0.25
	}
	// Note: EffectInterval == 0 is valid (no effects), so we only check < 0
	if v.EffectInterval < 0 {
		v.EffectInterval = 4
	}
	if len(v.TransitionPresets) == 0 {
		v.TransitionPresets = []string{
			"fade", "fadeblack", "fadewhite",
			"slideleft", "slideright", "slideup", "slidedown",
			"circleclose", "circleopen",
			"horzclose", "horzopen", "vertclose", "vertopen",
			"dissolve", "pixelize",
			"wipeleft", "wiperight", "wipeup", "wipedown",
			"smoothleft", "smoothright", "smoothup", "smoothdown",
			"radial", "hblur", "fadegrays",
			"squeezeh", "squeezev",
		}
	}
	return v
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
	// ImagesRootFolder is the root folder ID for image files on Drive.
	ImagesRootFolder string `yaml:"images_root_folder" env:"VELOX_DRIVE_IMAGES_ROOT" default:""`
	// VoiceoverRootFolder is the root folder ID for voiceover files on Drive.
	VoiceoverRootFolder string `yaml:"voiceover_root_folder" env:"VELOX_DRIVE_VOICEOVER_ROOT" default:""`
	// ClipRootFolders maps group names to Drive folder IDs for clip organization.
	// Example: map[string]string{"group1": "DRIVE_ID_1", "group2": "DRIVE_ID_2"}
	ClipRootFolders map[string]string `yaml:"clip_root_folders" env:"VELOX_DRIVE_CLIP_ROOT_FOLDERS" default:"{}"`
}

// LoggingConfig holds logger-specific configuration.
type LoggingConfig struct {
	Level  string `yaml:"level" env:"VELOX_LOG_LEVEL" default:"info"`
	Format string `yaml:"format" env:"VELOX_LOG_FORMAT" default:"json"`
}

// ServerConfig holds server-specific configuration.
type ServerConfig struct {
	Host         string `yaml:"host" env:"VELOX_HOST" default:"127.0.0.1"`
	Port         int    `yaml:"port" env:"VELOX_PORT" default:"8080"`
	ReadTimeout  int    `yaml:"read_timeout" env:"VELOX_READ_TIMEOUT" default:"600"`
	WriteTimeout int    `yaml:"write_timeout" env:"VELOX_WRITE_TIMEOUT" default:"600"`
	GinMode      string `yaml:"gin_mode" env:"GIN_MODE" default:"release"`
}

// StorageConfig holds storage configuration.
type StorageConfig struct {
	DataDir         string `yaml:"data_dir" env:"VELOX_DATA_DIR" default:"./data"`
	VoiceoversDir   string `yaml:"voiceovers_dir" env:"VELOX_VOICEOVERS_DIR" default:"voiceovers"`
	AssetsDir       string `yaml:"assets_dir" env:"VELOX_ASSETS_DIR" default:"assets/subjects"`
	DownloadsDir    string `yaml:"downloads_dir" env:"VELOX_DOWNLOADS_DIR" default:"downloads"`
	BackupsDir      string `yaml:"backups_dir" env:"VELOX_BACKUPS_DIR" default:"backups"`
	TempDir         string `yaml:"temp_dir" env:"VELOX_TEMP_DIR" default:"tmp"`
	AnimationsDir   string `yaml:"animations_dir" default:"animations"`
	YoutubeClipsDir string `yaml:"youtube_clips_dir" default:"youtube-clips"`
	ArtlistDir      string `yaml:"artlist_dir" default:"artlist"`
	ImagesDir       string `yaml:"images_dir" default:"images"`
	StockDBPath     string `yaml:"stock_db_path" env:"VELOX_STOCK_DB_PATH" default:"stock/stock.db.sqlite"`
	ArtlistDBPath   string `yaml:"artlist_db_path" env:"VELOX_ARTLIST_DB_PATH" default:"artlist/artlist.db.sqlite"`
}

const (
	defaultStockDBPath   = "stock/stock.db.sqlite"
	defaultArtlistDBPath = "artlist/artlist.db.sqlite"
)

// FullPath returns the absolute path to a subdirectory within DataDir.
func (s StorageConfig) FullPath(subDir string) string {
	if filepath.IsAbs(subDir) {
		return subDir
	}
	return filepath.Join(s.DataDir, subDir)
}

// VoiceoversPath returns the full path to the voiceovers directory.
func (s StorageConfig) VoiceoversPath() string {
	return s.FullPath(s.VoiceoversDir)
}

// AssetsPath returns the full path to the main assets directory.
func (s StorageConfig) AssetsPath() string {
	return s.FullPath(s.AssetsDir)
}

// DownloadsPath returns the full path to the downloads directory.
func (s StorageConfig) DownloadsPath() string {
	return s.FullPath(s.DownloadsDir)
}

// BackupsPath returns the full path to the backups directory.
func (s StorageConfig) BackupsPath() string {
	return s.FullPath(s.BackupsDir)
}

// TempPath returns the full path to the temporary directory.
func (s StorageConfig) TempPath() string {
	return s.FullPath(s.TempDir)
}

// AnimationsPath returns the full path to the animations directory.
func (s StorageConfig) AnimationsPath() string {
	return s.FullPath(s.AnimationsDir)
}

// YoutubeClipsPath returns the full path to the youtube clips directory.
func (s StorageConfig) YoutubeClipsPath() string {
	return s.FullPath(s.YoutubeClipsDir)
}

// ArtlistPath returns the full path to the artlist directory.
func (s StorageConfig) ArtlistPath() string {
	return s.FullPath(s.ArtlistDir)
}

// ImagesPath returns the full path to the images directory.
func (s StorageConfig) ImagesPath() string {
	return s.FullPath(s.ImagesDir)
}

// StockDBFullPath returns the full path to the stock SQLite database.
func (s StorageConfig) StockDBFullPath() string {
	path := s.StockDBPath
	if path == "" {
		path = defaultStockDBPath
	}
	return s.FullPath(path)
}

// ArtlistDBFullPath returns the full path to the artlist SQLite database.
func (s StorageConfig) ArtlistDBFullPath() string {
	path := s.ArtlistDBPath
	if path == "" {
		path = defaultArtlistDBPath
	}
	return s.FullPath(path)
}

// SecurityConfig holds security-related configuration.
type SecurityConfig struct {
	AdminToken           string   `yaml:"admin_token" env:"VELOX_ADMIN_TOKEN" default:""`
	WorkerToken          string   `yaml:"worker_token" env:"VELOX_WORKER_TOKEN" default:""`
	EnableAuth           bool     `yaml:"enable_auth" env:"VELOX_ENABLE_AUTH" default:"true"`
	CORSOrigins          []string `yaml:"cors_origins" env:"VELOX_CORS_ORIGINS" default:"[]"`
	RateLimitEnabled     bool     `yaml:"rate_limit_enabled" env:"VELOX_RATE_LIMIT_ENABLED" default:"true"`
	RateLimitRequests    int      `yaml:"rate_limit_requests" env:"VELOX_RATE_LIMIT_REQUESTS" default:"100"`
	AllowedDownloadHosts []string `yaml:"allowed_download_hosts" env:"VELOX_ALLOWED_DOWNLOAD_HOSTS" default:"[]"`
}

// ExternalConfig holds external service configuration.
type ExternalConfig struct {
	OllamaURL            string          `yaml:"ollama_url" env:"OLLAMA_ADDR" default:"http://localhost:11434"`
	OllamaModel          string          `yaml:"ollama_model" env:"OLLAMA_MODEL" default:"gemma3:4b"`
	OllamaTimeoutSeconds int             `yaml:"ollama_timeout_seconds" env:"OLLAMA_TIMEOUT" default:"120"`
	YtdlpPath            string          `yaml:"ytdlp_path" env:"YTDLP_PATH" default:"yt-dlp"`
	FfmpegPath           string          `yaml:"ffmpeg_path" env:"FFMPEG_PATH" default:"ffmpeg"`
	NvidiaAPIKey         string          `yaml:"nvidia_api_key" env:"NVIDIA_API_KEY" default:""`
	NvidiaModel          string          `yaml:"nvidia_model" env:"NVIDIA_MODEL" default:"stabilityai/sdxl-turbo"`
	SketchfabConfig      SketchfabConfig `yaml:"sketchfab"`
}

// SketchfabConfig holds Sketchfab API configuration.
type SketchfabConfig struct {
	APIToken string `yaml:"api_token" env:"SKETCHFAB_API_TOKEN" default:""`
}

// PathsConfig holds the few filesystem paths still used by the minimal server.
type PathsConfig struct {
	CredentialsFile  string `yaml:"credentials_file" env:"VELOX_CREDENTIALS_FILE" default:"credentials.json"`
	TokenFile        string `yaml:"token_file" env:"VELOX_TOKEN_FILE" default:"token.json"`
	ClipTextDir      string `yaml:"clip_text_dir" env:"VELOX_CLIP_TEXT_DIR" default:""`
	PythonScriptsDir string `yaml:"python_scripts_dir" env:"VELOX_PYTHON_SCRIPTS_DIR" default:"scripts"`
	WorkflowsDir     string `yaml:"workflows_dir" env:"VELOX_WORKFLOWS_DIR" default:"./workflows"`
}

// JobsConfig holds job-related configuration.
type JobsConfig struct {
	NewJobsPaused         bool   `yaml:"new_jobs_paused" default:"false"`
	LeaseTTLSeconds       int    `yaml:"lease_ttl_seconds" default:"300"`
	MaxParallelPerProject int    `yaml:"max_parallel_per_project" default:"16"`
	AutoCleanupHours      int    `yaml:"auto_cleanup_hours" default:"24"`
	CatalogSyncInterval   string `yaml:"catalog_sync_interval" env:"VELOX_CATALOG_SYNC_INTERVAL" default:"6h"`
	MaintenanceInterval   string `yaml:"maintenance_interval" default:"24h"`
	BackupInterval        string `yaml:"backup_interval" default:"6h"`
	IndexingInterval      string `yaml:"indexing_interval" default:"15m"`
	RetentionDays         int    `yaml:"retention_days" env:"VELOX_RETENTION_DAYS" default:"30"`
}

// WorkersConfig holds worker-related configuration.
type WorkersConfig struct {
	AllowedIPs              []string `yaml:"allowed_ips" default:"[]"`
	HeartbeatTimeout        int      `yaml:"heartbeat_timeout" default:"30"`
	WorkerFailWindowSeconds int      `yaml:"worker_fail_window_seconds" default:"300"`
	WorkerFailThreshold     int      `yaml:"worker_fail_threshold" default:"3"`
}

// FeaturesConfig controls optional modules.
// Stable modules default to true only if their dependencies are available.
// Experimental modules default to false.
type FeaturesConfig struct {
	ArtlistEnabled     bool `yaml:"artlist_enabled" env:"VELOX_FEATURE_ARTLIST_ENABLED" default:"false"`
	YouTubeEnabled     bool `yaml:"youtube_enabled" env:"VELOX_FEATURE_YOUTUBE_ENABLED" default:"false"`
	DriveEnabled       bool `yaml:"drive_enabled" env:"VELOX_FEATURE_DRIVE_ENABLED" default:"false"`
	ScriptDocsEnabled  bool `yaml:"script_docs_enabled" env:"VELOX_FEATURE_SCRIPT_DOCS_ENABLED" default:"false"`
	ScriptClipsEnabled bool `yaml:"script_clips_enabled" env:"VELOX_FEATURE_SCRIPT_CLIPS_ENABLED" default:"false"`
	VoiceoverEnabled   bool `yaml:"voiceover_enabled" env:"VELOX_FEATURE_VOICEOVER_ENABLED" default:"false"`
	WorkflowEnabled          bool `yaml:"workflow_enabled" env:"VELOX_FEATURE_WORKFLOW_ENABLED" default:"false"`
	ImagesEnabled            bool `yaml:"images_enabled" env:"VELOX_FEATURE_IMAGES_ENABLED" default:"false"`
	SketchfabEnabled         bool `yaml:"sketchfab_enabled" env:"VELOX_FEATURE_SKETCHFAB_ENABLED" default:"false"`
	StockPipelineEnabled     bool `yaml:"stock_pipeline_enabled" env:"VELOX_FEATURE_STOCK_PIPELINE_ENABLED" default:"false"`
	GoogleAccountingEnabled  bool `yaml:"google_accounting_enabled" env:"VELOX_FEATURE_GOOGLE_ACCOUNTING_ENABLED" default:"false"`
}

// ClipIndexerConfig holds settings for the clip metadata indexing service.
type ClipIndexerConfig struct {
	Enabled               bool   `yaml:"enabled" default:"true"`
	ServerURL             string `yaml:"server_url" default:"http://127.0.0.1:8001"`
	ScriptPath            string `yaml:"script_path" default:"scripts/index_clips.py"`
	PythonBin             string `yaml:"python_bin" default:"python3"`
	AutoIndexAfterArtlist bool   `yaml:"auto_index_after_artlist" default:"true"`
}
