package soundeffects

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

const soundEffectsDriveFolderID = "1vfZQHVNZab-pU2fBaj4qzR3iSz1sOVhW"

type soundEffectDriveAsset struct {
	Family string
}

// runOrganizeSoundEffectsDrive mirrors the repo taxonomy in Drive folders.
// It only moves files that are direct children of the supplied root; existing
// subfolders are left intact, making the command safe to run repeatedly.
func RunOrganizeSoundEffectsDrive(args []string) error {
	fs := flag.NewFlagSet("organize-sound-effects-drive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	rootFolder := fs.String("folder-id", soundEffectsDriveFolderID, "Drive root folder containing sound effects")
	apply := fs.Bool("apply", false, "create family folders and move files")
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
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil || root.DB == nil {
		return fmt.Errorf("Drive reader, admin and database are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	classification, err := loadSoundEffectDriveClassification(ctx, root.DB.DB)
	if err != nil {
		return err
	}
	files, err := root.Drive.Reader.ListFiles(ctx, strings.TrimSpace(*rootFolder))
	if err != nil {
		return fmt.Errorf("list Drive sound-effect root: %w", err)
	}

	counts := make(map[string]int)
	type move struct {
		file   drive.DriveFileInfo
		family string
	}
	moves := make([]move, 0, len(files))
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			continue
		}
		family := classification[file.ID].Family
		if family == "" {
			family = classification[file.Name].Family
		}
		if family == "" {
			family = asset.ClassifySoundEffect(file.Name, "", nil).Family
		}
		if family == "" {
			family = "misc"
		}
		counts[family]++
		moves = append(moves, move{file: file, family: family})
	}

	keys := make([]string, 0, len(counts))
	for family := range counts {
		keys = append(keys, family)
	}
	sort.Strings(keys)
	fmt.Printf("Drive SFX root=%s direct_files=%d\n", *rootFolder, len(moves))
	for _, family := range keys {
		fmt.Printf("  %-18s %d\n", family, counts[family])
	}
	if !*apply {
		fmt.Println("Dry run only. Re-run with --apply to create folders and move files.")
		return nil
	}

	folders := make(map[string]string, len(keys))
	for _, family := range keys {
		folderID, err := root.Drive.Admin.GetOrCreateFolder(ctx, displaySoundEffectFamily(family), *rootFolder)
		if err != nil {
			return fmt.Errorf("ensure Drive folder for %s: %w", family, err)
		}
		folders[family] = folderID
	}
	moved := 0
	for _, item := range moves {
		if err := root.Drive.Admin.MoveFile(ctx, item.file.ID, *rootFolder, folders[item.family]); err != nil {
			return fmt.Errorf("move %q to %s: %w", item.file.Name, item.family, err)
		}
		moved++
	}
	fmt.Printf("Drive SFX organization complete: folders=%d moved=%d\n", len(folders), moved)
	return nil
}

func loadSoundEffectDriveClassification(ctx context.Context, db *sql.DB) (map[string]soundEffectDriveAsset, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT drive_file_id, name, metadata_json
		FROM media_assets
		WHERE source='sound_effect' AND media_type='sound_effect' AND category='file'`)
	if err != nil {
		return nil, fmt.Errorf("load SFX Drive classification: %w", err)
	}
	defer rows.Close()
	result := make(map[string]soundEffectDriveAsset)
	for rows.Next() {
		var driveID, name, metadataJSON string
		if err := rows.Scan(&driveID, &name, &metadataJSON); err != nil {
			return nil, fmt.Errorf("scan SFX Drive classification: %w", err)
		}
		var metadata map[string]any
		_ = json.Unmarshal([]byte(metadataJSON), &metadata)
		family, _ := metadata["sfx_family"].(string)
		if family == "" {
			family = asset.ClassifySoundEffect(name, "", nil).Family
		}
		entry := soundEffectDriveAsset{Family: strings.ToLower(strings.TrimSpace(family))}
		if driveID != "" {
			result[driveID] = entry
		}
		if name != "" {
			result[name] = entry
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SFX Drive classification: %w", err)
	}
	return result, nil
}

func displaySoundEffectFamily(family string) string {
	words := strings.Fields(strings.ReplaceAll(family, "_", " "))
	for i, word := range words {
		if word == "sci-fi" {
			words[i] = "Sci-Fi"
			continue
		}
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
