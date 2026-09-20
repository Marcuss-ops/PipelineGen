package soundeffects

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// runDownloadSoundEffects downloads the canonical sound_effect catalog to
// <data_dir>/<media_dir>/sound_effects, preserving Drive folder-relative names.
// Each local_path update goes through the canonical dispatcher so the DB write
// and the Qdrant re-index request remain coupled.
func RunDownloadSoundEffects(args []string) error {
	fs := flag.NewFlagSet("download-sound-effects", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
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
	if root == nil || root.DB == nil || root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("database and Drive reader are required")
	}
	if root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil || root.Outbox.Dispatcher == nil {
		return fmt.Errorf("clips repository and outbox dispatcher are required")
	}

	// MEDIA-SSOT: the sound-effect listing is a media_assets read, so it MUST
	// resolve from the PostgreSQL media SSOT.
	catalog := pgmedia.NewSoundEffectCatalog(root.MediaPostgres)
	if catalog == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for sound-effect download")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	rootDir := cfg.Storage.FullPath(filepath.Join(cfg.Storage.MediaDir, "sound_effects"))
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return fmt.Errorf("create sound effects directory: %w", err)
	}

	rows, err := catalog.ListRows(ctx)
	if err != nil {
		return fmt.Errorf("list sound effects: %w", err)
	}

	var downloaded, skipped, failed int
	for _, row := range rows {
		id, name, driveID, folderPath := row.ID, row.Name, row.DriveFileID, row.FolderPath
		if strings.TrimSpace(driveID) == "" {
			failed++
			log.Warn("sound effect has no Drive file ID", zap.String("asset_id", id), zap.String("name", name))
			continue
		}

		rel, err := soundEffectRelativePath(name, folderPath)
		if err != nil {
			failed++
			log.Warn("invalid sound effect path", zap.String("asset_id", id), zap.Error(err))
			continue
		}
		dest := filepath.Join(rootDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", id, err)
		}

		if info, statErr := os.Stat(dest); statErr == nil && info.Size() > 0 {
			skipped++
			continue
		}
		if err := downloadSoundEffect(ctx, root.Drive.Reader, driveID, dest); err != nil {
			failed++
			log.Warn("sound effect download failed", zap.String("asset_id", id), zap.String("name", name), zap.Error(err))
			continue
		}

		clip, err := root.GetMediaClip(ctx, id)
		if err != nil || clip == nil {
			failed++
			log.Warn("downloaded sound effect but asset row is unavailable", zap.String("asset_id", id), zap.Error(err))
			continue
		}
		hash, err := cli.Sha256File(dest)
		if err != nil {
			failed++
			return fmt.Errorf("hash downloaded sound effect %s: %w", id, err)
		}
		clip.SetLocalPath(dest)
		clip.SetLegacyFileMD5(hash)
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			failed++
			return fmt.Errorf("persist local sound effect %s: %w", id, err)
		}
		downloaded++
	}

	log.Info("sound effects downloaded", zap.String("directory", rootDir), zap.Int("downloaded", downloaded), zap.Int("skipped", skipped), zap.Int("failed", failed))
	fmt.Printf("Sound effects downloaded: directory=%s downloaded=%d skipped=%d failed=%d\n", rootDir, downloaded, skipped, failed)
	if failed > 0 {
		return fmt.Errorf("sound effects download completed with %d failures", failed)
	}
	return nil
}

func soundEffectRelativePath(name, folderPath string) (string, error) {
	rel := strings.TrimSpace(folderPath)
	if rel == "" {
		rel = strings.TrimSpace(name)
	} else {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) > 0 && parts[0] == "sound_effects" {
			parts = parts[1:]
		}
		rel = strings.Join(parts, "/")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sound effects root: %q", rel)
	}
	return filepath.ToSlash(clean), nil
}

func downloadSoundEffect(ctx context.Context, reader interface {
	DownloadFile(context.Context, string) (io.ReadCloser, string, error)
}, driveID, dest string) error {
	rc, _, err := reader.DownloadFile(ctx, driveID)
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".sfx-download-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

type namedSoundEffect struct {
	OldName     string
	NewName     string
	Category    string
	Description string
	Tags        []string
	Duration    float64
}

func RunApplyAdditionalSoundEffects(args []string) error {
	fs := flag.NewFlagSet("apply-additional-sound-effects", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
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
	if root == nil || root.DB == nil || root.Drive == nil || root.Drive.Admin == nil || root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil || root.Outbox.Dispatcher == nil {
		return fmt.Errorf("database, Drive admin, clips repository and outbox dispatcher are required")
	}
	// MEDIA-SSOT: the row probe is a media_assets read, so it MUST resolve from
	// the PostgreSQL media SSOT.
	catalog := pgmedia.NewSoundEffectCatalog(root.MediaPostgres)
	if catalog == nil {
		return fmt.Errorf("media PostgreSQL SSOT is required for additional sound effects")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	prober := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	changed := 0
	for _, item := range additionalSoundEffects() {
		row, err := catalog.FindByName(ctx, item.OldName, item.NewName)
		if err != nil {
			return fmt.Errorf("find %q: %w", item.OldName, err)
		}
		if row == nil {
			log.Warn("additional sound effect already superseded or absent", zap.String("name", item.OldName))
			continue
		}
		id, currentName, driveID, localPath := row.ID, row.Name, row.DriveFileID, row.LocalPath
		if currentName != item.NewName {
			if err := root.Drive.Admin.RenameFile(ctx, driveID, item.NewName); err != nil {
				return fmt.Errorf("rename Drive file %s: %w", driveID, err)
			}
		}
		clip, err := root.GetMediaClip(ctx, id)
		if err != nil || clip == nil {
			return fmt.Errorf("load asset %s: %w", id, err)
		}
		clip.Name = item.NewName
		clip.Filename = item.NewName
		clip.SetFolderPath(filepath.ToSlash(filepath.Join("sound_effects", item.NewName)))
		clip.Tags = item.Tags
		clip.SearchText = item.NewName + " " + item.Description + " " + strings.Join(item.Tags, " ")
		clip.SetMetadataString("sfx_category", item.Category)
		clip.SetMetadataString("sfx_description", item.Description)
		clip.SetMetadataString("sfx_tags", strings.Join(item.Tags, ","))
		if localPath != "" {
			newLocalPath := filepath.Join(filepath.Dir(localPath), item.NewName)
			if localPath != newLocalPath {
				if err := os.Rename(localPath, newLocalPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("rename local file %s: %w", localPath, err)
				}
				localPath = newLocalPath
			}
			clip.SetLocalPath(localPath)
			if duration, err := cli.ProbeSoundEffectDuration(ctx, prober, localPath); err == nil && duration > 0 {
				clip.Duration = duration
			}
		}
		if duration := suppliedSoundEffectDuration(item.NewName); duration > 0 {
			clip.Duration = duration
		}
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.LegacyFileMD5()); err != nil {
			return fmt.Errorf("persist additional effect %s: %w", id, err)
		}
		changed++
	}
	fmt.Printf("Additional sound effects applied: %d\n", changed)
	return nil
}

// probeSoundEffectDuration measures a local audio file through the canonical
// Rust media probe port (never a raw ffprobe exec). An empty path is a
// legitimate "no local source" signal and returns (0, nil).
