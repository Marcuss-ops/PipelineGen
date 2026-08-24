package wiring

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// initLinguistics loads the per-language lexicon registry from the
// configured lexicon root, fails fast if any required language profile is
// missing, and installs it as the package-level default.
func initLinguistics(cfg *config.Config, log *zap.Logger) error {
	if cfg == nil {
		return fmt.Errorf("init linguistics: config is nil")
	}

	root := cfg.Linguistics.LexiconRoot
	reg, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		return fmt.Errorf("init linguistics: load lexicon registry from %q: %w", root, err)
	}

	for _, lang := range cfg.Linguistics.RequiredLanguages {
		if !reg.HasProfile(lang) {
			return fmt.Errorf("init linguistics: required language profile %q missing in lexicon root %q", lang, root)
		}
	}

	if err := linguistics.SetDefaultLexicon(reg); err != nil {
		return fmt.Errorf("init linguistics: install default registry: %w", err)
	}

	if log != nil {
		log.Info("linguistics: lexicon registry loaded",
			zap.String("root", root),
			zap.Int("profiles", reg.ProfileCount()),
			zap.Strings("required_languages", cfg.Linguistics.RequiredLanguages),
		)
	}

	return nil
}
