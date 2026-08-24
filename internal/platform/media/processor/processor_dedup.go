package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
)

// embeddingHTTPTimeout bounds the synchronous pHash HTTP call so a hung
// embedding server cannot block the media processor indefinitely. The
// caller's context cancellation also propagates through the request.
const embeddingHTTPTimeout = 30 * time.Second

// checkPHashDeduplication checks if a similar clip already exists using perceptual hashing.
func (p *Processor) checkPHashDeduplication(ctx context.Context, clipID, videoPath string) (string, error) {
	phash := p.phashForVideo(ctx, videoPath)
	if phash == "" {
		return "", nil
	}

	// Query registry for similar pHash. This lookup is intentionally NOT
	// cached: registry state changes as new assets land, so it must stay
	// fresh even when the (content-derived) pHash itself is a cache hit.
	existingID, err := p.registry.FindByPHash(ctx, phash)
	if err != nil {
		return "", nil
	}
	return existingID, nil
}

// phashForVideo returns the perceptual hash for a processed video, served
// from the artifact cache when available. The pHash is a deterministic
// function of the video bytes (frame @1s → embedding server), so it is
// cached under the "phash" operation keyed by the processed video SHA. A
// warm reprocess run reuses the cached pHash and therefore skips BOTH the
// ffmpeg frame extraction (the last ffmpeg dispatch in the reprocess path)
// and the embedding HTTP call — taking a warm run from delta +1 to delta 0.
func (p *Processor) phashForVideo(ctx context.Context, videoPath string) string {
	videoSHA := hashFileSHA256(videoPath)
	key := capcache.Key{}
	if videoSHA != "" {
		key = capcacheKey(videoSHA, "phash", map[string]any{"frame_seconds": 1.0}, "media-phash/v1")
	}

	phashPath := videoPath + ".phash.txt"
	cached, leaseID := p.materializeCachedFile(ctx, key, phashPath)
	if cached {
		b, rerr := os.ReadFile(phashPath)
		_ = os.Remove(phashPath)
		if rerr == nil {
			if phash := strings.TrimSpace(string(b)); phash != "" {
				p.log.Debug("phash artifact cache hit", zap.String("source_sha256", videoSHA))
				return phash
			}
		}
	}

	phash := p.extractPHash(ctx, videoPath)
	if phash == "" {
		// pHash unavailable (frame extraction or embedding server
		// failure). Release any acquired claim so an abandoned build
		// doesn't block concurrent callers; never cache a failed pHash.
		if videoSHA != "" {
			p.releaseCachedClaim(ctx, key, leaseID, "phash unavailable")
		}
		return ""
	}

	if videoSHA != "" {
		if err := os.WriteFile(phashPath, []byte(phash+"\n"), 0o644); err == nil {
			p.storeCachedFile(ctx, key, leaseID, phashPath, "text/plain")
		}
		_ = os.Remove(phashPath)
	}
	return phash
}

// extractPHash extracts the perceptual hash for a processed video by
// extracting the frame at 1s and asking the embedding server for its pHash.
// Returns "" on any failure (frame extraction, embedding HTTP error, empty
// response) — the caller treats "" as "dedup unavailable" and continues.
func (p *Processor) extractPHash(ctx context.Context, videoPath string) string {
	// 1. Extract a frame at 1s
	thumbPath := videoPath + ".thumb.png"
	err := p.ffmpeg.ExtractFrame(ctx, videoPath, thumbPath, 1.0)
	if err != nil {
		p.log.Warn("failed to extract frame for pHash", zap.Error(err))
		return ""
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewBuffer(reqBody))
	if err != nil {
		p.log.Warn("failed to build embedding server request for pHash", zap.Error(err))
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: embeddingHTTPTimeout}).Do(req)
	if err != nil {
		p.log.Warn("failed to connect to embedding server for pHash", zap.Error(err))
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var phashResp PhashResponse
	if err := json.NewDecoder(resp.Body).Decode(&phashResp); err != nil {
		return ""
	}

	return phashResp.Phash
}
