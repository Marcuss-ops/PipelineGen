package googleaccounting

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/config"
	"velox/go-master/internal/pkg/googleaccounting"
	"velox/go-master/internal/pkg/mediascan"
	"velox/go-master/internal/upload/drive"
)

type googlePublishMode string

const (
	googlePublishModeVids googlePublishMode = "vids"
	googlePublishModeFlow googlePublishMode = "flow"
)

type googlePublishOptions struct {
	Mode          googlePublishMode
	VideoID       string
	Prompt        string
	ProjectID     string
	Style         string
	Account       string
	Headless      bool
	UploadDrive   bool
	DriveFolderID string
	Timeout       time.Duration
	PollInterval  time.Duration
	APIBASE       string
}

type googlePublishResult struct {
	JobID    string
	Status   string
	FilePath string
	Files    []string
}

func runGoogleGenerateVideo(args []string) error {
	opts, err := parseGooglePublishOptions(args, googlePublishModeVids, false)
	if err != nil {
		return err
	}
	return executeGooglePublish(opts)
}

func RunGenerateVideo(args []string) error {
	return runGoogleGenerateVideo(args)
}

func runGoogleGenerateFlowImages(args []string) error {
	opts, err := parseGooglePublishOptions(args, googlePublishModeFlow, false)
	if err != nil {
		return err
	}
	return executeGooglePublish(opts)
}

func RunGenerateFlowImages(args []string) error {
	return runGoogleGenerateFlowImages(args)
}

func runGooglePublish(args []string) error {
	fs := flag.NewFlagSet("google-publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	mode := fs.String("mode", "vids", "Publish mode: vids or flow")
	videoID := fs.String("video-id", "", "Google Vids project ID")
	prompt := fs.String("prompt", "", "Generation prompt")
	projectID := fs.String("project-id", "", "Flow project ID")
	style := fs.String("style", "", "Flow style preset")
	account := fs.String("account", "", "Google account/session name")
	headless := fs.Bool("headless", true, "Run Playwright headless")
	uploadDrive := fs.Bool("upload-drive", true, "Upload produced files to Google Drive")
	driveFolderID := fs.String("drive-folder-id", "", "Google Drive folder ID for uploads")
	timeout := fs.Duration("timeout", 30*time.Minute, "Maximum time to wait for job completion")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "Polling interval for job status")
	apiBase := fs.String("api-base", "", "Go API base URL (default: config server host/port)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := googlePublishOptions{
		Mode:          googlePublishMode(strings.ToLower(strings.TrimSpace(*mode))),
		VideoID:       *videoID,
		Prompt:        *prompt,
		ProjectID:     *projectID,
		Style:         *style,
		Account:       *account,
		Headless:      *headless,
		UploadDrive:   *uploadDrive,
		DriveFolderID: *driveFolderID,
		Timeout:       *timeout,
		PollInterval:  *pollInterval,
		APIBASE:       *apiBase,
	}
	return executeGooglePublish(&opts)
}

func RunPublish(args []string) error {
	return runGooglePublish(args)
}

func parseGooglePublishOptions(args []string, mode googlePublishMode, defaultUpload bool) (*googlePublishOptions, error) {
	fs := flag.NewFlagSet(string(mode), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	videoID := fs.String("video-id", "", "Google Vids project ID")
	prompt := fs.String("prompt", "", "Generation prompt")
	projectID := fs.String("project-id", "", "Flow project ID")
	style := fs.String("style", "", "Flow style preset")
	account := fs.String("account", "", "Google account/session name")
	headless := fs.Bool("headless", true, "Run Playwright headless")
	uploadDrive := fs.Bool("upload-drive", defaultUpload, "Upload produced files to Google Drive")
	driveFolderID := fs.String("drive-folder-id", "", "Google Drive folder ID for uploads")
	timeout := fs.Duration("timeout", 30*time.Minute, "Maximum time to wait for job completion")
	pollInterval := fs.Duration("poll-interval", 5*time.Second, "Polling interval for job status")
	apiBase := fs.String("api-base", "", "Go API base URL (default: config server host/port)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return &googlePublishOptions{
		Mode:          mode,
		VideoID:       *videoID,
		Prompt:        *prompt,
		ProjectID:     *projectID,
		Style:         *style,
		Account:       *account,
		Headless:      *headless,
		UploadDrive:   *uploadDrive,
		DriveFolderID: *driveFolderID,
		Timeout:       *timeout,
		PollInterval:  *pollInterval,
		APIBASE:       *apiBase,
	}, nil
}

func executeGooglePublish(opts *googlePublishOptions) error {
	if opts == nil {
		return fmt.Errorf("missing publish options")
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	ctx := context.Background()

	baseURL := strings.TrimRight(opts.APIBASE, "/")
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	}

	startPath, payload, err := buildGoogleStartPayload(opts)
	if err != nil {
		return err
	}
	startResp, err := startGoogleJob(baseURL, startPath, payload)
	if err != nil {
		return err
	}
	fmt.Printf("Started Google publish job: job_id=%s status=%s mode=%s\n", startResp.JobID, startResp.Status, opts.Mode)

	job, err := waitForGoogleJob(baseURL, startResp.JobID, opts.Timeout, opts.PollInterval)
	if err != nil {
		return err
	}

	return publishGoogleJob(ctx, cfg, log, opts, job)
}

func buildGoogleStartPayload(opts *googlePublishOptions) (string, map[string]any, error) {
	if opts == nil {
		return "", nil, fmt.Errorf("missing publish options")
	}

	reqBody := map[string]any{
		"headless": opts.Headless,
	}
	if strings.TrimSpace(opts.Account) != "" {
		reqBody["account"] = opts.Account
	}

	switch opts.Mode {
	case googlePublishModeVids:
		if strings.TrimSpace(opts.VideoID) == "" {
			return "", nil, fmt.Errorf("--video-id is required for mode vids")
		}
		if strings.TrimSpace(opts.Prompt) == "" {
			return "", nil, fmt.Errorf("--prompt is required for mode vids")
		}
		reqBody["video_id"] = opts.VideoID
		reqBody["prompt"] = opts.Prompt
		return "/api/google-accounting/generate-video", reqBody, nil
	case googlePublishModeFlow:
		if strings.TrimSpace(opts.Prompt) == "" {
			return "", nil, fmt.Errorf("--prompt is required for mode flow")
		}
		reqBody["prompt"] = opts.Prompt
		if strings.TrimSpace(opts.ProjectID) != "" {
			reqBody["project_id"] = opts.ProjectID
		}
		if strings.TrimSpace(opts.Style) != "" {
			reqBody["style"] = opts.Style
		}
		return "/api/google-accounting/generate-flow-images", reqBody, nil
	default:
		return "", nil, fmt.Errorf("unsupported mode: %s", opts.Mode)
	}
}

func startGoogleJob(baseURL, path string, payload map[string]any) (*googleaccounting.StartResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("start request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var startResp googleaccounting.StartResponse
	if err := json.Unmarshal(respBody, &startResp); err != nil {
		return nil, fmt.Errorf("decode start response failed: %w (body: %s)", err, string(respBody))
	}
	if startResp.JobID == "" {
		return nil, fmt.Errorf("missing job_id in start response: %s", string(respBody))
	}
	return &startResp, nil
}

func waitForGoogleJob(baseURL, jobID string, timeout, pollInterval time.Duration) (*googleaccounting.Job, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	statusURL := fmt.Sprintf("%s/api/google-accounting/status/%s", strings.TrimRight(baseURL, "/"), url.PathEscape(jobID))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		statusReq, err := http.NewRequest(http.MethodGet, statusURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create status request failed: %w", err)
		}

		statusResp, err := client.Do(statusReq)
		if err != nil {
			return nil, fmt.Errorf("status request failed: %w", err)
		}

		statusBody, _ := io.ReadAll(statusResp.Body)
		statusResp.Body.Close()

		var job googleaccounting.Job
		if err := json.Unmarshal(statusBody, &job); err != nil {
			return nil, fmt.Errorf("decode status response failed: %w (body: %s)", err, string(statusBody))
		}

		printGoogleStatus(job)
		switch strings.ToLower(string(job.Status)) {
		case "done", "completed":
			return &job, nil
		case "failed":
			if job.Error == "" {
				job.Error = "job failed"
			}
			return &job, fmt.Errorf("job failed: %s", job.Error)
		}

		time.Sleep(pollInterval)
	}

	return nil, fmt.Errorf("timeout waiting for job completion after %s", timeout.String())
}

func printGoogleStatus(job googleaccounting.Job) {
	fmt.Printf("Poll: status=%s", job.Status)
	if job.Progress > 0 {
		fmt.Printf(" progress=%d", job.Progress)
	}
	if job.CurrentStep != "" {
		fmt.Printf(" current_step=%s", job.CurrentStep)
	}
	if job.Attempts > 0 {
		fmt.Printf(" attempts=%d", job.Attempts)
	}
	if job.LastLog != "" {
		fmt.Printf(" last_log=%s", job.LastLog)
	}
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
}

func publishGoogleJob(ctx context.Context, cfg *config.Config, log *zap.Logger, opts *googlePublishOptions, job *googleaccounting.Job) error {
	if job == nil {
		return fmt.Errorf("missing job status")
	}

	switch strings.ToLower(string(job.Status)) {
	case "done", "completed":
		switch opts.Mode {
		case googlePublishModeVids:
			if job.FilePath == "" {
				return fmt.Errorf("video publish completed but no file_path returned")
			}
			return publishGoogleFiles(ctx, cfg, log, opts, []string{job.FilePath}, "video/mp4")
		case googlePublishModeFlow:
			if len(job.Files) == 0 {
				return fmt.Errorf("flow publish completed but no files returned")
			}
			return publishGoogleFiles(ctx, cfg, log, opts, job.Files, "image/*")
		default:
			return fmt.Errorf("unsupported mode: %s", opts.Mode)
		}
	case "failed":
		if job.Error == "" {
			job.Error = "job failed"
		}
		return fmt.Errorf("job failed: %s", job.Error)
	default:
		return fmt.Errorf("unexpected terminal status: %s", job.Status)
	}
}

func publishGoogleFiles(ctx context.Context, cfg *config.Config, log *zap.Logger, opts *googlePublishOptions, files []string, mimeHint string) error {
        if len(files) == 0 {
                return fmt.Errorf("no files to publish")
        }

        kindLabel := "video"
        if strings.HasPrefix(mimeHint, "image/") {
                kindLabel = "image"
        }
        fmt.Printf("✅ %s files ready:\n", strings.Title(kindLabel))
        for _, filePath := range files {
                fmt.Printf(" - %s\n", filePath)
                printMediaURL(cfg.GoogleAccounting.DownloadDir, filePath)
                if opts.UploadDrive {
                        link, err := uploadGeneratedMedia(ctx, cfg, log, filePath, opts.DriveFolderID, mimeHint, opts.Style, opts.Prompt)
                        if err != nil {
                                return err
                        }
                        if link != "" {
                                fmt.Printf("   drive=%s\n", link)
                        }
                }
        }
        return nil
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

	files, err := mediascan.ScanDirectory(*dir, "")
	if err != nil {
		return fmt.Errorf("scan dir failed: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no uploadable media files found in %s", *dir)
	}

	fmt.Printf("Uploading %d files from %s to Drive folder %s\n", len(files), *dir, *driveFolderID)
	for _, f := range files {
		filePath := f.Path
		filename := filepath.Base(filePath)
		res, skipped, err := uploader.UploadFileIfChanged(ctx, filePath, *driveFolderID, filename)
		if err != nil {
			return fmt.Errorf("upload failed for %s: %w", filePath, err)
		}
		if skipped {
			fmt.Printf("SKIP %s\n", filePath)
		} else {
			fmt.Printf("OK %s\n", filePath)
		}
		if res.WebViewLink != "" {
			fmt.Printf("   drive=%s\n", res.WebViewLink)
		} else if res.DownloadLink != "" {
			fmt.Printf("   drive=%s\n", res.DownloadLink)
		}
	}

	return nil
}

func RunUploadMedia(args []string) error {
	return runGoogleUploadMedia(args)
}

func uploadGeneratedMedia(ctx context.Context, cfg *config.Config, log *zap.Logger, localPath, overrideFolderID, mimeHint, style, prompt string) (string, error) {
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
		folderID = strings.TrimSpace(cfg.Drive.RootFolder())
	}

        if folderID == "" {
                return "", fmt.Errorf("drive folder id is required for upload")
        }

        // Apply Style -> Prompt hierarchy if using default root
        if overrideFolderID == "" {
                if style != "" {
                        fid, err := uploader.GetOrCreateFolder(ctx, style, folderID)
                        if err == nil {
                                folderID = fid
                        }
                }
                if prompt != "" {
                        promptSlug := slugify(prompt)
                        if len(promptSlug) > 100 {
                                promptSlug = promptSlug[:100]
                        }
                        fid, err := uploader.GetOrCreateFolder(ctx, promptSlug, folderID)
                        if err == nil {
                                folderID = fid
                        }
                }
        }

        filename := filepath.Base(resolvedPath)
        result, skipped, err := uploader.UploadFileIfChanged(ctx, resolvedPath, folderID, filename)
        if err != nil {
                return "", err
        }
        if skipped {
                log.Info("drive upload skipped, matching file already exists", zap.String("file_path", resolvedPath), zap.String("folder_id", folderID), zap.String("filename", filename))
        }

        if result.WebViewLink != "" {
                return result.WebViewLink, nil
        }
        return result.DownloadLink, nil
}

func slugify(s string) string {
        s = strings.ToLower(s)
        s = strings.TrimSpace(s)
        parts := strings.Fields(s)
        return strings.Join(parts, "-")
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
