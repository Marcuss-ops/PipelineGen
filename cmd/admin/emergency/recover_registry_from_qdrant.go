package emergency

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"go.uber.org/zap"
)

const (
	recoveryHealthy    = "HEALTHY"
	recoveryMissing    = "MISSING"
	recoveryRepairable = "REPAIRABLE"
	recoveryInvalid    = "INVALID"
	recoveryConflict   = "CONFLICT"
)

type recoveryPurpose string

const (
	purposeDisasterRecovery  recoveryPurpose = "disaster-recovery"
	purposeMigrationRecovery recoveryPurpose = "migration-recovery"
	purposeForensics         recoveryPurpose = "forensics"
)

type recoverRegistryFlags struct {
	Collection string
	AssetIDs   []string
	All        bool
	JSON       bool
	Apply      bool
	PageSize   int
	Purpose    recoveryPurpose
}

type recoveryItem struct {
	AssetID string `json:"asset_id"`
	PointID string `json:"point_id,omitempty"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Source  string `json:"source,omitempty"`
	DriveID string `json:"drive_file_id,omitempty"`
}

type recoveryReport struct {
	Collection string         `json:"collection"`
	DryRun     bool           `json:"dry_run"`
	Complete   bool           `json:"complete_scan"`
	Total      int            `json:"total"`
	Counts     map[string]int `json:"counts"`
	Items      []recoveryItem `json:"items"`
}

func parseRecoverRegistryFlags(args []string) (recoverRegistryFlags, error) {
	f := recoverRegistryFlags{PageSize: 500}
	for _, arg := range args {
		switch {
		case arg == "--json":
			f.JSON = true
		case arg == "--all":
			f.All = true
		case strings.HasPrefix(arg, "--purpose="):
			f.Purpose = recoveryPurpose(strings.TrimSpace(strings.TrimPrefix(arg, "--purpose=")))
		case strings.HasPrefix(arg, "--collection="):
			f.Collection = strings.TrimSpace(strings.TrimPrefix(arg, "--collection="))
		case strings.HasPrefix(arg, "--asset-id="):
			id := strings.TrimSpace(strings.TrimPrefix(arg, "--asset-id="))
			if id == "" {
				return f, errors.New("recover-registry-from-qdrant: --asset-id cannot be empty")
			}
			f.AssetIDs = append(f.AssetIDs, id)
		case strings.HasPrefix(arg, "--page-size="):
			n, err := cli.ParsePositiveFlag(arg, "--page-size")
			if err != nil {
				return f, err
			}
			if n > 1000 {
				return f, errors.New("recover-registry-from-qdrant: --page-size cannot exceed 1000")
			}
			f.PageSize = n
		case arg == "--apply":
			f.Apply = true
		default:
			if strings.HasPrefix(arg, "-") {
				return f, fmt.Errorf("recover-registry-from-qdrant: unknown flag %s", arg)
			}
		}
	}
	switch f.Purpose {
	case purposeDisasterRecovery, purposeMigrationRecovery, purposeForensics:
		// Explicit purpose is required so this command cannot be used as an
		// accidental replacement for the normal SQLite -> Qdrant reconciler.
	default:
		return f, errors.New("recover-registry-from-qdrant: --purpose must be one of disaster-recovery, migration-recovery, forensics")
	}
	if err := qdrantschema.ValidateEmergencyCollection(f.Collection); err != nil {
		return f, fmt.Errorf("recover-registry-from-qdrant: invalid --collection: %w", err)
	}
	if f.All && len(f.AssetIDs) > 0 {
		return f, errors.New("recover-registry-from-qdrant: --all and --asset-id are mutually exclusive")
	}
	if f.Apply && !f.All && len(f.AssetIDs) == 0 {
		return f, errors.New("recover-registry-from-qdrant: --apply requires --asset-id=<id> or explicit --all")
	}
	if !f.All && len(f.AssetIDs) == 0 {
		return f, errors.New("recover-registry-from-qdrant: specify at least one --asset-id (or --all)")
	}
	return f, nil
}

func RunRecoverRegistryFromQdrant(args []string) error {
	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	flags, err := parseRecoverRegistryFlags(args)
	if err != nil {
		return err
	}
	if !cfg.Qdrant.Enabled {
		return errors.New("recover-registry-from-qdrant requires qdrant.enabled=true")
	}

	ctx := cli.CmdContext()
	client := transport.NewClient(&qdrantschema.Config{BaseURL: cfg.Qdrant.BaseURL, APIKey: cfg.Qdrant.APIKey, Timeout: cfg.Qdrant.Timeout}, log)
	dbSet, err := cli.OpenDatabaseSet(cfg, log)
	if err != nil {
		return fmt.Errorf("open database set: %w", err)
	}
	defer dbSet.Close()
	db := dbSet.Primary

	report, err := classifyRecovery(ctx, client, db.DB, flags)
	if err != nil {
		return err
	}
	if flags.Apply {
		if flags.All {
			return errors.New("recover-registry-from-qdrant: --apply --all is intentionally blocked until an explicit operator confirmation mechanism is added")
		}
		return errors.New("recover-registry-from-qdrant: --apply is parsed safely but mutation is not yet implemented; no records were changed")
	}
	if flags.JSON {
		encoded, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(encoded))
		return nil
	}
	fmt.Printf("=== recover-registry-from-qdrant (EMERGENCY DRY-RUN) ===\n  Purpose:    %s\n  Collection: %s\n  Complete:   %v\n  Total:      %d\n", flags.Purpose, report.Collection, report.Complete, report.Total)
	for _, status := range []string{recoveryHealthy, recoveryMissing, recoveryRepairable, recoveryInvalid, recoveryConflict} {
		fmt.Printf("  %-11s %d\n", status, report.Counts[status])
	}
	for _, item := range report.Items {
		fmt.Printf("  %s  %-11s %s\n", item.AssetID, item.Status, item.Reason)
	}
	log.Info("emergency recovery dry-run complete", zap.String("purpose", string(flags.Purpose)), zap.Int("total", report.Total))
	return nil
}

func classifyRecovery(ctx context.Context, client *transport.Client, db *sql.DB, flags recoverRegistryFlags) (recoveryReport, error) {
	wanted := map[string]struct{}{}
	for _, id := range flags.AssetIDs {
		wanted[id] = struct{}{}
	}
	seen := map[string]recoveryItem{}
	offset := ""
	for {
		page, err := client.ScrollPoints(ctx, flags.Collection, offset, flags.PageSize, nil)
		if err != nil {
			return recoveryReport{}, fmt.Errorf("scroll recovery collection %q: %w", flags.Collection, err)
		}
		if page == nil {
			break
		}
		for _, point := range page.Points {
			id := stringPayload(point.Payload, "asset_id")
			if id == "" || (!flags.All && !contains(wanted, id)) {
				continue
			}
			item := classifyPoint(ctx, db, point.ID, id, point.Payload)
			if prior, ok := seen[id]; ok && prior.PointID != point.ID {
				item.Status = recoveryConflict
				item.Reason = "multiple Qdrant points contain the same asset_id"
			}
			seen[id] = item
		}
		if page.NextOffset == "" {
			break
		}
		offset = page.NextOffset
	}
	ids := flags.AssetIDs
	if flags.All {
		ids = make([]string, 0, len(seen))
		for id := range seen {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	report := recoveryReport{Collection: flags.Collection, DryRun: true, Complete: true, Counts: map[string]int{}, Items: make([]recoveryItem, 0, len(ids))}
	for _, id := range ids {
		item, ok := seen[id]
		if !ok {
			item = recoveryItem{AssetID: id, Status: recoveryMissing, Reason: "asset_id was not found in the recovery collection"}
		}
		report.Items = append(report.Items, item)
		report.Counts[item.Status]++
		report.Total++
	}
	return report, nil
}

func classifyPoint(ctx context.Context, db *sql.DB, pointID, assetID string, payload map[string]any) recoveryItem {
	item := recoveryItem{AssetID: assetID, PointID: pointID, Source: stringPayload(payload, "source"), DriveID: firstPayload(payload, "drive_file_id", "external_id")}
	if !strings.HasPrefix(assetID, "yt_") {
		item.Status = recoveryInvalid
		item.Reason = "asset_id is not a canonical YouTube identity"
		return item
	}
	if !validYouTubeID(assetID) {
		item.Status = recoveryInvalid
		item.Reason = "asset_id does not match yt_{videoID}_{start}_{end}_{policy}"
		return item
	}
	var exists int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM media_assets WHERE id = ?", assetID).Scan(&exists)
	if err == nil {
		item.Status = recoveryHealthy
		item.Reason = "canonical media_assets row already exists"
		return item
	}
	if !errors.Is(err, sql.ErrNoRows) {
		item.Status = recoveryInvalid
		item.Reason = fmt.Sprintf("cannot check media_assets: %v", err)
		return item
	}
	if item.DriveID == "" {
		item.Status = recoveryInvalid
		item.Reason = "missing Drive locator evidence"
		return item
	}
	if stringPayload(payload, "media_type") == "clip" {
		item.Status = recoveryRepairable
		item.Reason = "legacy clip taxonomy can be normalized by ResolveTaxonomy"
	} else {
		item.Status = recoveryRepairable
		item.Reason = "canonical SQLite row is missing and payload contains recovery evidence"
	}
	return item
}

func validYouTubeID(id string) bool {
	parts := strings.Split(id, "_")
	return len(parts) >= 5 && parts[0] == "yt" && parts[1] != "" && parts[2] != "" && parts[3] != "" && parts[4] != ""
}
func contains(set map[string]struct{}, value string) bool { _, ok := set[value]; return ok }
func stringPayload(payload map[string]any, key string) string {
	if s, ok := payload[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
func firstPayload(payload map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := stringPayload(payload, k); v != "" {
			return v
		}
	}
	return ""
}
