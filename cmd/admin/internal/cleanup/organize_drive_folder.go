package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

type driveOrganizationPolicy struct {
	FolderID string                  `json:"folder_id,omitempty"`
	Default  string                  `json:"default_group"`
	Rules    []driveOrganizationRule `json:"rules"`
}
type driveOrganizationRule struct {
	Group    string   `json:"group"`
	Keywords []string `json:"keywords"`
}

func RunOrganizeDriveFolder(args []string) error {
	fs := flag.NewFlagSet("organize-drive-folder", flag.ContinueOnError)
	policyPath := fs.String("policy", "", "JSON organization policy (required)")
	folderID := fs.String("folder-id", "", "override the policy folder ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*policyPath) == "" {
		return fmt.Errorf("--policy is required")
	}
	data, err := os.ReadFile(*policyPath)
	if err != nil {
		return fmt.Errorf("read policy: %w", err)
	}
	var policy driveOrganizationPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return fmt.Errorf("decode policy: %w", err)
	}
	destination := strings.TrimSpace(*folderID)
	if destination == "" {
		destination = strings.TrimSpace(policy.FolderID)
	}
	if destination == "" || strings.TrimSpace(policy.Default) == "" {
		return fmt.Errorf("policy folder_id and default_group are required")
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
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Admin == nil {
		return fmt.Errorf("Drive reader and admin are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	files, err := root.Drive.Reader.ListFiles(ctx, destination)
	if err != nil {
		return fmt.Errorf("list folder: %w", err)
	}
	type move struct{ id, name, group string }
	moves := make([]move, 0, len(files))
	counts := map[string]int{}
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			continue
		}
		group := policy.Default
		lower := strings.ToLower(file.Name)
		for _, rule := range policy.Rules {
			if containsAny(lower, rule.Keywords...) {
				group = rule.Group
				break
			}
		}
		moves = append(moves, move{id: file.ID, name: file.Name, group: group})
		counts[group]++
	}
	groups := make([]string, 0, len(counts))
	for group := range counts {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		fmt.Printf("  %-24s %d\n", group, counts[group])
	}
	folders := make(map[string]string, len(groups))
	for _, group := range groups {
		id, err := root.Drive.Admin.GetOrCreateFolder(ctx, group, destination)
		if err != nil {
			return fmt.Errorf("ensure subfolder %s: %w", group, err)
		}
		folders[group] = id
	}
	for _, item := range moves {
		if err := root.Drive.Admin.MoveFile(ctx, item.id, destination, folders[item.group]); err != nil {
			return fmt.Errorf("move %q to %s: %w", item.name, item.group, err)
		}
	}
	fmt.Printf("Drive organization complete: folder=%s subfolders=%d moved=%d\n", destination, len(folders), len(moves))
	return nil
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
