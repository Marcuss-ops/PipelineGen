package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: admin <command> [args]")
		fmt.Println("Commands: seed-channels, stock-reset, stock-subfolders-reset, summarize-book, sync-outros, unify-catalogs, ai-generate, backfill-missing")
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "seed-channels":
		err = runSeedChannels(args)
	case "stock-reset":
		err = runResetStockDrive(args)
	case "stock-subfolders-reset":
		err = runResetStockSubfolders(args)
	case "summarize-book":
		err = runSummarizeBook(args)
	case "sync-outros":
		err = runSyncOutros(args)
	case "unify-catalogs":
		err = runUnifyCatalogs(args)
	case "ai-generate":
		err = runListStyles(args) // fallback/default
	case "generate-avatar":
		err = runGenerateAvatar(args)
	case "generate-ai-video":
		err = runGenerateAIVideo(args)
	case "backfill-missing":
		err = runBackfillMissing(args)
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}

	if err != nil {
		fmt.Printf("Error running command %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func appLogger() (*config.Config, *zap.Logger, func(), error) {
	log, err := zap.NewDevelopment()
	if err != nil {
		return nil, nil, nil, err
	}
	cfg := config.Get()
	cleanup := func() {
		_ = log.Sync()
	}
	return cfg, log, cleanup, nil
}

func cmdContext() context.Context {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
