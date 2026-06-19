package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	concurrent "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
	urlutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

	"go.uber.org/zap"
)

func (m *ChannelMonitor) matchSemantically(ctx context.Context, videoURL string, semanticKeywords []string, minScore int, cfg *MonitorConfig) (int, string, error) {
	model := m.cfg.External.OllamaModel
	if model == "" {
		model = "gemma4:e2b"
	}

	threshold := semanticScoreThreshold(minScore)

	// Extract video ID from URL for cache lookup
	videoID := ""
	if vID, _ := urlutil.ExtractVideoID(videoURL); vID != "" {
		videoID = vID
	} else {
		// Fallback: use simple hash from URL
		h := 0
		for _, c := range videoURL {
			h = h*31 + int(c)
		}
		videoID = fmt.Sprintf("vid_%x", h)[:16]
	}

	// ── Step 1: Try loading transcript from cache ────────────────────────
	transcript, err := m.loadTranscriptCache(ctx, videoID)
	if err != nil {
		m.log.Debug("transcript cache miss, will download",
			zap.String("video_id", videoID),
			zap.Error(err))
	}

	// ── Step 2: If not cached, download subtitles via yt-dlp ─────────────
	if transcript == "" {
		tempDir, err := os.MkdirTemp("", "semantic_match_*")
		if err != nil {
			return 0, "", fmt.Errorf("create temp dir: %w", err)
		}
		defer os.RemoveAll(tempDir)

		subCmd := exec.CommandContext(ctx, cfg.YtdlpPath,
			"--write-auto-subs",
			"--write-subs",
			"--skip-download",
			"--sub-langs", "en",
			"--sub-format", "vtt",
			"-o", filepath.Join(tempDir, "subs"),
			videoURL,
		)
		if out, err := subCmd.CombinedOutput(); err != nil {
			return 0, "", fmt.Errorf("subtitle download failed: %w, output: %s", err, string(out))
		}

		// Find the downloaded VTT file
		var vttPath string
		entries, err := os.ReadDir(tempDir)
		if err != nil {
			return 0, "", fmt.Errorf("read temp dir: %w", err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "subs.") && strings.HasSuffix(entry.Name(), ".vtt") {
				vttPath = filepath.Join(tempDir, entry.Name())
				break
			}
		}

		if vttPath == "" {
			return 0, "", fmt.Errorf("no subtitle file found for video: %s", videoURL)
		}

		// Extract text from VTT
		vttData, err := os.ReadFile(vttPath)
		if err != nil {
			return 0, "", fmt.Errorf("read vtt file: %w", err)
		}

		content := string(vttData)
		content = regexRemoveVTTHeader(content)
		var transcriptLines []string
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.Contains(line, "-->") || strings.Contains(line, "NOTE ") {
				continue
			}
			cleaned := regexRemoveXMLTags(line)
			if cleaned != "" {
				transcriptLines = append(transcriptLines, cleaned)
			}
		}

		transcript = strings.Join(transcriptLines, " ")

		// Save to cache for next time (async, non-blocking)
		if transcript != "" && m.db != nil {
			concurrent.SafeGoFunc("monitor-transcript-save", struct {
				ID  string
				Txt string
			}{ID: videoID, Txt: transcript}, func(arg struct {
				ID  string
				Txt string
			}) {
				saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if err := m.saveTranscriptCache(saveCtx, arg.ID, arg.Txt); err != nil {
					m.log.Warn("failed to cache transcript",
						zap.String("video_id", arg.ID),
						zap.Error(err))
				}
			})
		}
	}

	// Limit transcript length to avoid context overflow (first 8000 chars)
	if len(transcript) > 8000 {
		transcript = transcript[:8000]
	}

	if len(strings.Fields(transcript)) < 10 {
		return 0, "", fmt.Errorf("transcript too short (%d words), skipping", len(strings.Fields(transcript)))
	}

	// ── Step 3: Ask Ollama for relevance score ───────────────────────────
	keywordsStr := strings.Join(semanticKeywords, ", ")
	prompt := fmt.Sprintf(`You are a content classifier. Analyze this video transcript and determine if the video discusses any of these topics: %s.

Transcript:
%s

Respond with a JSON object ONLY, no other text:
{
  "score": <0-100 integer>,
  "matched_keyword": "<the single best-matching keyword or empty string if none>",
  "reason": "<one-sentence justification>"
}

Rules:
- Score 0 = not relevant at all
- Score 100 = entirely about the topic
- Score >= %d = relevant, should be processed
- Score < %d = not relevant, skip
- Consider the entire transcript, not just the first few lines.`, keywordsStr, transcript, threshold, threshold)

	responseStr, err := m.ollamaClient.SimpleGenerate(ctx, model, prompt, 60*time.Second, map[string]any{"format": "json"})
	if err != nil {
		return 0, "", fmt.Errorf("ollama call: %w", err)
	}

	// Parse the JSON response
	var result struct {
		Score          int    `json:"score"`
		MatchedKeyword string `json:"matched_keyword"`
		Reason         string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(responseStr), &result); err != nil {
		// Sometimes Ollama wraps in markdown, try to extract JSON
		if jsonMatch := jsonRegexFind([]byte(responseStr)); jsonMatch != nil {
			if err2 := json.Unmarshal(jsonMatch, &result); err2 != nil {
				return 0, "", fmt.Errorf("parse ollama response (fallback also failed): %w, raw: %s", err, responseStr)
			}
		} else {
			return 0, "", fmt.Errorf("parse ollama response: %w, raw: %s", err, responseStr)
		}
	}

	if result.Score < 0 || result.Score > 100 {
		result.Score = 0
	}

	m.log.Debug("semantic match result",
		zap.Int("score", result.Score),
		zap.String("matched_keyword", result.MatchedKeyword),
		zap.String("reason", result.Reason))

	return result.Score, result.MatchedKeyword, nil
}

// regexRemoveVTTHeader removes the WEBVTT header and metadata from VTT content.
func (m *ChannelMonitor) loadTranscriptCache(ctx context.Context, videoID string) (string, error) {
	if m.db == nil {
		return "", fmt.Errorf("no db available")
	}

	var transcript string
	var cachedAt string
	err := m.db.QueryRowContext(ctx, `
		SELECT transcript_text, cached_at FROM transcript_cache WHERE video_id = ?
	`, videoID).Scan(&transcript, &cachedAt)
	if err != nil {
		return "", err
	}

	// Check TTL: 7 days
	if cachedAt != "" {
		cachedTime, parseErr := time.Parse("2006-01-02 15:04:05", cachedAt)
		if parseErr == nil && time.Since(cachedTime) > 7*24*time.Hour {
			// Stale — delete asynchronously and return miss
			concurrent.SafeGoFunc("monitor-transcript-cleanup", videoID, func(id string) {
				delCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if _, err := m.db.ExecContext(delCtx, "DELETE FROM transcript_cache WHERE video_id = ?", id); err != nil {
					m.log.Warn("failed to delete stale transcript cache",
						zap.String("video_id", id), zap.Error(err))
				}
			})
			return "", fmt.Errorf("cache stale")
		}
	}

	return transcript, nil
}

// saveTranscriptCache stores a transcript in the cache.
func (m *ChannelMonitor) saveTranscriptCache(ctx context.Context, videoID, transcript string) error {
	if m.db == nil {
		return nil
	}
	_, err := m.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO transcript_cache (video_id, transcript_text, cached_at)
		VALUES (?, ?, datetime('now'))
	`, videoID, transcript)
	return err
}
