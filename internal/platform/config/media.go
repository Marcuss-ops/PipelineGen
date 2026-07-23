package config

// MediaConfig groups all media-pipeline configuration (multilingual
// settings, text-track acquisition, future media-runtime knobs) under a
// single namespace so the Config struct stays tree-readable. The
// nested Multilingual field mirrors the top-level Multilingual on
// Config (kept for back-compat with the pre-Fase-1.b callers); both
// shapes carry the same canonical MultilingualConfig so YAML drift
// between media.multilingual.* and multilingual.* keys is a
// follow-up concern, not a blocker for the Go-level wiring.
//
// godlike/06 SSOT: this struct is the canonical namespace for media-
// pipeline configuration. New media-runtime knobs (subtitle format,
// transcript format, evidence-support toggles, etc.) MUST be added
// here, not as flat top-level fields on Config.
type MediaConfig struct {
	// Multilingual holds the canonical BCP-47-driven language
	// policy for the YouTube acquisition chain. The top-level
	// Config.Multilingual field is retained for back-compat; this
	// nested field is the SSOT path for new callers
	// (buildDomainMediaServices consumes cfg.Media.Multilingual.*).
	Multilingual MultilingualConfig `yaml:"multilingual"`
}
