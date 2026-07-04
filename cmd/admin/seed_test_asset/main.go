// cmd/admin/seed_test_asset/main.go — seed_test_asset CLI entry point.
//
// Standalone binary invoked as: go run ./cmd/admin/seed_test_asset/
// Or as:    ./bin/admin seed-test-asset (after BuildAll)
//
// godlike/07 NO-FAKE-AVAILABILITY: exits non-zero (1) on any failure;
// the typed sentinel errors from seed.go are surfaced verbatim on stderr
// for caller errors.Is traversal.
//
// godlike/07 minimum-blast-radius: --list prints the seed plan and exits 0
// without touching the network (for CI dry-runs and operator pre-checks).
//
// godlike/07 typed-error contract: 4 sentinels (ErrSeedStackDown /
// ErrSeedHTTPFailed / ErrSeedIndexTimeout / ErrSeedQdrantNotSynced) are
// forwarded verbatim to the caller via fmt.Errorf %w chains.
//
// Forward-pointer (per architecture/current.yaml#PR-QDRANT-PREFLIGHT-DATA-SEED):
// per-test fill-in PRs (Tests 3-8 + 10) consume this CLI's stdout JSON
// to populate preflightDeps.SeedAssetID / SeedJobID / SeedVOAssetID.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	fs := flag.NewFlagSet("seed-test-asset", flag.ExitOnError)
	urlFlag := fs.String("url", "http://127.0.0.1:8081", "PipelineGen server URL")
	qdrantFlag := fs.String("qdrant-url", "http://127.0.0.1:16333", "Qdrant base URL")
	collectionFlag := fs.String("collection", "media_assets_current", "Qdrant canonical collection alias")
	tokenFlag := fs.String("admin-token", "", "Admin auth token (or set VELOX_ADMIN_TOKEN env)")
	timeoutFlag := fs.Duration("timeout", 2*time.Minute, "Max wait for index_state=INDEXED")
	pollFlag := fs.Duration("poll", 2*time.Second, "Poll interval for index_state check")
	assetNameFlag := fs.String("asset-name", "preflight-test-asset", "Human-readable identifier for the seed asset")
	voAssetFlag := fs.String("vo-asset", "", "Optional vo-asset-id for Test 11 voiceover piggyback")
	listOnlyFlag := fs.Bool("list", false, "Print the seed plan and exit (no network calls)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "seed-test-asset: flag parse: %v\n", err)
		os.Exit(1)
	}

	if *listOnlyFlag {
		printPlan(*urlFlag, *qdrantFlag, *collectionFlag, *assetNameFlag, *voAssetFlag)
		return
	}

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("VELOX_ADMIN_TOKEN")
	}
	if token == "" {
		fmt.Fprintf(os.Stderr, "seed-test-asset: admin token required (--admin-token=... or set VELOX_ADMIN_TOKEN env)\n")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	result, err := Run(ctx, SeedDeps{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Config: SeedConfig{
			URL:        *urlFlag,
			QdrantURL:  *qdrantFlag,
			Collection: *collectionFlag,
			AdminToken: token,
			Timeout:    *timeoutFlag,
			PollEvery:  *pollFlag,
			AssetName:  *assetNameFlag,
			VOAssetID:  *voAssetFlag,
		},
	})
	if err != nil {
		// Per godlike/07 typed-error contract: print the sentinel verbatim
		// so the caller can errors.Is the error.
		fmt.Fprintf(os.Stderr, "seed-test-asset: %v\n", err)
		os.Exit(1)
	}
	// Emit JSON to stdout for the preflight binary to consume.
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "seed-test-asset: encode result: %v\n", err)
		os.Exit(1)
	}
}

func printPlan(url, qdrantURL, collection, assetName, voAsset string) {
	fmt.Println("seed-test-asset plan (dry-run, no network):")
	fmt.Printf("  url:             %s\n", url)
	fmt.Printf("  qdrant-url:      %s\n", qdrantURL)
	fmt.Printf("  collection:      %s\n", collection)
	fmt.Printf("  asset-name:      %s\n", assetName)
	if voAsset != "" {
		fmt.Printf("  vo-asset:        %s\n", voAsset)
	}
	fmt.Println("  payload: 1 sandbox clip with text content for Tests 3-8 + 10")
}
