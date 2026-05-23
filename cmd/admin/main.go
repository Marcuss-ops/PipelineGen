package main

import (
	"fmt"
	"os"
	"strings"

	"velox/go-master/internal/admin/googleaccounting"
)

type commandFunc func(args []string) error

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmdName := normalizeCommand(os.Args[1])
	args := os.Args[2:]

	commands := map[string]commandFunc{
		"backfill-hash":                 runBackfillHash,
		"backfill-hash-v2":              runBackfillHashV2,
		"backfill-asset-index":          runBackfillAssetIndex,
		"backfill-asset-tree":           runBackfillAssetTree,
		"google-generate-flow-images":   googleaccounting.RunGenerateFlowImages,
		"google-generate-video":         googleaccounting.RunGenerateVideo,
		"google-publish":                googleaccounting.RunPublish,
		"google-upload-media":           googleaccounting.RunUploadMedia,
		"cleanup-orphans":               runCleanupOrphans,
		"cleanup-all-orphans":           runCleanupAllOrphans,
		"cleanup-artlist-empty-folders": runCleanupArtlistEmptyFolders,
		"cleanup-stock-orphans":         runCleanupStockOrphans,
		"delete-specific-folders":       runDeleteSpecificFolders,
		"sync-all-drive":                runSyncAllDrive,
		"test-youtube":                  runTestYouTube,
		"verify-artlist-pipeline":       runVerifyArtlistPipeline,
		"unify-catalogs":               runUnifyCatalogs,
		"reset-stock-drive":            runResetStockDrive,
		"reset-stock-subfolders":       runResetStockSubfolders,
	}

	fn, ok := commands[cmdName]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown admin command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err := fn(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func normalizeCommand(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: admin <command> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  backfill-hash")
	fmt.Fprintln(os.Stderr, "  backfill-hash-v2")
	fmt.Fprintln(os.Stderr, "  backfill-asset-index")
	fmt.Fprintln(os.Stderr, "  backfill-asset-tree")
	fmt.Fprintln(os.Stderr, "  google-generate-flow-images")
	fmt.Fprintln(os.Stderr, "  google-generate-video")
	fmt.Fprintln(os.Stderr, "  google-publish")
	fmt.Fprintln(os.Stderr, "  google-upload-media")
	fmt.Fprintln(os.Stderr, "  cleanup-orphans")
	fmt.Fprintln(os.Stderr, "  cleanup-all-orphans")
	fmt.Fprintln(os.Stderr, "  cleanup-artlist-empty-folders")
	fmt.Fprintln(os.Stderr, "  cleanup-stock-orphans")
	fmt.Fprintln(os.Stderr, "  delete-specific-folders")
	fmt.Fprintln(os.Stderr, "  sync-all-drive")
	fmt.Fprintln(os.Stderr, "  test-youtube")
	fmt.Fprintln(os.Stderr, "  verify-artlist-pipeline")
	fmt.Fprintln(os.Stderr, "  unify-catalogs")
	fmt.Fprintln(os.Stderr, "  reset-stock-drive")
	fmt.Fprintln(os.Stderr, "  reset-stock-subfolders")
}
