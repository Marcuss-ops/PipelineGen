package vlmtagger

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/service/clipcatalog"
)

type Service struct {
	gen    *ollama.Generator
	repo   *clipcatalog.Repository
	log    *zap.Logger
	tmpDir string
}

func NewService(gen *ollama.Generator, repo *clipcatalog.Repository, log *zap.Logger) *Service {
	return &Service{
		gen:    gen,
		repo:   repo,
		log:    log,
		tmpDir: "/tmp/velox_vlm",
	}
}

type VLMTagResult struct {
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Category    string   `json:"category"`
	SceneType   string   `json:"scene_type"`
	UsableFor   []string `json:"usable_for"`
	AvoidFor    []string `json:"avoid_for"`
}

func (s *Service) TagClip(ctx context.Context, clipID string, localPath string) error {
	if localPath == "" {
		return fmt.Errorf("local path is empty")
	}

	// 1. Extract frame
	framePath := filepath.Join(s.tmpDir, clipID+"_frame.jpg")
	os.MkdirAll(s.tmpDir, 0755)

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "00:00:02", "-i", localPath, "-frames:v", "1", framePath)
	if err := cmd.Run(); err != nil {
		// Try at 0 if 2s fails (clip might be shorter)
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y", "-ss", "00:00:00", "-i", localPath, "-frames:v", "1", framePath)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to extract frame: %w", err)
		}
	}
	defer os.Remove(framePath)

	// 2. Encode to base64
	imageData, err := ioutil.ReadFile(framePath)
	if err != nil {
		return fmt.Errorf("failed to read frame: %w", err)
	}
	base64Image := base64.StdEncoding.EncodeToString(imageData)

	// 3. Send to VLM
	prompt := `Describe this video frame for stock footage retrieval. 
Focus on concrete visual details: subject, action, lighting, camera angle, and setting.
Return ONLY a valid JSON object with the following schema:
{
  "description": "Short evocative description",
  "tags": ["keyword1", "keyword2"],
  "category": "e.g. sports, nature, tech",
  "scene_type": "e.g. training, interview, b-roll",
  "usable_for": ["context1", "context2"],
  "avoid_for": ["context1"]
}`

	messages := []types.Message{
		{
			Role:    "user",
			Content: prompt,
			Images:  []string{base64Image},
		},
	}

	// Use a VLM model if specified, otherwise default
	resp, err := s.gen.GetClient().Chat(ctx, messages, nil)
	if err != nil {
		return fmt.Errorf("VLM chat failed: %w", err)
	}

	// 4. Parse response
	var result VLMTagResult
	jsonStart := strings.Index(resp, "{")
	jsonEnd := strings.LastIndex(resp, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		if err := json.Unmarshal([]byte(resp[jsonStart:jsonEnd+1]), &result); err != nil {
			return fmt.Errorf("failed to parse VLM JSON: %w", err)
		}
	} else {
		return fmt.Errorf("no JSON found in VLM response: %s", resp)
	}

	// 5. Update Repository
	meta := clipcatalog.ClipMetadata{
		SearchText:   result.Description,
		Tags:         result.Tags,
		Category:     result.Category,
		SceneType:    result.SceneType,
		UsableFor:    result.UsableFor,
		AvoidFor:     result.AvoidFor,
		QualityScore: 0.8, // Boost quality since it's now well-tagged
	}

	if err := s.repo.UpdateMetadata(ctx, clipID, meta); err != nil {
		return fmt.Errorf("failed to update repository: %w", err)
	}

	s.log.Info("clip tagged successfully via VLM",
		zap.String("clip_id", clipID),
		zap.String("category", result.Category),
	)

	return nil
}
