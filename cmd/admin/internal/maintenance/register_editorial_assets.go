package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	admindrive "github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/drive"
	wiringmedia "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
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
// The registered hash is NOT copied from the catalog. Every plate is
// materialized through the canonical content-addressed materializer first — a
// registered local fixture, an already-verified cached copy, or a fresh Drive
// download — and the digest of the bytes that were actually read is what gets
// committed. A digest that disagrees with the catalog is a hard error: the
// render resolver compares the registered hash against the file it hashes, so
// a copied-but-unverified hash would surface as a clip.render failure instead.
//
// The row is written exclusively through persistence.AssetCommitter
// (see wiring/media.EnsureEditorialAssets): this command holds no SQL against
// media_assets.
//
// Idempotent: a re-run upserts on media_assets.id and re-verifies the bytes,
// refreshing the certified hash / Drive identity instead of duplicating.
func RunRegisterEditorialAssets(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("register-editorial-assets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	platesDir := fs.String("plates-dir", "",
		"directory holding the certified plate fixtures (default $"+wiringmedia.EditorialBackgroundsDirEnv+"); a missing fixture is not the authority — the plate is re-verified or fetched from Drive")
	platesFlag := fs.String("plates", "",
		"comma-separated canonical plate aliases to register (default: the whole catalog); an alias the catalog does not own is an error")
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

	// The materializer can only certify bytes it can read, so a working Drive
	// reader is mandatory: the fixture directory is an optimization and plates
	// 02/03/04/06 are not checked in at all.
	reader, err := admindrive.BuildDriveAdminForCLI(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("register-editorial-assets: Drive reader (required to materialize and verify plate bytes): %w", err)
	}
	materializer, err := drive.NewCanonicalAssetMaterializer(reader, filepath.Join(cfg.Storage.TempPath(), "editorial-assets"), log)
	if err != nil {
		return fmt.Errorf("register-editorial-assets: canonical asset materializer: %w", err)
	}

	dir := strings.TrimSpace(*platesDir)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv(wiringmedia.EditorialBackgroundsDirEnv))
	}

	registrations, err := wiringmedia.EnsureEditorialAssets(ctx, db, materializer, wiringmedia.EditorialAssetsOptions{
		PlateIDs:  cli.SplitCSV(*platesFlag),
		PlatesDir: dir,
	}, log)
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
