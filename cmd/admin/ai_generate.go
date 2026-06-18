package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runListStyles(args []string) error {
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	core, coreCleanup, err := app.InitCore(cfg, log)
	if err != nil {
		return err
	}
	defer coreCleanup()

	if core.StyleRegistry == nil {
		return fmt.Errorf("style registry not available")
	}

	styles := core.StyleRegistry.List()
	fmt.Printf("Available Styles (%d):\n", len(styles))
	for _, s := range styles {
		fmt.Printf("- %-15s: %s\n", s.Name, s.Description)
	}
	return nil
}

func runGenerateAvatar(args []string) error {
	return fmt.Errorf("avatar generation not implemented")
}

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

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	core, coreCleanup, err := app.InitCore(cfg, log)
	if err != nil {
		return err
	}
	defer coreCleanup()

	ctx := context.Background()
	fmt.Printf("Generating AI Video for prompt: %q (style: %s)...\n", *prompt, *style)
	
	videoPath, err := core.ImageService.GenerateVideoAI(ctx, *prompt, *style)
	if err != nil {
		return fmt.Errorf("AI video generation failed: %w", err)
	}

	fmt.Printf("AI Video generated successfully at: %s\n", videoPath)
	return nil
}
