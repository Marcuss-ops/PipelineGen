package soundeffects

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

// runRenameIndexedSoundEffects makes the Drive display names match the
// professional filenames already assigned by the sound-effect indexer.
// It is intentionally opt-in because it changes external Drive state.
func RunRenameIndexedSoundEffects(args []string) error {
	fs := flag.NewFlagSet("rename-indexed-sound-effects", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	apply := fs.Bool("apply", false, "apply the Drive renames; without this flag only preview changes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.Drive == nil || root.Drive.Admin == nil {
		return fmt.Errorf("database and Drive admin are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	rows, err := root.DB.DB.QueryContext(ctx, `
		SELECT COALESCE(drive_file_id, ''), COALESCE(name, ''), COALESCE(filename, '')
		FROM media_assets
		WHERE source='sound_effect' AND COALESCE(drive_file_id, '') <> ''
		ORDER BY created_at ASC`)
	if err != nil {
		return fmt.Errorf("query indexed sound effects: %w", err)
	}
	defer rows.Close()

	changed := 0
	for rows.Next() {
		var driveID, currentName, targetName string
		if err := rows.Scan(&driveID, &currentName, &targetName); err != nil {
			return fmt.Errorf("scan indexed sound effect: %w", err)
		}
		targetName = strings.TrimSpace(targetName)
		if targetName == "" || targetName == strings.TrimSpace(currentName) {
			continue
		}
		fmt.Printf("%s -> %s\n", strings.TrimSpace(currentName), targetName)
		changed++
		if *apply {
			if err := root.Drive.Admin.RenameFile(ctx, driveID, targetName); err != nil {
				return fmt.Errorf("rename Drive file %s to %q: %w", driveID, targetName, err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate indexed sound effects: %w", err)
	}
	mode := "preview"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("Indexed sound-effect Drive renames %s: %d\n", mode, changed)
	return nil
}
