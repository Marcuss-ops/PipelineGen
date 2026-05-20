package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/upload/drive"
)

type googleGenerateVideoResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type googleJobStatusResponse struct {
	Status   string   `json:"status"`
	Error    string   `json:"error,omitempty"`
	FilePath string   `json:"file_path,omitempty"`
	Files    []string `json:"files,omitempty"`
}

type googleGenerateFlowImagesResponse struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type mediaUploadResult struct {
	LocalPath string
	WebLink   string
}

func runGoogleGenerateVideo(args []string) error {
	fs := flag.NewFlagSet("google-generate-video", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	videoID := fs.String("video-id", "", "Google Vids project ID")
	prompt := fs.String("prompt", "", "Generation prompt")
	account := fs.String("account", "", "Google account/session name")
	headless := fs.Bool("headless", true, "Run Playwright headless")
	uploadDrive := fs.Bool("upload-drive", false, "Upload produced files to Google Drive")
	driveFolderID := fs.String("drive-folder-id", "", "Google Drive folder ID for uploads")
	timeout := fs.Duration("timeout", 30*time.Minute, "Maximum time to wait for job completion")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "Polling interval for job status")
	apiBase := fs.String("api-base", "", "Go API base URL (default: config server host/port)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *videoID == "" {
		return fmt.Errorf("--video-id is required")
	}
	if *prompt == "" {
		return fmt.Errorf("--prompt is required")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	ctx := context.Background()

	baseURL := strings.TrimRight(*apiBase, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	}

	reqBody := map[string]any{
		"video_id": *videoID,
		"prompt":   *prompt,
		"headless": *headless,
	}
	if strings.TrimSpace(*account) != "" {
		reqBody["account"] = *account
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request failed: %w", err)
	}

	startURL := fmt.Sprintf("%s/api/google-accounting/generate-video", baseURL)
	req, err := http.NewRequest(http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var startResp googleGenerateVideoResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return fmt.Errorf("decode start response failed: %w (body: %s)", err, string(respBody))
	}
	if startResp.JobID == "" {
		return fmt.Errorf("missing job_id in start response: %s", string(respBody))
	}

	fmt.Printf("Started Google Vids generation: job_id=%s status=%s\n", startResp.JobID, startResp.Status)

	statusURL := fmt.Sprintf("%s/api/google-accounting/status/%s", baseURL, url.PathEscape(startResp.JobID))
	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		statusReq, err := http.NewRequest(http.MethodGet, statusURL, nil)
		if err != nil {
			return fmt.Errorf("create status request failed: %w", err)
		}

		statusResp, err := client.Do(statusReq)
		if err != nil {
			return fmt.Errorf("status request failed: %w", err)
		}

		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()

		var job googleJobStatusResponse
		if err := json.Unmarshal(statusBody, &job); err != nil {
			return fmt.Errorf("decode status response failed: %w (body: %s)", err, string(statusBody))
		}

		fmt.Printf("Poll: status=%s", job.Status)
		if job.FilePath != "" {
			fmt.Printf(" file_path=%s", job.FilePath)
		}
		if len(job.Files) > 0 {
			fmt.Printf(" files=%d", len(job.Files))
		}
		if job.Error != "" {
			fmt.Printf(" error=%s", job.Error)
		}
		fmt.Println()

		switch strings.ToLower(job.Status) {
		case "done", "completed":
			if job.FilePath != "" {
				fmt.Printf("✅ Video ready: %s\n", job.FilePath)
				printMediaURL(cfg.GoogleAccounting.DownloadDir, job.FilePath)
				if *uploadDrive {
					link, err := uploadGeneratedMedia(ctx, cfg, log, job.FilePath, *driveFolderID, "video/mp4")
					if err != nil {
						return err
					}
					if link != "" {
						fmt.Printf("   drive=%s\n", link)
					}
				}
			}
			return nil
		case "failed":
			if job.Error == "" {
				job.Error = "job failed"
			}
			return fmt.Errorf("job failed: %s", job.Error)
		}

		time.Sleep(*pollInterval)
	}

	return fmt.Errorf("timeout waiting for job completion after %s", timeout.String())
}

func runGoogleGenerateFlowImages(args []string) error {
	fs := flag.NewFlagSet("google-generate-flow-images", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	prompt := fs.String("prompt", "", "Flow generation prompt")
	projectID := fs.String("project-id", "", "Flow project ID")
	style := fs.String("style", "", "Flow style preset")
	account := fs.String("account", "", "Google account/session name")
	headless := fs.Bool("headless", true, "Run Playwright headless")
	uploadDrive := fs.Bool("upload-drive", false, "Upload produced files to Google Drive")
	driveFolderID := fs.String("drive-folder-id", "", "Google Drive folder ID for uploads")
	timeout := fs.Duration("timeout", 30*time.Minute, "Maximum time to wait for job completion")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "Polling interval for job status")
	apiBase := fs.String("api-base", "", "Go API base URL (default: config server host/port)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("--prompt is required")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	ctx := context.Background()

	baseURL := strings.TrimRight(*apiBase, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	}

	reqBody := map[string]any{
		"prompt":   *prompt,
		"headless": *headless,
	}
	if strings.TrimSpace(*projectID) != "" {
		reqBody["project_id"] = *projectID
	}
	if strings.TrimSpace(*style) != "" {
		reqBody["style"] = *style
	}
	if strings.TrimSpace(*account) != "" {
		reqBody["account"] = *account
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request failed: %w", err)
	}

	startURL := fmt.Sprintf("%s/api/google-accounting/generate-flow-images", baseURL)
	req, err := http.NewRequest(http.MethodPost, startURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var startResp googleGenerateFlowImagesResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return fmt.Errorf("decode start response failed: %w (body: %s)", err, string(respBody))
	}
	if startResp.JobID == "" {
		return fmt.Errorf("missing job_id in start response: %s", string(respBody))
	}

	fmt.Printf("Started Flow generation: job_id=%s status=%s\n", startResp.JobID, startResp.Status)

	statusURL := fmt.Sprintf("%s/api/google-accounting/status/%s", baseURL, url.PathEscape(startResp.JobID))
	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		statusReq, err := http.NewRequest(http.MethodGet, statusURL, nil)
		if err != nil {
			return fmt.Errorf("create status request failed: %w", err)
		}

		statusResp, err := client.Do(statusReq)
		if err != nil {
			return fmt.Errorf("status request failed: %w", err)
		}

		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()

		var job googleJobStatusResponse
		if err := json.Unmarshal(statusBody, &job); err != nil {
			return fmt.Errorf("decode status response failed: %w (body: %s)", err, string(statusBody))
		}

		fmt.Printf("Poll: status=%s", job.Status)
		if len(job.Files) > 0 {
			fmt.Printf(" files=%d", len(job.Files))
		}
		if job.Error != "" {
			fmt.Printf(" error=%s", job.Error)
		}
		fmt.Println()

		switch strings.ToLower(job.Status) {
		case "done", "completed":
			if len(job.Files) == 0 {
				fmt.Println("✅ Flow ready, but no files were returned.")
				return nil
			}
			fmt.Println("✅ Flow images ready:")
			for _, filePath := range job.Files {
				fmt.Printf(" - %s\n", filePath)
				printMediaURL(cfg.GoogleAccounting.DownloadDir, filePath)
				if *uploadDrive {
					link, err := uploadGeneratedMedia(ctx, cfg, log, filePath, *driveFolderID, "image/*")
					if err != nil {
						return err
					}
					if link != "" {
						fmt.Printf("   drive=%s\n", link)
					}
				}
			}
			return nil
		case "failed":
			if job.Error == "" {
				job.Error = "job failed"
			}
			return fmt.Errorf("job failed: %s", job.Error)
		}

		time.Sleep(*pollInterval)
	}

	return fmt.Errorf("timeout waiting for job completion after %s", timeout.String())
}

func runGoogleUploadMedia(args []string) error {
	fs := flag.NewFlagSet("google-upload-media", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("dir", "", "Directory containing generated media files")
	driveFolderID := fs.String("drive-folder-id", "", "Google Drive folder ID for uploads")
	apiBase := fs.String("api-base", "", "Reserved for parity with other commands")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dir) == "" {
		return fmt.Errorf("--dir is required")
	}
	if strings.TrimSpace(*driveFolderID) == "" {
		return fmt.Errorf("--drive-folder-id is required")
	}
	_ = apiBase

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx := context.Background()
	driveSvc, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		return fmt.Errorf("initializing drive service failed: %w", err)
	}
	uploader := &drive.Uploader{Service: driveSvc, Log: log}

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return fmt.Errorf("resolve dir failed: %w", err)
	}

	var files []string
	walkErr := filepath.WalkDir(absDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		switch ext {
		case ".mp4", ".mov", ".mkv", ".webm", ".jpg", ".jpeg", ".png", ".webp":
			files = append(files, path)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}
	sort.Strings(files)
	if len(files) == 0 {
		return fmt.Errorf("no uploadable media files found in %s", absDir)
	}

	fmt.Printf("Uploading %d files from %s to Drive folder %s\n", len(files), absDir, *driveFolderID)
	for _, filePath := range files {
		filename := filepath.Base(filePath)
		res, err := uploader.UploadFile(ctx, filePath, *driveFolderID, filename)
		if err != nil {
			return fmt.Errorf("upload failed for %s: %w", filePath, err)
		}
		fmt.Printf("OK %s\n", filePath)
		if res.WebViewLink != "" {
			fmt.Printf("   drive=%s\n", res.WebViewLink)
		} else if res.DownloadLink != "" {
			fmt.Printf("   drive=%s\n", res.DownloadLink)
		}
	}

	return nil
}

func uploadGeneratedMedia(ctx context.Context, cfg *config.Config, log *zap.Logger, localPath, overrideFolderID, mimeHint string) (string, error) {
	driveSvc, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("initializing drive service failed: %w", err)
	}

	uploader := &drive.Uploader{Service: driveSvc, Log: log}
	resolvedPath, err := filepath.Abs(localPath)
	if err != nil {
		return "", fmt.Errorf("resolve media path failed: %w", err)
	}

	folderID := strings.TrimSpace(overrideFolderID)
	if folderID == "" {
		switch {
		case strings.HasPrefix(mimeHint, "image/"):
			folderID = strings.TrimSpace(cfg.Drive.ImagesRootFolder)
		default:
			folderID = strings.TrimSpace(cfg.Drive.ClipsRootFolder)
			if folderID == "" {
				folderID = strings.TrimSpace(cfg.Drive.StockRootFolder)
			}
		}
	}

	if folderID == "" {
		return "", fmt.Errorf("drive folder id is required for upload")
	}

	filename := filepath.Base(resolvedPath)
	result, err := uploader.UploadFile(ctx, resolvedPath, folderID, filename)
	if err != nil {
		return "", err
	}

	if result.WebViewLink != "" {
		return result.WebViewLink, nil
	}
	return result.DownloadLink, nil
}

func printMediaURL(downloadDir string, filePath string) {
	if strings.TrimSpace(downloadDir) == "" || strings.TrimSpace(filePath) == "" {
		return
	}
	absBase, err := filepath.Abs(downloadDir)
	if err != nil {
		return
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(absBase, absFile)
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	fmt.Printf("   url=/media/google-accounting/%s\n", filepath.ToSlash(rel))
}
