package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	wiringmedia "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
)

// RunRegisterEditorialAssets registers the curated editorial background plates
// (internal/capabilities/mediaregistry/editorial_backgrounds.go) in the
// PostgreSQL media SSOT under their canonical alias.
//
// clip.render addresses a plate by that alias and fails closed unless the
// registered content hash is the certified normalized-plate hash, so this is
// the operation that makes a NEW deployment (or a freshly recreated E2E
// database) able to resolve a plate at all — previously the only way was a
// hand-written INSERT that could register the wrong bytes.
//
// Idempotent: a re-run upserts on media_assets.id and only refreshes the
// certified hash / Drive identity.
func RunRegisterEditorialAssets(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("register-editorial-assets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	platesDir := fs.String("plates-dir", "",
		"directory holding the certified plate fixtures (default $"+wiringmedia.EditorialBackgroundsDirEnv+"); when absent the plate is registered without a local copy and fetched from Drive at render time")
	jsonOutput := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	dsn := strings.TrimSpace(cfg.MediaPostgreSQL.DSN)
	if dsn == "" {
		return fmt.Errorf("register-editorial-assets: the PostgreSQL media DSN is empty (set PIPELINEGEN_MEDIA_POSTGRES_DSN)")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("register-editorial-assets: open media postgres: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("register-editorial-assets: ping media postgres: %w", err)
	}

	dir := strings.TrimSpace(*platesDir)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv(wiringmedia.EditorialBackgroundsDirEnv))
	}

	registrations, err := wiringmedia.EnsureEditorialAssets(ctx, db, dir, log)
	if err != nil {
		return err
	}

	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"registered": len(registrations),
			"assets":     registrations,
		})
	}
	fmt.Printf("registered %d editorial background plates\n", len(registrations))
	for _, r := range registrations {
		local := r.LocalPath
		if local == "" {
			local = "(drive)"
		}
		fmt.Printf("  %-20s %s  %s\n", r.AssetID, r.SHA256[:12], local)
	}
	return nil
}
