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
	// Sampler carries the Go-owned selection policy for the Rust
	// mediasampler: the DATA-DRIVEN keyword tables (replacing the worker's
	// curated single-domain defaults wholesale when supplied) and the
	// optional diversity weight. An empty policy keeps the Rust V1
	// curated behaviour byte-for-byte, so an unconfigured operator sees
	// no behavioral change.
	Sampler SamplerPolicyConfig `yaml:"sampler"`
}

// SamplerPolicyConfig is the operator-facing selection policy for the
// media sampler. Tables arrive lowercased in Rust (case-insensitive
// matching); an empty list means "keep the worker's default table".
// DiversityWeight must stay in [0.0, 0.1] (the stage's bounded blend
// weight); zero means "unspecified" and the worker's default applies.
type SamplerPolicyConfig struct {
	MismatchedSubjects []string `yaml:"mismatched_subjects"`
	CompatibleKeywords []string `yaml:"compatible_keywords"`
	ActionVerbs        []string `yaml:"action_verbs"`
	ContextKeywords    []string `yaml:"context_keywords"`
	DiversityWeight    float64  `yaml:"diversity_weight"`
}
