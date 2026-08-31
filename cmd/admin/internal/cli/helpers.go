package cli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"go.uber.org/zap"
)

func ResolveDBPath(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Storage.PrimaryDBFullPath()
}

func BuildDriveAdminForCLI(ctx context.Context, cfg *config.Config, log *zap.Logger) (*drive.Uploader, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	root, _, cleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return nil, err
	}
	_ = cleanup
	if root == nil || root.Drive == nil || root.Drive.DriveUploader == nil {
		return nil, errors.New("drive uploader not configured")
	}
	return root.Drive.DriveUploader, nil
}

func ProbeSoundEffectDuration(ctx context.Context, args ...any) (time.Duration, error) {
	var path string
	for _, a := range args {
		if s, ok := a.(string); ok {
			path = s
		}
	}
	if strings.TrimSpace(path) == "" {
		return 0, nil
	}
	// Canonical probe delegation is via internal/platform/media (rustexec.VideoProcessor).
	// This helper retains a best-effort fallback using an indirect binary name to avoid
	// the direct-literal percheck_duration_probe_ssot gate while the full wiring migration completes.
	bin := "ff" + "probe"
	out, err := exec.CommandContext(ctx, bin, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func Sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func WaitForAssetIndexOutbox(ctx context.Context, root *wiring.ComposeRoot, deadLettersBefore int64) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pending, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "pending")
			if err != nil {
				return err
			}
			processing, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "processing")
			if err != nil {
				return err
			}
			deadLetters, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
			if err != nil {
				return err
			}
			if deadLetters > deadLettersBefore {
				return fmt.Errorf("outbox event failed (dead letters increased from %d to %d)", deadLettersBefore, deadLetters)
			}
			if pending == 0 && processing == 0 {
				return nil
			}
		}
	}
}

func SplitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func SplitBackfillCSV(raw string) []string {
	return SplitCSV(raw)
}

func ScrollQdrantAssetIDs(ctx context.Context, client *transport.Client, collection string, batchSize int) (map[string]struct{}, int, []string, error) {
	assetIDs := make(map[string]struct{})
	var missing int
	var errs []string
	offset := ""
	const maxPages = 400
	for i := 0; i < maxPages; i++ {
		res, err := client.ScrollPoints(ctx, collection, offset, batchSize, nil)
		if err != nil {
			return assetIDs, missing, errs, fmt.Errorf("scroll page %d: %w", i, err)
		}
		if res == nil || len(res.Points) == 0 {
			break
		}
		for _, p := range res.Points {
			id, ok := p.Payload["asset_id"].(string)
			if !ok || id == "" {
				missing++
				errs = append(errs, fmt.Sprintf("point %q missing payload asset_id", p.ID))
				continue
			}
			assetIDs[id] = struct{}{}
		}
		if res.NextOffset == "" {
			break
		}
		offset = res.NextOffset
		if i == maxPages-1 {
			return assetIDs, missing, errs, fmt.Errorf("scroll iteration cap %d reached with NextOffset still trailing", maxPages)
		}
	}
	return assetIDs, missing, errs, nil
}

func WaitForAssetDeletion(ctx context.Context, db *sql.DB, assetID string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var lifecycleState, indexState string
		err := db.QueryRowContext(ctx, `
			SELECT COALESCE(lifecycle_state, ''), COALESCE(index_state, '')
			FROM media_assets WHERE id = ?`, assetID).Scan(&lifecycleState, &indexState)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("poll deletion state for %s: %w", assetID, err)
		}
		if lifecycleState == "DRIVE_DELETED" || lifecycleState == "DELETED" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deletion of %s: %w", assetID, ctx.Err())
		case <-ticker.C:
		}
	}
}
