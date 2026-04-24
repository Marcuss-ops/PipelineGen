package config

import (
	"os"
	"path/filepath"
)

func resolveRelativePath(path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	fallback := filepath.Join("src/go-master", path)
	if _, err := os.Stat(fallback); err == nil {
		return fallback
	}
	return path
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
	return resolveRelativePath(c.Paths.CredentialsFile)
}

// GetTokenPath returns the full path to the Google OAuth token file
func (c *Config) GetTokenPath() string {
	return resolveRelativePath(c.Paths.TokenFile)
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
