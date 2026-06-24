package main

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
)

// runListStyles enumerates the canonical generation_styles.yaml registry.
// Pre-PR4 it loaded styles via the now-removed app.InitCore's
// core.StyleRegistry field. Post-PR4 the composition root
// (app.NewComposition) is the canonical entry-point, but cmd/admin has
// no convenient entry-point yet — until a dedicated admin loader lands,
// loading the YAML directly via generation.NewStyleRegistry mirrors the
// top of composition.go::BuildDriveBundle:
//   - same import path: internal/media/generation
//   - same YAML file:   config/generation_styles.yaml
//   - same error semantics: file missing / parse error
//
// Once app.LoadAdminCore exists, this site should re-link rather than
// duplicate the parse (PR-administrative dedup chore).
func runListStyles(args []string) error {
	_, _, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	styleRegistry, regErr := generation.NewStyleRegistry("config/generation_styles.yaml")
	if regErr != nil || styleRegistry == nil {
		return fmt.Errorf("style registry not available: %w", regErr)
	}

	styles := styleRegistry.List()
	fmt.Printf("Available Styles (%d):\n", len(styles))
	for _, s := range styles {
		fmt.Printf("- %-15s: %s\n", s.Name, s.Description)
	}
	return nil
}
