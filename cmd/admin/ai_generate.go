package main

import (
	"flag"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
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

func runGenerateAvatar(args []string) error {
	return fmt.Errorf("avatar generation not implemented (admin CLI pending post-PR4 admin loader)")
}

// runGenerateAIVideo is a stub. Pre-PR4 it called
// core.ImageService.GenerateVideoAI on the legacy app.InitCore surface.
// Post-PR4 the composition root reconstructs the image service stack
// inside NewComposition.BuildDomainBundle (an opinionated chain that
// does not include a video-generation entry-point). Reaching into the
// composition root from cmd/admin requires a dedicated admin loader
// (analogous to gen_api_docs.go's app.WireServices usage) — a follow-up
// PR will reintroduce this functionality on that path. The stub keeps
// the build green + honest about the gap (CLI parses flags, surfaces a
// clear "not implemented" error so callers see the situation).
func runGenerateAIVideo(args []string) error {
	fs := flag.NewFlagSet("generate-ai-video", flag.ContinueOnError)
	prompt := fs.String("prompt", "", "Prompt for the video")
	style := fs.String("style", "", "Style to apply")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *prompt == "" {
		return fmt.Errorf("prompt is required")
	}

	fmt.Printf("AI video generation stub (post-PR4): prompt=%q style=%s — not yet re-linked to app.NewComposition's image/video services\n", *prompt, *style)
	return fmt.Errorf("generate-ai-video: not implemented (post-PR4 admin re-link pending)")
}
