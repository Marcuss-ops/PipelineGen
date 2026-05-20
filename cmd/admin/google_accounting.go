package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
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

func runGoogleGenerateVideo(args []string) error {
	fs := flag.NewFlagSet("google-generate-video", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	videoID := fs.String("video-id", "", "Google Vids project ID")
	prompt := fs.String("prompt", "", "Generation prompt")
	account := fs.String("account", "", "Google account/session name")
	headless := fs.Bool("headless", true, "Run Playwright headless")
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

	cfg, _, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

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
