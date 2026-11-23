package script

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// InitLinguistics loads and installs the configured lexicon registry, failing
// closed when any required language profile is missing.
func InitLinguistics(cfg *config.Config, log *zap.Logger) error {
	if cfg == nil {
		return fmt.Errorf("init linguistics: config is nil")
	}

	profiles, err := linguistics.NewProfiles()
	if err != nil {
		return fmt.Errorf("init linguistics: load generated Rust lexicon profiles: %w", err)
	}
	for _, lang := range cfg.Linguistics.RequiredLanguages {
		if !profiles.HasProfile(lang) {
			return fmt.Errorf("init linguistics: required language profile %q missing from the generated Rust snapshot", lang)
		}
	}
	if err := linguistics.SetDefaultProfiles(profiles); err != nil {
		return fmt.Errorf("init linguistics: install default registry: %w", err)
	}
	// internal/kernel cannot import internal/capabilities, so the kernel-side
	// progress sub-kind filter receives the stop words by injection instead of
	// mirroring the lexicon as a hardcoded map.
	job.SetProgressStopWords(linguistics.DefaultStopWords())
	if log != nil {
		log.Info("linguistics: Rust-generated lexicon profiles loaded",
			zap.String("source", profiles.Source()),
			zap.String("schema_version", profiles.SchemaVersion()),
			zap.String("version", profiles.Version()),
			zap.Int("profiles", profiles.ProfileCount()),
			zap.Strings("required_languages", cfg.Linguistics.RequiredLanguages),
		)
	}
	return nil
}
