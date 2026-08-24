// Package types — shared YouTube types: RuntimeConfig isolates the YouTube
// application layer from infrastructure/config. All config values consumed
// by the YouTube package tree are resolved at composition time
// (internal/app/composition.go) and injected via ServiceDeps so the
// application layer has zero dependency on `internal/platform/config`.
package dto

// RuntimeConfig holds all configuration values consumed by the YouTube
// application layer. Every field is a flat, resolved value — composition
// resolves nested config paths (cfg.External, cfg.Concurrency, etc.) into
// this struct once at startup.
//
// Fields are declared zero-value-safe so a zero RuntimeConfig{} is a
// valid (fallback-defaults) configuration: a missing or partially-wired
// composition root produces implicit defaults (1 for concurrency, "gemma4:e2b"
// for Ollama model) at first use.
type RuntimeConfig struct {
	// Concurrency limits (default 1).
	MaxConcurrentVideoExtracts int
	MaxConcurrentOllamaCalls   int

	// Job/extraction timeouts (seconds).
	YouTubeExtractTimeout int

	// Paths resolved at composition time.
	DataDir                  string // cfg.Storage.DataDir
	YtdlpPath                string // cfg.External.ResolvedYtdlpPath()
	ClipsFolderID            string // cfg.Drive.ClipsFolder()
	YouTubeSubtitlesFolderID string // cfg.Drive.YouTubeSubtitlesFolder()

	// Ollama model selection (default "gemma4:e2b").
	OllamaModel         string
	OllamaMetadataModel string

	// YouTube client configuration.
	YouTubeCookiesPath   string
	YouTubeJSRuntimePath string
	YouTubeEnabled       bool
}
