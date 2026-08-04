// Package adminmedia contains application use cases for operator media tools.
// CLI packages only parse input, compose adapters, and present results.
package adminmedia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
)

// DriveAudioFile is the infrastructure-neutral file projection needed by
// the recursive sound-effect normalizer.
type DriveAudioFile struct {
	ID       string
	Name     string
	MimeType string
}

// DriveAudioReader is the read-only Drive surface used by normalization.
type DriveAudioReader interface {
	ListFiles(ctx context.Context, parentID string) ([]DriveAudioFile, error)
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, error)
}

// AudioEditor probes and rewrites local media without exposing a subprocess
// or FFmpeg dependency to the application layer.
type AudioEditor interface {
	Probe(ctx context.Context, path string) (time.Duration, error)
	Trim(ctx context.Context, path string, maxSeconds float64) error
}

// AdminUploader is the canonical explicit-folder publication use case.
type AdminUploader interface {
	Publish(ctx context.Context, cmd delivery.AdminUploadCommand) (*delivery.PublishResult, error)
}

// NormalizeReport summarizes the deterministic Drive normalization operation.
type NormalizeReport struct {
	Checked int
	Changed int
	Updates []NormalizeUpdate
}

// NormalizeUpdate describes one republished file for operator output.
type NormalizeUpdate struct {
	Filename      string
	FolderID      string
	Before        time.Duration
	After         time.Duration
	PublishResult *delivery.PublishResult
}

// NormalizeDriveSoundEffects downloads, validates, trims, and republishes
// remote audio in place. It owns the traversal and duration policy; Drive,
// media processing, and publication remain typed ports.
func NormalizeDriveSoundEffects(ctx context.Context, rootFolder string, maxSeconds float64, reader DriveAudioReader, editor AudioEditor, uploader AdminUploader) (NormalizeReport, error) {
	if strings.TrimSpace(rootFolder) == "" {
		return NormalizeReport{}, fmt.Errorf("adminmedia: root folder is required")
	}
	if maxSeconds <= 0 {
		return NormalizeReport{}, fmt.Errorf("adminmedia: max seconds must be positive")
	}
	if reader == nil || editor == nil || uploader == nil {
		return NormalizeReport{}, fmt.Errorf("adminmedia: reader, editor, and uploader are required")
	}

	files, err := listAudioRecursive(ctx, reader, rootFolder)
	if err != nil {
		return NormalizeReport{}, err
	}
	tempDir, err := os.MkdirTemp("", "velox-sfx-drive-")
	if err != nil {
		return NormalizeReport{}, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	report := NormalizeReport{}
	for _, item := range files {
		report.Checked++
		localPath := filepath.Join(tempDir, item.file.ID+filepath.Ext(item.file.Name))
		if err := downloadFile(ctx, reader, item.file.ID, localPath); err != nil {
			return report, fmt.Errorf("save %q: %w", item.file.Name, err)
		}
		duration, err := editor.Probe(ctx, localPath)
		if err != nil {
			return report, fmt.Errorf("probe remote %q: %w", item.file.Name, err)
		}
		if duration <= time.Duration(maxSeconds*float64(time.Second)) {
			continue
		}

		target := trimTargetSeconds(localPath, maxSeconds)
		if err := editor.Trim(ctx, localPath, target); err != nil {
			return report, fmt.Errorf("trim remote %q: %w", item.file.Name, err)
		}
		newDuration, err := editor.Probe(ctx, localPath)
		if err != nil || newDuration > time.Duration(maxSeconds*float64(time.Second)) {
			return report, fmt.Errorf("remote trim validation failed for %q: duration=%.3fs err=%v", item.file.Name, newDuration.Seconds(), err)
		}
		publishResult, err := uploader.Publish(ctx, delivery.AdminUploadCommand{
			LocalPath: localPath,
			FolderID:  item.folderID,
			Filename:  item.file.Name,
		})
		if err != nil {
			return report, fmt.Errorf("update remote %q: %w", item.file.Name, err)
		}
		report.Changed++
		report.Updates = append(report.Updates, NormalizeUpdate{
			Filename: item.file.Name, FolderID: item.folderID,
			Before: duration, After: newDuration, PublishResult: publishResult,
		})
	}
	return report, nil
}

type audioEntry struct {
	file     DriveAudioFile
	folderID string
}

func listAudioRecursive(ctx context.Context, reader DriveAudioReader, folderID string) ([]audioEntry, error) {
	files, err := reader.ListFiles(ctx, folderID)
	if err != nil {
		return nil, fmt.Errorf("list Drive folder %s: %w", folderID, err)
	}
	result := make([]audioEntry, 0, len(files))
	for _, file := range files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			children, err := listAudioRecursive(ctx, reader, file.ID)
			if err != nil {
				return nil, err
			}
			result = append(result, children...)
			continue
		}
		result = append(result, audioEntry{file: file, folderID: folderID})
	}
	return result, nil
}

func downloadFile(ctx context.Context, reader DriveAudioReader, fileID, destination string) error {
	body, err := reader.DownloadFile(ctx, fileID)
	if err != nil {
		return err
	}
	defer body.Close()
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, body); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func trimTargetSeconds(inputPath string, maxSeconds float64) float64 {
	switch strings.ToLower(filepath.Ext(inputPath)) {
	case ".mp3":
		return maxSeconds - 0.10
	case ".mp4", ".mov", ".mkv":
		return maxSeconds - 0.05
	default:
		return maxSeconds
	}
}

// SoundEffectMetadata is the application DTO emitted by the SQLite adapter.
type SoundEffectMetadata struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Filename        string          `json:"filename"`
	DriveFileID     string          `json:"drive_file_id"`
	DriveLink       string          `json:"drive_link"`
	DownloadLink    string          `json:"download_link"`
	LocalPath       string          `json:"local_path"`
	DurationSeconds float64         `json:"duration_seconds"`
	Family          string          `json:"family"`
	Subtype         string          `json:"subtype"`
	Tags            string          `json:"tags"`
	FolderID        string          `json:"folder_id"`
	ParentFolderID  string          `json:"parent_folder_id"`
	FolderPath      string          `json:"folder_path"`
	Metadata        json.RawMessage `json:"metadata"`
}

// MetadataSource supplies the canonical sound-effect projection.
type MetadataSource interface {
	ListSoundEffects(ctx context.Context) ([]SoundEffectMetadata, error)
}

// MetadataExport is the stable JSON document written by the export use case.
type MetadataExport struct {
	GeneratedAt string                           `json:"generated_at"`
	DriveRootID string                           `json:"drive_root_folder_id"`
	Total       int                              `json:"total"`
	ByFamily    map[string][]SoundEffectMetadata `json:"by_family"`
}

// ExportReport describes a successful metadata publication.
type ExportReport struct {
	Total       int
	FamilyCount int
	Result      *delivery.PublishResult
}

// ExportSoundEffectsMetadata builds and publishes the metadata document.
func ExportSoundEffectsMetadata(ctx context.Context, source MetadataSource, uploader AdminUploader, rootFolder, filename string) (ExportReport, error) {
	if source == nil || uploader == nil {
		return ExportReport{}, fmt.Errorf("adminmedia: metadata source and uploader are required")
	}
	if strings.TrimSpace(rootFolder) == "" || strings.TrimSpace(filename) == "" {
		return ExportReport{}, fmt.Errorf("adminmedia: metadata destination is required")
	}
	items, err := source.ListSoundEffects(ctx)
	if err != nil {
		return ExportReport{}, err
	}
	export := MetadataExport{GeneratedAt: time.Now().UTC().Format(time.RFC3339), DriveRootID: rootFolder, ByFamily: make(map[string][]SoundEffectMetadata)}
	for _, item := range items {
		family := strings.TrimSpace(item.Family)
		if family == "" {
			family = "uncategorized"
			item.Family = family
		}
		export.ByFamily[family] = append(export.ByFamily[family], item)
		export.Total++
	}

	tmp, err := os.CreateTemp("", "sound_effects_metadata_*.json")
	if err != nil {
		return ExportReport{}, fmt.Errorf("create metadata temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(export); err != nil {
		_ = tmp.Close()
		return ExportReport{}, fmt.Errorf("encode metadata: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return ExportReport{}, fmt.Errorf("close metadata file: %w", err)
	}
	result, err := uploader.Publish(ctx, delivery.AdminUploadCommand{LocalPath: tmpPath, FolderID: rootFolder, Filename: filename})
	if err != nil {
		return ExportReport{}, fmt.Errorf("upload metadata JSON: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return ExportReport{}, fmt.Errorf("metadata upload completed without Drive file ID")
	}
	return ExportReport{Total: export.Total, FamilyCount: len(export.ByFamily), Result: result}, nil
}

// RenderManifest is the validated input contract for a short render.
type RenderManifest struct {
	Input    string          `json:"input"`
	Output   string          `json:"output"`
	Font     string          `json:"font"`
	Upload   *RenderUpload   `json:"upload,omitempty"`
	Effects  []RenderEffect  `json:"effects"`
	Overlays []RenderOverlay `json:"overlays"`
}

type RenderUpload struct {
	FolderID string `json:"folder_id"`
	Filename string `json:"filename"`
}

type RenderEffect struct {
	Path     string  `json:"path"`
	DelayMS  int     `json:"delay_ms"`
	Duration float64 `json:"duration"`
	Volume   string  `json:"volume"`
}

type RenderOverlay struct {
	Text  string `json:"text"`
	Start string `json:"start"`
	End   string `json:"end"`
	Size  string `json:"size"`
	Y     string `json:"y"`
	Color string `json:"color"`
}

// ShortRenderer is the infrastructure port for the actual media render.
type ShortRenderer interface {
	Render(ctx context.Context, manifest RenderManifest) error
}

// RenderShort validates and renders a short, then optionally publishes it.
func RenderShort(ctx context.Context, manifest RenderManifest, renderer ShortRenderer, uploader AdminUploader) (*delivery.PublishResult, error) {
	if renderer == nil {
		return nil, fmt.Errorf("adminmedia: renderer is required")
	}
	if strings.TrimSpace(manifest.Input) == "" || strings.TrimSpace(manifest.Output) == "" || strings.TrimSpace(manifest.Font) == "" {
		return nil, fmt.Errorf("adminmedia: input, output and font are required")
	}
	if _, err := os.Stat(manifest.Input); err != nil {
		return nil, fmt.Errorf("input unavailable: %w", err)
	}
	if len(manifest.Overlays) == 0 {
		return nil, fmt.Errorf("adminmedia: at least one overlay is required")
	}
	for _, effect := range manifest.Effects {
		if _, err := os.Stat(effect.Path); err != nil {
			return nil, fmt.Errorf("effect unavailable: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(manifest.Output), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	if err := renderer.Render(ctx, manifest); err != nil {
		return nil, err
	}
	if manifest.Upload == nil {
		return nil, nil
	}
	if uploader == nil {
		return nil, fmt.Errorf("adminmedia: uploader is required when upload is requested")
	}
	result, err := uploader.Publish(ctx, delivery.AdminUploadCommand{LocalPath: manifest.Output, FolderID: manifest.Upload.FolderID, Filename: manifest.Upload.Filename})
	if err != nil {
		return nil, fmt.Errorf("upload rendered short: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return nil, fmt.Errorf("upload completed without a Drive file ID")
	}
	return result, nil
}
