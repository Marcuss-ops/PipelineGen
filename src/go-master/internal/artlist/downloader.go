// Package artlist fornisce funzionalità per scaricare e organizzare clip da Artlist
package artlist

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"velox/go-master/pkg/logger"
	"velox/go-master/internal/upload/drive"
	"go.uber.org/zap"
)

// Downloader gestisce il download e l'organizzazione di clip Artlist
type Downloader struct {
	dbPath      string
	driveClient *drive.Client
	driveFolder string // Cartella Drive principale "Artlist Clips"
}

// NewDownloader crea un nuovo downloader Artlist
func NewDownloader(dbPath string, driveClient *drive.Client, driveFolder string) *Downloader {
	return &Downloader{
		dbPath:      dbPath,
		driveClient: driveClient,
		driveFolder: driveFolder,
	}
}

// DownloadAndOrganize scarica clip da Artlist e le organizza in cartelle Drive
func (d *Downloader) DownloadAndOrganize(ctx context.Context, scriptID string, clips []ClipToDownload) (*DownloadReport, error) {
	logger.Info("Starting Artlist clip download and organization",
		zap.String("script_id", scriptID),
		zap.Int("total_clips", len(clips)),
	)

	report := &DownloadReport{
		ScriptID:  scriptID,
		StartedAt: time.Now(),
	}

	// Crea cartella principale su Drive se non esiste
	mainFolderID, err := d.ensureDriveFolder(ctx, d.driveFolder)
	if err != nil {
		return nil, fmt.Errorf("failed to create main Drive folder: %w", err)
	}

	// Crea sottocartella per questo script
	scriptFolderName := fmt.Sprintf("Script_%s", scriptID[:8])
	scriptFolderID, err := d.createSubFolder(ctx, mainFolderID, scriptFolderName)
	if err != nil {
		return nil, fmt.Errorf("failed to create script folder: %w", err)
	}

	report.ScriptFolderID = scriptFolderID

	// Raggruppa clip per categoria
	groupedClips := d.groupByCategory(clips)

	// Processa ogni categoria
	for category, clips := range groupedClips {
		logger.Info("Processing category",
			zap.String("category", category),
			zap.Int("clip_count", len(clips)),
		)

		// Crea sottocartella per categoria
		categoryFolderID, err := d.createSubFolder(ctx, scriptFolderID, category)
		if err != nil {
			logger.Warn("Failed to create category folder",
				zap.String("category", category),
				zap.Error(err),
			)
			categoryFolderID = scriptFolderID // Fallback alla cartella script
		}

		// Download clip
		for _, clip := range clips {
			result := d.downloadSingleClip(ctx, clip, categoryFolderID, category)
			report.Results = append(report.Results, result)

			if result.Success {
				report.SuccessCount++
			} else {
				report.FailedCount++
				report.Errors = append(report.Errors, result.Error)
			}
		}
	}

	report.CompletedAt = time.Now()
	report.Duration = report.CompletedAt.Sub(report.StartedAt)

	logger.Info("Artlist download and organization completed",
		zap.String("script_id", scriptID),
		zap.Int("success", report.SuccessCount),
		zap.Int("failed", report.FailedCount),
		zap.Duration("duration", report.Duration),
	)

	return report, nil
}

// ClipToDownload rappresenta una clip da scaricare
type ClipToDownload struct {
	ClipID     string `json:"clip_id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Category   string `json:"category"`
	Duration   int    `json:"duration"`
	Source     string `json:"source"` // artlist, youtube, tiktok
}

// DownloadReport report del download
type DownloadReport struct {
	ScriptID       string          `json:"script_id"`
	ScriptFolderID string          `json:"script_folder_id"`
	Results        []ClipResult    `json:"results"`
	SuccessCount   int             `json:"success_count"`
	FailedCount    int             `json:"failed_count"`
	Errors         []string        `json:"errors,omitempty"`
	StartedAt      time.Time       `json:"started_at"`
	CompletedAt    time.Time       `json:"completed_at"`
	Duration       time.Duration   `json:"duration"`
}

// ClipResult risultato del download di una clip
type ClipResult struct {
	ClipID    string `json:"clip_id"`
	Category  string `json:"category"`
	Success   bool   `json:"success"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	Error     string `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
}

// downloadSingleClip scarica una singola clip
func (d *Downloader) downloadSingleClip(ctx context.Context, clip ClipToDownload, folderID, category string) ClipResult {
	startTime := time.Now()
	result := ClipResult{
		ClipID:   clip.ClipID,
		Category: category,
	}

	logger.Info("Downloading clip",
		zap.String("clip_id", clip.ClipID),
		zap.String("category", category),
	)

	// Ottieni URL download dal database
	downloadURL, err := d.getDownloadURL(clip.ClipID)
	if err != nil {
		result.Error = fmt.Sprintf("failed to get download URL: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	// Scarica file temporaneo
	tempPath, err := d.downloadTemp(ctx, downloadURL, clip.ClipID)
	if err != nil {
		result.Error = fmt.Sprintf("download failed: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	// Carica su Drive
	driveFileID, err := d.uploadToDrive(ctx, tempPath, clip.Title, folderID)
	if err != nil {
		result.Error = fmt.Sprintf("Drive upload failed: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	// Pulisci file temporaneo
	os.Remove(tempPath)

	result.Success = true
	result.DriveFileID = driveFileID
	result.FilePath = tempPath
	result.Duration = time.Since(startTime)

	logger.Info("Clip downloaded and uploaded to Drive",
		zap.String("clip_id", clip.ClipID),
		zap.String("drive_file_id", driveFileID),
		zap.Duration("duration", result.Duration),
	)

	return result
}

// ensureDriveFolder crea o ottiene cartella Drive
func (d *Downloader) ensureDriveFolder(ctx context.Context, folderName string) (string, error) {
	// Prova a ottenere la cartella esistente
	folderID, err := d.driveClient.GetOrCreateFolder(ctx, folderName, "")
	if err == nil {
		return folderID, nil
	}

	// Crea nuova cartella
	folderID, err = d.driveClient.CreateFolder(ctx, folderName, "")
	if err != nil {
		return "", fmt.Errorf("failed to create folder: %w", err)
	}

	return folderID, nil
}

// createSubFolder crea una sottocartella
func (d *Downloader) createSubFolder(ctx context.Context, parentID, folderName string) (string, error) {
	folderID, err := d.driveClient.CreateFolder(ctx, folderName, parentID)
	if err != nil {
		return "", fmt.Errorf("failed to create subfolder: %w", err)
	}
	return folderID, nil
}

// groupByCategory raggruppa clip per categoria
func (d *Downloader) groupByCategory(clips []ClipToDownload) map[string][]ClipToDownload {
	grouped := make(map[string][]ClipToDownload)

	for _, clip := range clips {
		category := clip.Category
		if category == "" {
			category = "Uncategorized"
		}
		grouped[category] = append(grouped[category], clip)
	}

	return grouped
}

// getDownloadURL ottiene URL di download dal database Artlist
func (d *Downloader) getDownloadURL(clipID string) (string, error) {
	db, err := sql.Open("sqlite3", d.dbPath)
	if err != nil {
		return "", fmt.Errorf("failed to open Artlist DB: %w", err)
	}
	defer db.Close()

	var url string
	err = db.QueryRow(`
		SELECT url FROM video_links 
		WHERE video_id = ? AND downloaded = 1
	`, clipID).Scan(&url)

	if err != nil {
		return "", fmt.Errorf("clip not found in Artlist DB: %w", err)
	}

	return url, nil
}

// downloadTemp scarica file temporaneamente
func (d *Downloader) downloadTemp(ctx context.Context, url, clipID string) (string, error) {
	tempDir := "/tmp/velox/artlist_downloads"
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Usa wget o curl per download
	tempPath := filepath.Join(tempDir, fmt.Sprintf("%s.mp4", clipID))

	// Implementazione semplificata - in produzione usare HTTP client Go
	cmd := fmt.Sprintf("wget -q -O %s %s", tempPath, url)
	if err := exec.CommandContext(ctx, "bash", "-c", cmd).Run(); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	return tempPath, nil
}

// uploadToDrive carica file su Drive
func (d *Downloader) uploadToDrive(ctx context.Context, filePath, title, folderID string) (string, error) {
	fileID, err := d.driveClient.UploadFile(ctx, filePath, folderID, title)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	return fileID, nil
}
