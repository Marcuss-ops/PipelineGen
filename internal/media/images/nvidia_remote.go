package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	googleaccounting "github.com/Marcuss-ops/PipelineGen/internal/platform/googleaccounting"
)

// RemoteImageJob represents the job status response from the remote image endpoint.
// The status endpoint returns "id", while the start endpoint returns "job_id".
type RemoteImageJob struct {
	ID     string                 `json:"id"`
	JobID  string                 `json:"job_id,omitempty"`
	Status string                 `json:"status"`
	Result map[string]interface{} `json:"result,omitempty"`
	Error  string                 `json:"error,omitempty"`
}

// GenerateRemoteImage generates an image via the remote Google Flow endpoint.
// The remote service runs the generation, then we poll job status and download
// the artifact files directly from /v1/jobs/{job_id}/artifact/{name}.
func (s *Service) GenerateRemoteImage(ctx context.Context, slug, prompt, style, model string, width, height int, tags []string, skipDrive bool) (*models.ImageAsset, error) {
	if s.remoteImageEndpointURL == "" {
		return nil, fmt.Errorf("remote image endpoint URL not configured")
	}

	s.log.Info("GenerateRemoteImage: starting remote generation",
		zap.String("slug", slug),
		zap.String("style", style),
		zap.String("endpoint", s.remoteImageEndpointURL),
	)

	// Apply style from registry if provided
	styledPrompt := prompt
	if s.styleRegistry != nil && style != "" {
		styledPrompt = s.styleRegistry.ApplyStyle(prompt, style)
	}

	// Build request payload for remote endpoint
	reqPayload := map[string]any{
		"project_id": "velox-test",
		"prompt":     styledPrompt,
	}
	if style != "" {
		reqPayload["style"] = style
	}
	if model != "" {
		reqPayload["extra"] = map[string]any{
			"model": model,
		}
	}

	jsonPayload, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal remote request failed: %w", err)
	}

	// POST /v1/generate
	startURL := strings.TrimRight(s.remoteImageEndpointURL, "/") + "/v1/generate"
	s.log.Info("GenerateRemoteImage: calling remote endpoint", zap.String("url", startURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, startURL, bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("create remote request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote endpoint POST failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read remote response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote endpoint error (status %d): %s", resp.StatusCode, string(body))
	}

	var startResp struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &startResp); err != nil {
		return nil, fmt.Errorf("decode remote start response failed: %w", err)
	}

	if startResp.JobID == "" {
		return nil, fmt.Errorf("remote start response missing job_id")
	}

	// Wait for job completion
	job, pollErr := s.waitForRemoteJob(ctx, startResp.JobID)
	if pollErr != nil {
		return nil, fmt.Errorf("remote job failed or timed out: %w", pollErr)
	}

	imageNames := extractRemoteImageNames(job)
	if len(imageNames) == 0 {
		return nil, fmt.Errorf("remote job completed but no image artifacts returned")
	}

	baseURL := strings.TrimRight(s.remoteImageEndpointURL, "/")
	description := fmt.Sprintf("AI generated image via Google Flow for prompt: %s", prompt)
	generator := "google-flow"

	var firstAsset *models.ImageAsset
	for idx, imageName := range imageNames {
		imageURL := fmt.Sprintf("%s/v1/jobs/%s/artifact/%s", baseURL, startResp.JobID, url.PathEscape(imageName))
		s.log.Info("GenerateRemoteImage: downloading artifact",
			zap.String("job_id", startResp.JobID),
			zap.String("artifact", imageName),
			zap.String("url", imageURL),
		)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
		if err != nil {
			s.log.Warn("GenerateRemoteImage: failed to build artifact request", zap.String("artifact", imageName), zap.Error(err))
			continue
		}
		req.Header.Set("User-Agent", userAgent)

		imgResp, err := s.client.Do(req)
		if err != nil {
			s.log.Warn("GenerateRemoteImage: artifact download failed", zap.String("artifact", imageName), zap.Error(err))
			continue
		}

		content, readErr := io.ReadAll(io.LimitReader(imgResp.Body, 50<<20))
		imgResp.Body.Close()
		if readErr != nil {
			s.log.Warn("GenerateRemoteImage: failed to read artifact body", zap.String("artifact", imageName), zap.Error(readErr))
			continue
		}
		if imgResp.StatusCode != http.StatusOK {
			s.log.Warn("GenerateRemoteImage: artifact download returned non-200",
				zap.String("artifact", imageName),
				zap.Int("status", imgResp.StatusCode),
			)
			continue
		}

		filename := filepath.Base(imageName)
		if filename == "" || filename == "." {
			filename = fmt.Sprintf("remote_%s_%d.jpg", startResp.JobID[:8], idx)
		}

		asset, ingestErr := s.IngestImage(ctx, slug, style, startResp.JobID, bytes.NewReader(content), filename, generator, description, tags, skipDrive, false)
		if ingestErr != nil {
			s.log.Warn("GenerateRemoteImage: ingest failed for artifact",
				zap.String("artifact", imageName),
				zap.Error(ingestErr),
			)
			continue
		}

		if firstAsset == nil {
			firstAsset = asset
		}

		s.log.Info("GenerateRemoteImage: successfully ingested artifact",
			zap.String("job_id", startResp.JobID),
			zap.String("artifact", imageName),
			zap.String("asset_hash", asset.Hash),
			zap.String("path_rel", asset.PathRel),
		)
	}

	if firstAsset == nil {
		return nil, fmt.Errorf("remote job completed but no artifacts could be downloaded and ingested")
	}

	return firstAsset, nil
}

// waitForRemoteJob polls the remote job status until completion or timeout.
func (s *Service) waitForRemoteJob(ctx context.Context, jobID string) (*RemoteImageJob, error) {
	statusURL := strings.TrimRight(s.remoteImageEndpointURL, "/") + "/v1/jobs/" + jobID

	const maxWait = 5 * time.Minute
	deadlineCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		req, err := http.NewRequestWithContext(deadlineCtx, http.MethodGet, statusURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create remote job status request failed: %w", err)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("remote job status request failed: %w", err)
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read remote job status failed: %w", readErr)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("remote job status failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var job RemoteImageJob
		if err := json.Unmarshal(body, &job); err != nil {
			return nil, fmt.Errorf("decode remote job status failed: %w", err)
		}

		switch strings.ToLower(job.Status) {
		case string(googleaccounting.StatusSucceeded), "done", "completed":
			return &job, nil
		case "failed":
			errMsg := job.Error
			if errMsg == "" {
				errMsg = "remote job failed"
			}
			return nil, fmt.Errorf("%s", errMsg)
		}

		select {
		case <-deadlineCtx.Done():
			return nil, deadlineCtx.Err()
		case <-ticker.C:
		}
	}
}

func extractRemoteImageNames(job *RemoteImageJob) []string {
	if job == nil || job.Result == nil {
		return nil
	}

	if names := stringsFromAny(job.Result["images"]); len(names) > 0 {
		return dedupeNonEmptyStrings(names)
	}

	if artifacts := job.Result["artifacts"]; artifacts != nil {
		switch typed := artifacts.(type) {
		case []any:
			var imageNames []string
			var otherNames []string
			for _, item := range typed {
				if artifact, ok := item.(map[string]any); ok {
					kind, _ := artifact["kind"].(string)
					if strings.EqualFold(kind, "downloaded_images") {
						imageNames = append(imageNames, stringsFromAny(artifact["value"])...)
						continue
					}
					if name, _ := artifact["name"].(string); name != "" {
						otherNames = append(otherNames, name)
					}
				}
			}
			return dedupeNonEmptyStrings(append(imageNames, otherNames...))
		case []map[string]any:
			var imageNames []string
			var otherNames []string
			for _, artifact := range typed {
				kind, _ := artifact["kind"].(string)
				if strings.EqualFold(kind, "downloaded_images") {
					imageNames = append(imageNames, stringsFromAny(artifact["value"])...)
					continue
				}
				if name, _ := artifact["name"].(string); name != "" {
					otherNames = append(otherNames, name)
				}
			}
			return dedupeNonEmptyStrings(append(imageNames, otherNames...))
		}
	}

	return nil
}

func stringsFromAny(v any) []string {
	switch typed := v.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	default:
		return nil
	}
}

func dedupeNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
