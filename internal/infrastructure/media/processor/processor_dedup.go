package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"go.uber.org/zap"
)

// checkPHashDeduplication checks if a similar clip already exists using perceptual hashing.
func (p *Processor) checkPHashDeduplication(ctx context.Context, clipID, videoPath string) (string, error) {
	// 1. Extract a frame at 1s
	thumbPath := videoPath + ".thumb.png"
	err := p.ffmpeg.ExtractFrame(ctx, videoPath, thumbPath, 1.0)
	if err != nil {
		p.log.Warn("failed to extract frame for pHash", zap.Error(err))
		return "", nil // Continue even if pHash fails
	}
	defer os.Remove(thumbPath)

	// 2. Call embedding server to get pHash
	serverURL := strings.TrimSuffix(p.embeddingURL, "/") + "/phash"

	type PhashRequest struct {
		ImagePath string `json:"image_path"`
	}
	type PhashResponse struct {
		Phash string `json:"phash"`
	}

	reqBody, _ := json.Marshal(PhashRequest{ImagePath: thumbPath})
	resp, err := http.Post(serverURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		p.log.Warn("failed to connect to embedding server for pHash", zap.Error(err))
		return "", nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil
	}

	var phashResp PhashResponse
	if err := json.NewDecoder(resp.Body).Decode(&phashResp); err != nil {
		return "", nil
	}

	phash := phashResp.Phash
	if phash == "" {
		return "", nil
	}

	// 3. Query registry for similar pHash
	existingID, err := p.registry.FindByPHash(ctx, phash)
	if err != nil {
		return "", nil
	}
	return existingID, nil
}
