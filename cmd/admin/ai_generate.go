package main

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// runListStyles enumerates the canonical generation_styles.yaml registry.
// Loading the YAML directly keeps this read-only admin command independent
// from the full runtime composition graph.
//
// Step 3 (July 2026): enriched output shows version, providers, destination
// key, and enabled status for each style.
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
	fmt.Printf("Available Styles (%d total):\n", len(styles))

	enabledCount := 0
	for _, s := range styles {
		status := "enabled"
		if !s.IsEnabled() {
			status = "DISABLED"
		} else {
			enabledCount++
		}

		providerList := "any"
		if len(s.AllowedProviders) > 0 {
			providerList = fmt.Sprintf("%v", s.AllowedProviders)
		}

		fmt.Printf("  %-18s v%-2d [%s] → %s\n", s.Name, s.Version, status, s.DestinationKey)
		fmt.Printf("    prompt:     %s\n", textutil.Truncate(s.EffectiveSuffix(), 80))
		fmt.Printf("    providers:  %s\n", providerList)
		if s.NegativePrompt != "" {
			fmt.Printf("    negative:   %s\n", textutil.Truncate(s.NegativePrompt, 80))
		}
		if len(s.Tags) > 0 {
			fmt.Printf("    tags:       %v\n", s.Tags)
		}
		fmt.Println()
	}

	fmt.Printf("\nEnabled: %d / Disabled: %d\n", enabledCount, len(styles)-enabledCount)
	return nil
}
