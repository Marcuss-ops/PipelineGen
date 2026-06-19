package images

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// globalNvidiaSem limits concurrent NVIDIA NIM/flux image generation requests system-wide to avoid overloading GPU/VRAM.
var globalNvidiaSem = make(chan struct{}, 2)

// GenerateAImage generates an AI image using NVIDIA NIM and stores it under a
// prompt-derived slug. Equivalent to GenerateStyledImage(ctx, textutil.Slugify(prompt), ...).
func (s *Service) GenerateAImage(ctx context.Context, prompt, style, model string, width, height int, tags []string, skipDrive bool) (*media.ImageAsset, error) {
	slug := textutil.Slugify(prompt)
	if len(slug) > 50 {
		slug = slug[:50]
	}
	return s.GenerateStyledImage(ctx, slug, prompt, style, model, width, height, tags, skipDrive)
}

// GenerateStyledImage generates an AI image and stores it under the given slug
// rather than deriving the slug from the prompt. This lets callers control
// the storage group (e.g., "gothic/introduction" instead of "cinematic-documentary...").
//
// The slug is used as SubjectID in the DB and as the filesystem directory for the image.
// All other parameters match GenerateAImage.
func (s *Service) GenerateStyledImage(ctx context.Context, slug, prompt, style, model string, width, height int, tags []string, skipDrive bool) (*media.ImageAsset, error) {
	// Acquire semaphore slot to prevent GPU/CPU VRAM saturation from concurrent image generations
	select {
	case globalNvidiaSem <- struct{}{}:
		defer func() { <-globalNvidiaSem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Validate or clamp dimensions for all cloud models
	if model == "flux-1-dev" || model == "flux.1-schnell" || model == "flux-1-schnell" || model == "flux1-schnell" || model == "flux-2-klein" || model == "" {
		// Supported: 768, 832, 896, 960, 1024, 1088, 1152, 1216, 1280 or 1344
		if width > 1344 {
			width = 1344
		} else if width < 768 {
			width = 768
		} else {
			// Find closest valid
			width = (width / 64) * 64
		}

		if height > 1344 {
			height = 1344
		} else if height < 768 {
			height = 768
		} else {
			height = (height / 64) * 64
		}

		// Ensure specifically supported wide format for YouTube-like aspect ratio
		if width == 1344 && height == 1088 {
			// fine
		} else if width == 1344 && height > 768 {
			height = 768 // 1344x768 is ~1.75:1 (close to 16:9)
		}
	}

	var payload map[string]any
	var useCloudAuth bool
	var sourceLabel string
	var invokeURL string
	resolvedModel := strings.ToLower(strings.TrimSpace(model))

	// Default resolution if not provided
	if width <= 0 {
		width = 1024
	}
	if height <= 0 {
		height = 1024
	}

	if resolvedModel == "" {
		if s.nvidiaAPIKey != "" && s.nvidiaAPIKey != "PASTE_YOUR_NVIDIA_API_KEY_HERE" {
			resolvedModel = "flux-1-dev"
		} else {
			resolvedModel = "local-nim"
		}
	}

	switch resolvedModel {
	case "flux-1-dev":
		invokeURL = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.1-dev"
		payload = map[string]any{
			"prompt":    prompt,
			"mode":      "base",
			"cfg_scale": 3.5,
			"width":     width,
			"height":    height,
			"seed":      0,
			"steps":     50,
		}
		useCloudAuth = true
		sourceLabel = "flux-1-dev"

	case "flux.1-schnell", "flux-1-schnell", "flux1-schnell":
		invokeURL = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.1-schnell"
		payload = map[string]any{
			"prompt": prompt,
			"width":  width,
			"height": height,
			"seed":   0,
			"steps":  4,
		}
		useCloudAuth = true
		sourceLabel = "flux.1-schnell"

	case "flux-2-klein":
		invokeURL = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.2-klein-4b"
		payload = map[string]any{
			"prompt": prompt,
			"width":  width,
			"height": height,
			"seed":   0,
			"steps":  4,
		}
		useCloudAuth = true
		sourceLabel = "flux-2-klein"

	case "local-nim", "":
		invokeURL = s.nvidiaLocalNIMURL
		if invokeURL == "" {
			invokeURL = "http://localhost:8000/v1/infer"
		}
		payload = map[string]any{
			"prompt": prompt,
			"mode":   "base",
			"seed":   0,
			"steps":  30,
		}
		useCloudAuth = false
		sourceLabel = "nvidia-local"
		resolvedModel = "local-nim"

	default:
		return nil, fmt.Errorf("unsupported model: %s", model)
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequest("POST", invokeURL, bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, err
	}

	if useCloudAuth {
		if s.nvidiaAPIKey == "" || s.nvidiaAPIKey == "PASTE_YOUR_NVIDIA_API_KEY_HERE" {
			return nil, fmt.Errorf("NVIDIA API key not configured (required for cloud models)")
		}
		req.Header.Set("Authorization", "Bearer "+s.nvidiaAPIKey)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s error (status %d): %s", resolvedModel, resp.StatusCode, string(body))
	}

	var responseBody struct {
		Image     string `json:"image"`
		Artifacts []struct {
			Base64 string `json:"base64"`
		} `json:"artifacts"`
	}

	if err := json.Unmarshal(body, &responseBody); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var base64Data string
	if responseBody.Image != "" {
		base64Data = responseBody.Image
	} else if len(responseBody.Artifacts) > 0 {
		base64Data = responseBody.Artifacts[0].Base64
	}

	if base64Data == "" {
		return nil, fmt.Errorf("no image data found in response")
	}

	// Decode base64
	imageData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 image: %w", err)
	}

	// Ingest image — slug is provided by the caller (style-based)
	filename := fmt.Sprintf("%s_%d.png", sourceLabel, time.Now().Unix())
	description := fmt.Sprintf("AI generated image via %s for prompt: %s", resolvedModel, prompt)

	return s.IngestImage(ctx, slug, style, "", bytes.NewReader(imageData), filename, sourceLabel, description, tags, skipDrive, false)
}
