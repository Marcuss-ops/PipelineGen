// Package images — external-integration configuration types.
//
// PR4.D (June 2026): these struct types replace the loose scalar args in
// NewService for the optional NVIDIA + Google Accounting integrations. Empty
// values disable the corresponding path (Diagnostics().NvidiaConfigured
// reports false when APIKey is unset, etc.).
//
// The other ctor args of NewService (cfg, repo, stock repo, drive client,
// style registry, media store, llm generator, vector store, metadata writer,
// log) remain flat scalars because they each represent a single, unambiguous
// dependency. The two config structs here bundle related options so the
// NewService signature stays readable as the list of integrations grows.

package images

// NvidiaConfig is the optional NVIDIA AI image generation configuration.
// All fields are optional — empty values disable the corresponding path.
type NvidiaConfig struct {
	// APIKey is the NVIDIA NGC API key. Empty disables NVIDIA image
	// generation entirely (see Service.Diagnostics().NvidiaConfigured).
	APIKey string
	// Model is the NVIDIA-hosted model identifier (e.g.
	// "stabilityai/stable-diffusion-xl"). Empty falls back to the
	// service-level default or skips the NVIDIA integration.
	Model string
}

// GoogleAccountingConfig is the optional Google Accounting server
// integration used by AI image generation for asset attribution and
// Drive-aware placement (the GA server records which output belongs to
// which Vids project).
type GoogleAccountingConfig struct {
	// ServerURL is the base URL of the Google Accounting service that
	// records AI-generated asset attribution. Empty disables GA wiring.
	ServerURL string
	// DownloadDir is the local directory where GA-fetched assets are
	// staged before Drive upload. Empty falls back to a temp path.
	DownloadDir string
	// VidsProjectID is the Google Vids project identifier embedded in
	// GA attribution payloads. Empty disables project attribution.
	VidsProjectID string
}
