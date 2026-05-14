package main

import (
	"fmt"
	"os"
	"strings"
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
		"cleanup-orphans":               runCleanupOrphans,
		"cleanup-all-orphans":           runCleanupAllOrphans,
		"cleanup-artlist-empty-folders": runCleanupArtlistEmptyFolders,
		"cleanup-stock-orphans":         runCleanupStockOrphans,
		"delete-specific-folders":       runDeleteSpecificFolders,
		"sync-all-drive":                runSyncAllDrive,
		"test-youtube":                  runTestYouTube,
		"verify-artlist-pipeline":       runVerifyArtlistPipeline,
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
	fmt.Fprintln(os.Stderr, "  cleanup-orphans")
	fmt.Fprintln(os.Stderr, "  cleanup-all-orphans")
	fmt.Fprintln(os.Stderr, "  cleanup-artlist-empty-folders")
	fmt.Fprintln(os.Stderr, "  cleanup-stock-orphans")
	fmt.Fprintln(os.Stderr, "  delete-specific-folders")
	fmt.Fprintln(os.Stderr, "  sync-all-drive")
	fmt.Fprintln(os.Stderr, "  test-youtube")
	fmt.Fprintln(os.Stderr, "  verify-artlist-pipeline")
}
