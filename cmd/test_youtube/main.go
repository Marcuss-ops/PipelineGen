package main

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"velox/go-master/internal/bootstrap"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"
)

func main() {
	cfg := config.Get()
	if err := cfg.Validate(); err != nil {
		fmt.Printf("Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.GetLogLevel(), cfg.GetLogFormat())
	log := logger.Get()
	defer logger.Sync()

	deps, err := bootstrap.WireServices(cfg, log, "")
	if err != nil {
		log.Error("Failed to wire services", zap.Error(err))
		os.Exit(1)
	}

	fmt.Println("Services wired successfully!")
	fmt.Printf("YouTubeClip handler: %v\n", deps.Handlers.YouTubeClip != nil)

	if deps.Cleanup != nil {
		defer deps.Cleanup()
	}
}
