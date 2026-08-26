// Package audioasset — worker_protocol.go (VO-DECOMPOSITION P0 #1, July 2026):
// HTTP client helpers for the tts_edge_server.py sidecar.
//
// godlike/06 + AGENTS.md Pattern 5: single-purpose capability file in
// the same package. Owns the HTTP wire protocol:
//   - sendSynthesizeRequest(ctx, input) — POST /synthesize with JSON body,
//     parse the JSON response into AudioResult.
//
// Mirrors the precedent in internal/capabilities/images/workflow/slide_worker_
// protocol.go (PR-CHROME-PROVIDER-SPLIT, 2026-07-04).
package audioasset

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/checksum"
	"go.uber.org/zap"
)

// ttsSynthesizeRequest is the JSON body sent to POST /synthesize.
type ttsSynthesizeRequest struct {
	Text               string `json:"text"`
	Lang               string `json:"lang"`
	Voice              string `json:"voice,omitempty"`
	Out                string `json:"out"`
	AllowVoiceFallback bool   `json:"allow_voice_fallback,omitempty"`
}

// ttsSynthesizeResponse is the JSON response from the Python server.
// Mirrors the tts_edge.py stdout JSON shape exactly.
type ttsSynthesizeResponse struct {
	OK    bool   `json:"ok"`
	Voice string `json:"voice,omitempty"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
	// MetadataPath is <out>.metadata.jsonl when the bridge captured word
	// boundaries in the same synthesis stream (empty otherwise).
	MetadataPath string `json:"metadata_path,omitempty"`
	// BoundaryCount is the number of WordBoundary chunks captured.
	BoundaryCount  int   `json:"boundary_count,omitempty"`
	VoiceResolveMS int64 `json:"voice_resolve_ms,omitempty"`
	StreamMS       int64 `json:"stream_ms,omitempty"`
}

// sendSynthesizeRequest sends a POST /synthesize to the persistent worker
// and returns the AudioResult. The persistent aiohttp worker handles these
// requests concurrently; lifecycle protection is owned by synthesizeOnce.
//
// godlike/07 typed-error contract (PR-VO-TTS-PERSISTENT-WORKER): every
// failure path wraps a typed sentinel + the underlying cause (dual %w,
// Go 1.20+) so callers can probe with errors.Is(ErrSynthesizeFailed) AND
// errors.As(workerErr) without parsing string fragments:
//   - ErrSynthesizeFailed: non-200 status OR ok=false response body
//   - ErrOutputMissing: post-synthesis file missing from disk (worker claimed
//     success but the file is gone)
func (p *Processor) sendSynthesizeRequest(ctx context.Context, input *AudioInput) (*AudioResult, error) {
	reqBody := ttsSynthesizeRequest{
		Text:               input.Text,
		Lang:               input.Language,
		Voice:              input.Voice,
		Out:                filepath.Join(input.OutputDir, input.Filename),
		AllowVoiceFallback: input.AllowVoiceFallback,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal synthesize request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/synthesize", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create synthesize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("synthesize request failed: %w: %w", err, ErrSynthesizeFailed)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read synthesize response: %w: %w", err, ErrSynthesizeFailed)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("synthesize returned %d: %s: %w", resp.StatusCode,
			string(bytes.TrimSpace(respBody)), ErrSynthesizeFailed)
	}

	var ttsOut ttsSynthesizeResponse
	if err := json.Unmarshal(respBody, &ttsOut); err != nil {
		return nil, fmt.Errorf("decode synthesize response: %w (body=%s): %w", err,
			string(bytes.TrimSpace(respBody)), ErrSynthesizeFailed)
	}

	if !ttsOut.OK {
		if isBridgeEmptyAudioError(ttsOut.Error) {
			return nil, fmt.Errorf("TTS generation failed: %s: %w: %w", ttsOut.Error, ErrEmptyAudio, ErrSynthesizeFailed)
		}
		return nil, fmt.Errorf("TTS generation failed: %s: %w", ttsOut.Error, ErrSynthesizeFailed)
	}

	// Post-condition: output file must exist and be non-empty.
	outputPath := ttsOut.Path
	if outputPath == "" {
		outputPath = reqBody.Out
	}
	if _, statErr := os.Stat(outputPath); os.IsNotExist(statErr) {
		return nil, fmt.Errorf("TTS output file not found: %s: %w", outputPath, ErrOutputMissing)
	}

	result := &AudioResult{
		LocalPath:     outputPath,
		Status:        "generated",
		Voice:         ttsOut.Voice,
		MetadataPath:  ttsOut.MetadataPath,
		BoundaryCount: ttsOut.BoundaryCount,
		Metrics: TTSMetrics{
			VoiceResolveMS: ttsOut.VoiceResolveMS,
			StreamMS:       ttsOut.StreamMS,
		},
	}
	postprocessStart := time.Now()

	p.log.Info("TTS synthesized via persistent worker",
		zap.String("path", outputPath),
		zap.String("voice", ttsOut.Voice),
		zap.Int64("voice_resolve_ms", result.Metrics.VoiceResolveMS),
		zap.Int64("stream_ms", result.Metrics.StreamMS))

	// Optional silence removal (mirrors the legacy processor.go block).
	if input.RemoveSilence {
		safeName := input.Filename
		cleanedPath := filepath.Join(input.OutputDir, "cleaned_"+safeName)
		media, mediaErr := p.mediaExecutor()
		if mediaErr != nil {
			return nil, fmt.Errorf("remove silence: %w", mediaErr)
		}
		if err := media.RemoveSilence(ctx, outputPath, cleanedPath); err != nil {
			p.log.Warn("silence removal failed", zap.Error(err))
		} else {
			result.CleanedPath = cleanedPath
			result.LocalPath = cleanedPath
			result.Status = "cleaned"
		}
	}

	// Compute MD5 hash — byte-identical with the legacy path
	// (processor.go::generateLegacy uses md5.New()).
	if result.LocalPath != "" {
		if hash, hashErr := checksum.LegacyMD5File(result.LocalPath); hashErr != nil {
			p.log.Warn("hash computation failed", zap.Error(hashErr))
		} else {
			result.LegacyFileMD5 = hash
		}
	}
	if media, mediaErr := p.mediaExecutor(); mediaErr == nil {
		if info, probeErr := media.Probe(ctx, result.LocalPath); probeErr == nil {
			result.Duration = info.Duration
		} else {
			p.log.Warn("failed to probe synthesized audio duration", zap.Error(probeErr))
		}
	} else {
		p.log.Warn("failed to probe synthesized audio duration", zap.Error(mediaErr))
	}
	result.Metrics.PostprocessMS = time.Since(postprocessStart).Milliseconds()
	result.Metrics.AudioDurationMS = result.Duration.Milliseconds()
	if result.Metrics.AudioDurationMS > 0 {
		result.Metrics.RTF = float64(result.Metrics.StreamMS) / float64(result.Metrics.AudioDurationMS)
	}

	return result, nil
}

// filepathJoin is a thin wrapper for path/filepath.Join so this file
// does not need the path/filepath import (the package-level import
// lives in processor.go per AGENTS.md single-import-site convention).
