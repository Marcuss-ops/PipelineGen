package soundeffects

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminmedia"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

const soundEffectsMetadataDriveFolderID = "1vfZQHVNZab-pU2fBaj4qzR3iSz1sOVhW"

func RunExportSoundEffectsMetadata(args []string) error {
	_ = args
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
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Drive == nil || root.Drive.Publisher == nil {
		return fmt.Errorf("database and Drive publisher are required")
	}
	// MEDIA-SSOT: the sound-effect projection is a media_assets read, so it MUST
	// resolve from the PostgreSQL media SSOT. A closed media plane fails closed
	// rather than degrading onto the operational SQLite store (which holds no
	// committed media rows).
	source := pgmedia.NewSoundEffectSource(root.MediaPostgres)
	if source == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for sound-effect metadata export")
	}
	uploader, err := delivery.NewAdminUploadService(root.Drive.Publisher)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	report, err := adminmedia.ExportSoundEffectsMetadata(ctx, source, uploader, soundEffectsMetadataDriveFolderID, "sound_effects_metadata.json")
	if err != nil {
		return err
	}
	if report.Result == nil {
		return fmt.Errorf("metadata export completed without result")
	}
	fmt.Printf("uploaded sound effects metadata: total=%d families=%d file_id=%s link=%s\n", report.Total, report.FamilyCount, report.Result.FileID, strings.TrimSpace(report.Result.WebViewLink))
	return nil
}
