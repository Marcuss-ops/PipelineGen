package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// runClassifySoundEffects backfills semantic SFX taxonomy and re-emits the
// canonical asset.index.requested event for every catalog file. Existing
// broad categories and tags are preserved; the richer fields are additive.
func runClassifySoundEffects(args []string) error {
	fs := flag.NewFlagSet("classify-sound-effects", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "show classifications without writing or reindexing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.Repos == nil || root.Repos.ClipsRepo == nil {
		return fmt.Errorf("database and clips repository are required")
	}
	if !*dryRun && (root.Outbox == nil || root.Outbox.Dispatcher == nil) {
		return fmt.Errorf("outbox dispatcher is required unless --dry-run is used")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	rows, err := root.DB.DB.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE source='sound_effect' AND media_type='sound_effect' AND category='file'
		ORDER BY name, id`)
	if err != nil {
		return fmt.Errorf("list sound effects: %w", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan sound effect: %w", err)
		}
		clip, err := root.Repos.ClipsRepo.GetClip(ctx, id)
		if err != nil || clip == nil {
			return fmt.Errorf("load sound effect %s: %w", id, err)
		}
		oldFamily := clip.GetMetadataString("sfx_category")
		taxonomy := asset.ClassifySoundEffect(clip.Name, oldFamily, clip.Tags)
		searchText := strings.Join([]string{
			clip.Name,
			taxonomy.Family,
			taxonomy.Subtype,
			taxonomy.Mood,
			taxonomy.Energy,
			strings.Join(taxonomy.BestFor, " "),
			strings.Join(taxonomy.Tags, " "),
		}, " ")

		if *dryRun {
			fmt.Printf("%s -> %s/%s mood=%s energy=%s\n", clip.Name, taxonomy.Family, taxonomy.Subtype, taxonomy.Mood, taxonomy.Energy)
			continue
		}

		clip.Tags = taxonomy.Tags
		clip.SearchText = searchText
		clip.SetMetadataString("sfx_family", taxonomy.Family)
		clip.SetMetadataString("sfx_subtype", taxonomy.Subtype)
		clip.SetMetadataString("sfx_mood", taxonomy.Mood)
		clip.SetMetadataString("sfx_energy", taxonomy.Energy)
		clip.SetMetadataString("sfx_best_for", strings.Join(taxonomy.BestFor, ","))
		clip.SetMetadataString("sfx_tags", strings.Join(taxonomy.Tags, ","))
		if oldFamily == "" || oldFamily == "file" {
			clip.SetMetadataString("sfx_category", taxonomy.Family)
		}
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.LegacyFileMD5()); err != nil {
			return fmt.Errorf("reindex sound effect %s: %w", id, err)
		}
		updated++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sound effects: %w", err)
	}
	if *dryRun {
		return nil
	}
	fmt.Printf("Sound effect taxonomy applied and reindex requested: %d\n", updated)
	return nil
}
