package main

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
)

// runListStyles enumerates the canonical generation_styles.yaml registry.
// Loading the YAML directly keeps this read-only admin command independent
// from the full runtime composition graph.
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
