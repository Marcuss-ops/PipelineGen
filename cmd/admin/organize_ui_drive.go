package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

const uiDriveFolderID = "1YLE7cVlXUwA9Sa4dqJbkaSGQ2colp-Wy"

func runOrganizeUIDrive(args []string) error {
	folderID := uiDriveFolderID
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		folderID = strings.TrimSpace(args[0])
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
	files, err := root.Drive.Reader.ListFiles(ctx, folderID)
	if err != nil {
		return fmt.Errorf("list UI folder: %w", err)
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
		group := classifyUIDriveName(file.Name)
		moves = append(moves, move{file: file, group: group})
		counts[group]++
	}
	groups := make([]string, 0, len(counts))
	for group := range counts {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	fmt.Printf("UI root=%s direct_files=%d\n", folderID, len(moves))
	for _, group := range groups {
		fmt.Printf("  %-24s %d\n", group, counts[group])
	}
	folders := make(map[string]string, len(groups))
	for _, group := range groups {
		id, err := root.Drive.Admin.GetOrCreateFolder(ctx, group, folderID)
		if err != nil {
			return fmt.Errorf("ensure UI subfolder %s: %w", group, err)
		}
		folders[group] = id
	}
	for _, item := range moves {
		if err := root.Drive.Admin.MoveFile(ctx, item.file.ID, folderID, folders[item.group]); err != nil {
			return fmt.Errorf("move UI file %q to %s: %w", item.file.Name, item.group, err)
		}
	}
	fmt.Printf("UI organization complete: subfolders=%d moved=%d\n", len(folders), len(moves))
	return nil
}

func classifyUIDriveName(name string) string {
	text := strings.ToLower(name)
	switch {
	case containsAny(text, "cash", "register", "success", "win", "correct", "ascending"):
		return "Rewards & Success"
	case containsAny(text, "display", "digits", "counter", "dialup", "network", "download", "disc"):
		return "Displays & System"
	case containsAny(text, "scifi", "futuristic", "metal", "modem"):
		return "Sci-Fi Interface"
	case containsAny(text, "bubble", "pop"):
		return "Pops & Bubbles"
	case containsAny(text, "click", "select", "mouse", "keyboard", "shutter", "press"):
		return "Clicks & Selects"
	case containsAny(text, "beep", "ding", "iphone", "discord", "notification", "message", "error", "wrong", "access", "censor"):
		return "Notifications & Alerts"
	default:
		return "Other UI"
	}
}
