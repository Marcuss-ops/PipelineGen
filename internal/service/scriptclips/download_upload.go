package scriptclips

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"velox/go-master/internal/stock"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"
	"velox/go-master/pkg/util"

	"go.uber.org/zap"
)

// validateYouTubeLinks sends YouTube search results to Ollama for validation
func (s *ScriptClipsService) validateYouTubeLinks(ctx context.Context, entityName string, results []stock.VideoResult) ([]stock.VideoResult, error) {
	var videosList strings.Builder
	for i, r := range results {
		videosList.WriteString(fmt.Sprintf("%d. Title: \"%s\"\n   URL: %s\n   Duration: %ds\n\n",
			i+1, r.Title, r.URL, r.Duration))
	}

	prompt := fmt.Sprintf(`You are an expert video curator for a documentary about %s.

I have found these YouTube videos that might be used as stock footage. Please evaluate each one and tell me which are RELEVANT and USEFUL for a documentary about %s.

YOUTUBE VIDEOS:
%s

For each video, respond with:
- "APPROVED" if it's relevant (interviews, biographical content, news about the topic, performance footage, etc.)
- "REJECTED" if it's not relevant (unrelated content, spam, wrong topic, poor quality)

Respond with ONLY a JSON object like this:
{
  "approved": [1, 3, 5],
  "rejected": [2, 4],
  "reasoning": "Brief explanation of your choices"
}

Focus on:
- Relevance to the topic: %s
- Video quality (longer videos are generally better for stock footage)
- Content type (interviews, documentaries, news reports are preferred)

JSON:`, entityName, entityName, videosList.String(), entityName)

	logger.Info("Sending YouTube links to Ollama for validation",
		zap.String("entity", entityName), zap.Int("links_count", len(results)))

	response, err := s.ollamaClient.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("Ollama validation failed: %w", err)
	}

	var validation struct {
		Approved  []int  `json:"approved"`
		Rejected  []int  `json:"rejected"`
		Reasoning string `json:"reasoning"`
	}

	jsonStr := response
	if strings.Contains(response, "```") {
		re := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
		matches := re.FindStringSubmatch(response)
		if len(matches) > 1 {
			jsonStr = matches[1]
		}
	}

	if err := json.Unmarshal([]byte(jsonStr), &validation); err != nil {
		logger.Warn("Failed to parse Ollama validation response",
			zap.Error(err),
			zap.String("raw_response", response[:util.Min(len(response), 500)]))
		return nil, fmt.Errorf("failed to parse validation response: %w", err)
	}

	logger.Info("Ollama validation completed",
		zap.String("entity", entityName),
		zap.Int("approved", len(validation.Approved)),
		zap.Int("rejected", len(validation.Rejected)),
		zap.String("reasoning", validation.Reasoning))

	var approved []stock.VideoResult
	for _, idx := range validation.Approved {
		if idx >= 1 && idx <= len(results) {
			approved = append(approved, results[idx-1])
		}
	}

	return approved, nil
}

// downloadVideo downloads a video using yt-dlp
func (s *ScriptClipsService) downloadVideo(ctx context.Context, url string) (string, error) {
	filename := fmt.Sprintf("clip_%d_%%(id)s.%%(ext)s", time.Now().Unix())
	outputTemplate := filepath.Join(s.downloadDir, filename)

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--newline",
		"--no-warnings",
		"-f", "bestvideo[height<=1080]+bestaudio/best[height<=1080]",
		"-o", outputTemplate,
		url,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("yt-dlp failed: %v: %s", err, string(output))
	}

	files, err := filepath.Glob(filepath.Join(s.downloadDir, "clip_*"))
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("no downloaded file found")
	}

	return files[len(files)-1], nil
}

// uploadToDriveWithTopic uploads a file to a topic-specific Stock folder on Drive
func (s *ScriptClipsService) uploadToDriveWithTopic(ctx context.Context, filePath, entityName, topic string) (string, string, error) {
	var folderID string
	var err error

	if topic != "" {
		stockRootID, err := s.driveClient.GetOrCreateFolder(ctx, "Stock", "root")
		if err != nil {
			return "", "", fmt.Errorf("failed to create Stock folder: %w", err)
		}

		topicFolderID, err := s.driveClient.GetOrCreateFolder(ctx, topic, stockRootID)
		if err != nil {
			return "", "", fmt.Errorf("failed to create topic folder '%s': %w", topic, err)
		}
		folderID = topicFolderID
	} else if s.driveFolderID != "" {
		folderID = s.driveFolderID
	} else {
		stockFolderID, err := s.driveClient.GetOrCreateFolder(ctx, "Stock Clips", "root")
		if err != nil {
			return "", "", fmt.Errorf("failed to create Stock Clips folder: %w", err)
		}
		folderID = stockFolderID
	}

	filename := fmt.Sprintf("clip_%s_%d.mp4", sanitizeFilename(entityName), time.Now().Unix())
	fileID, err := s.driveClient.UploadFile(ctx, filePath, folderID, filename)
	if err != nil {
		return "", "", err
	}

	os.Remove(filePath)
	driveURL := drive.GetDriveLink(fileID)

	return fileID, driveURL, nil
}

// mapLanguageToCode converte nomi completi di lingue in codici ISO
func (s *ScriptClipsService) mapLanguageToCode(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "italian", "ita", "it", "italiano":
		return "it"
	case "english", "eng", "en":
		return "en"
	case "spanish", "esp", "es", "español":
		return "es"
	case "french", "fra", "fr", "français":
		return "fr"
	case "german", "deu", "de", "deutsch":
		return "de"
	case "portuguese", "por", "pt", "português":
		return "pt"
	default:
		return "it"
	}
}
