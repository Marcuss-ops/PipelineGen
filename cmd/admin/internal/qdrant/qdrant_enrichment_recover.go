package qdrant

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/enrichment"
	qrecovery "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/recovery"
	mediaenrichment "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaenrichment"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

func RunQdrantEnrichmentRecover(args []string) error {
	fs := flag.NewFlagSet("qdrant-enrichment-recover", flag.ContinueOnError)
	collections := fs.String("collections", "", "comma-separated historical Qdrant collections")
	language := fs.String("language", "en", "language code for recovered text")
	limit := fs.Int("limit", 0, "maximum active clips to inspect; 0 means all")
	apply := fs.Bool("apply", false, "persist recovered tracks and enqueue targeted reindex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*collections) == "" {
		return fmt.Errorf("--collections is required; dry-run is the default")
	}
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return err
	}
	defer rootCleanup()
	if root.DB == nil || root.DB.DB == nil || root.Repos == nil || root.Repos.TextTrackRepo == nil || root.Process == nil || root.Process.QdrantClient == nil {
		return fmt.Errorf("qdrant-enrichment-recover: canonical DB, text-track repo and Qdrant client are required")
	}
	reader, err := qrecovery.NewReader(root.Process.QdrantClient, strings.Split(*collections, ","), 500)
	if err != nil {
		return err
	}
	rows, err := root.DB.DB.QueryContext(context.Background(), `SELECT id FROM media_assets WHERE lifecycle_state NOT IN ('DELETED','TOMBSTONED') AND media_type='clip' ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	assetIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		assetIDs = append(assetIDs, id)
		if *limit > 0 && len(assetIDs) >= *limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	assets, err := mediaenrichment.NewAssetReader(root.DB.DB)
	if err != nil {
		return err
	}
	trackReader, err := mediaenrichment.NewTextTrackReader(root.Repos.TextTrackRepo)
	if err != nil {
		return err
	}
	matched := 0
	if !*apply {
		for _, id := range assetIDs {
			h, e := reader.FindByAssetID(context.Background(), id)
			if e != nil {
				return e
			}
			if h != nil && (strings.TrimSpace(h.Description) != "" || strings.TrimSpace(h.SearchText) != "") {
				matched++
			}
		}
		fmt.Printf("QDRANT ENRICHMENT RECOVERY DRY-RUN\nactive_clips=%d\nhistorical_matches=%d\napply=false\n", len(assetIDs), matched)
		return nil
	}
	committer, err := mediaenrichment.NewRecoveryCommitter(root.DB.DB, outboxevents.NewRepository(root.DB.DB), "media_assets_current")
	if err != nil {
		return err
	}
	svc, err := enrichment.NewRecoveryService(assets, reader, trackReader, committer)
	if err != nil {
		return err
	}
	recovered, skipped := 0, 0
	for _, id := range assetIDs {
		result, e := svc.RecoverAsset(context.Background(), id, *language)
		if e != nil {
			return fmt.Errorf("asset %s: %w", id, e)
		}
		recovered += result.Recovered
		skipped += result.SkippedBetter
	}
	fmt.Printf("QDRANT ENRICHMENT RECOVERY\nprocessed=%d\nrecovered_tracks=%d\nskipped_existing=%d\ntargeted_reindex=true\n", len(assetIDs), recovered, skipped)
	return nil
}
