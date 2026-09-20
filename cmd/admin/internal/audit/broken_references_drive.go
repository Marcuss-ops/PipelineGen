// cmd/admin/internal/audit/broken_references_drive.go — the Drive side of the
// broken-references audit.
//
// The audit needs the set of Drive file IDs that currently exist before it can
// judge whether a stored reference is broken. That set comes either from a Fase
// 1 snapshot file or from a live Drive walk, and it is loaded ONCE per run so a
// live walk is never repeated for the media and operational halves of the same
// comparison.
//
// Failure stays data, not a silent skip: a partially-failed walk returns the
// failure strings alongside whatever inventory it did collect, so the report can
// surface them instead of implying full coverage.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// loadKnownDriveIDs resolves the set of Drive file IDs that currently exist.
func loadKnownDriveIDs(ctx context.Context, cfg *config.Config, log *zap.Logger, inventoryPath string) (map[string]bool, []string, error) {
	if inventoryPath != "" {
		knownIDs, errs := loadDriveInventoryFromFile(inventoryPath)
		return knownIDs, errs, nil
	}
	knownIDs, errs, err := walkLiveDriveIDs(ctx, cfg, log)
	if err != nil {
		return nil, errs, err
	}
	return knownIDs, errs, nil
}

func loadDriveInventoryFromFile(path string) (map[string]bool, []string) {
	var errs []string
	data, err := os.ReadFile(path)
	if err != nil {
		errs = append(errs, fmt.Sprintf("read drive inventory %s: %v", path, err))
		return nil, errs
	}
	var entries []driveInventoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		errs = append(errs, fmt.Sprintf("parse drive inventory %s: %v", path, err))
		return nil, errs
	}
	known := make(map[string]bool, len(entries))
	for _, e := range entries {
		known[e.ID] = true
	}
	return known, errs
}

func walkLiveDriveIDs(ctx context.Context, cfg *config.Config, log *zap.Logger) (map[string]bool, []string, error) {
	uploader, err := cli.BuildDriveAdminForCLI(ctx, cfg, log)
	if err != nil {
		return nil, nil, fmt.Errorf("init Drive: %w", err)
	}

	roots := collectDriveRoots(cfg.Drive)
	if len(roots) == 0 {
		return make(map[string]bool), []string{"no Drive roots configured"}, nil
	}

	inventory, failures := walkDriveInventory(ctx, uploader.ListFiles, roots)
	errs := make([]string, len(failures))
	copy(errs, failures)

	known := make(map[string]bool, len(inventory.entries))
	for _, e := range inventory.entries {
		known[e.ID] = true
	}
	return known, errs, nil
}
