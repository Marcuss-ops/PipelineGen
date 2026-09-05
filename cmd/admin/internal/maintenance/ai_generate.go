package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"fmt"

	imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// runListStyles enumerates the canonical generation_styles.yaml registry.
// Loading the YAML directly keeps this read-only admin command independent
// from the full runtime composition graph.
//
// Step 3 (July 2026): enriched output shows version, providers, destination
// key, and enabled status for each style.
func RunListStyles(args []string) error {
	_, _, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	styleRegistry, regErr := imagestyles.NewStyleRegistry("config/generation_styles.yaml")
	if regErr != nil || styleRegistry == nil {
		return fmt.Errorf("style registry not available: %w", regErr)
	}

	styles := styleRegistry.List()
	fmt.Printf("Available Styles (%d total):\n", len(styles))

	enabledCount := 0
	for _, s := range styles {
		status := "enabled"
		// Step-1 typed migration (A1, July 2026): the *bool
		// tri-state IsEnabled() method was retired along with
		// the legacy 14-field struct. The plain `Enabled bool`
		// field replaces it (silent-flip absent → false; the
		// existing config/generation_styles.yaml pins enabled
		// explicitly on every entry, so production is transparent).
		if !s.Enabled {
			status = "DISABLED"
		} else {
			enabledCount++
		}

		fmt.Printf("  %-18s v%-2d [%s] → %s\n", s.Name, s.Version, status, s.DestinationKey)
		fmt.Printf("    prompt:     %s\n", textutil.Truncate(s.PromptSuffix, 80))
		// Step-1 typed migration (A1, July 2026): the per-style
		// AllowedProviders check was retired in surface-3 and the
		// field itself was removed in A1. Hard-coded static
		// statement below matches the same canonical provider
		// surface.
		fmt.Printf("    providers:  google-slides (only)\n")
		if s.NegativePrompt != "" {
			fmt.Printf("    negative:   %s\n", textutil.Truncate(s.NegativePrompt, 80))
		}
		fmt.Println()
	}

	fmt.Printf("\nEnabled: %d / Disabled: %d\n", enabledCount, len(styles)-enabledCount)
	return nil
}
