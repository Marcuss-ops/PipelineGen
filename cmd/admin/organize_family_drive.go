package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

func runOrganizeFamilyDrive(args []string) error {
	fs := flag.NewFlagSet("organize-family-drive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folderID := fs.String("folder-id", "", "Drive family folder ID")
	family := fs.String("family", "", "family: whoosh, impact or ui")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*folderID) == "" || strings.TrimSpace(*family) == "" {
		return fmt.Errorf("folder-id and family are required")
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
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil {
		return fmt.Errorf("Drive reader and admin are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	files, err := root.Drive.Reader.ListFiles(ctx, *folderID)
	if err != nil {
		return fmt.Errorf("list family folder: %w", err)
	}
	type move struct {
		file  drive.DriveFileInfo
		group string
	}
	moves := make([]move, 0, len(files))
	counts := make(map[string]int)
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			continue
		}
		group := classifyFamilyDriveName(strings.ToLower(*family), file.Name)
		moves = append(moves, move{file: file, group: group})
		counts[group]++
	}
	groups := make([]string, 0, len(counts))
	for group := range counts {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	fmt.Printf("Family=%s folder=%s direct_files=%d\n", *family, *folderID, len(moves))
	for _, group := range groups {
		fmt.Printf("  %-24s %d\n", group, counts[group])
	}
	folders := make(map[string]string, len(groups))
	for _, group := range groups {
		id, err := root.Drive.Admin.GetOrCreateFolder(ctx, group, *folderID)
		if err != nil {
			return fmt.Errorf("ensure subfolder %s: %w", group, err)
		}
		folders[group] = id
	}
	for _, item := range moves {
		if err := root.Drive.Admin.MoveFile(ctx, item.file.ID, *folderID, folders[item.group]); err != nil {
			return fmt.Errorf("move %q to %s: %w", item.file.Name, item.group, err)
		}
	}
	fmt.Printf("Family organization complete: subfolders=%d moved=%d\n", len(folders), len(moves))
	return nil
}

func classifyFamilyDriveName(family, name string) string {
	text := strings.ToLower(name)
	switch family {
	case "whoosh":
		switch {
		case containsAny(text, "suction", "reverse", "rewind"):
			return "Suction & Reverse"
		case containsAny(text, "electronic", "synth", "scifi", "space"):
			return "Electronic & Sci-Fi"
		case containsAny(text, "arrow", "whistle", "swish", "swing", "swipe"):
			return "Swish & Swings"
		case containsAny(text, "fire", "transition", "sweep", "sweep"):
			return "Transitions & Sweeps"
		case containsAny(text, "low", "sub", "atmospheric", "deep"):
			return "Low & Sub"
		case containsAny(text, "fast", "quick", "broadband"):
			return "Fast Whooshes"
		default:
			return "Other Whoosh"
		}
	case "impact":
		switch {
		case containsAny(text, "water", "watery", "puddle", "ocean"):
			return "Watery Impacts"
		case containsAny(text, "metal", "warehouse", "glass", "crash", "slam"):
			return "Metal & Crashes"
		case containsAny(text, "drop", "sub", "bass", "rumble", "disto"):
			return "Drops & Sub Bass"
		case containsAny(text, "boom", "cinematic", "grand", "explosive", "universe"):
			return "Cinematic Booms"
		case containsAny(text, "hit", "punch", "combat", "thud"):
			return "Hits & Punches"
		default:
			return "Other Impact"
		}
	case "ui":
		return classifyUIDriveName(name)
	default:
		return "Other " + strings.Title(family)
	}
}
