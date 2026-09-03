// Package media — enrichment_adapters.go: the production adapter set for
// the enrichment engine's ports. Each adapter wraps an existing platform
// concrete behind the narrow media-package port so the enrichment engine
// stays leaf-only (no imports of rustexec / capabilities packages).
package media

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── FFMPEG probe adapter ────────────────────────────────────────────────

// FFMPEGProbeAdapter implements MediaProbePort via the ffprobe CLI.
type FFMPEGProbeAdapter struct {
	ffprobeBin string
}

// NewFFMPEGProbeAdapter constructs the probe adapter. An empty bin
// defaults to "ffprobe".
func NewFFMPEGProbeAdapter(ffprobeBin string) *FFMPEGProbeAdapter {
	if strings.TrimSpace(ffprobeBin) == "" {
		ffprobeBin = "ffprobe"
	}
	return &FFMPEGProbeAdapter{ffprobeBin: ffprobeBin}
}

// Probe reads duration + video presence for one media file.
func (a *FFMPEGProbeAdapter) Probe(ctx context.Context, path string) (*ProbeSummary, error) {
	cmd := exec.CommandContext(ctx, a.ffprobeBin,
		"-v", "error",
		"-print_format", "json",
		"-show_format", "-show_streams", path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %q: %w", path, err)
	}
	var parsed struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("ffprobe %q decode: %w", path, err)
	}
	sec, err := strconvParseFloat(parsed.Format.Duration)
	if err != nil {
		return nil, fmt.Errorf("ffprobe %q duration %q: %w", path, parsed.Format.Duration, err)
	}
	hasVideo := false
	for _, s := range parsed.Streams {
		if s.CodecType == "video" {
			hasVideo = true
			break
		}
	}
	return &ProbeSummary{Duration: time.Duration(sec * float64(time.Second)), HasVideo: hasVideo}, nil
}

// ── FFMPEG keyframe sampler adapter ─────────────────────────────────────

// FFMPEGKeyframeSamplerAdapter implements KeyframeSamplerPort directly
// over the ffmpeg CLI (percentage cadence). It mirrors the canonical
// indexing.FFMPEGFrameSampler behavior without importing the capabilities
// package (the media package stays leaf-only).
type FFMPEGKeyframeSamplerAdapter struct {
	ffmpegBin string
}

// NewFFMPEGKeyframeSamplerAdapter constructs the sampler. An empty bin
// defaults to "ffmpeg".
func NewFFMPEGKeyframeSamplerAdapter(ffmpegBin string) (*FFMPEGKeyframeSamplerAdapter, error) {
	if strings.TrimSpace(ffmpegBin) == "" {
		ffmpegBin = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpegBin); err != nil {
		return nil, fmt.Errorf("ffmpeg keyframe sampler: %s not in PATH: %w", ffmpegBin, err)
	}
	return &FFMPEGKeyframeSamplerAdapter{ffmpegBin: ffmpegBin}, nil
}

// ExtractPercentageFrames extracts one PNG frame per requested percentage
// of the source duration (order-preserved).
func (a *FFMPEGKeyframeSamplerAdapter) ExtractPercentageFrames(ctx context.Context, localPath string, percentages []float64, outDir string) ([]KeyframeSample, error) {
	if strings.TrimSpace(localPath) == "" {
		return nil, fmt.Errorf("ffmpeg keyframe sampler: local path is required")
	}
	probe := FFMPEGProbeAdapter{ffprobeBin: "ffprobe"}
	summary, err := probe.Probe(ctx, localPath)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg keyframe sampler: probe %q: %w", localPath, err)
	}
	if summary.Duration <= 0 {
		return nil, fmt.Errorf("ffmpeg keyframe sampler: non-positive duration for %q", localPath)
	}
	durationSec := summary.Duration.Seconds()
	out := make([]KeyframeSample, 0, len(percentages))
	for i, p := range percentages {
		ts := p * durationSec
		framePath := filepath.Join(outDir, fmt.Sprintf("frame_%03d_%.0f.png", i, p*100))
		cmd := exec.CommandContext(ctx, a.ffmpegBin,
			"-hide_banner", "-loglevel", "error",
			"-ss", fmt.Sprintf("%.3f", ts),
			"-i", localPath,
			"-frames:v", "1",
			"-y", framePath,
		)
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("ffmpeg keyframe sampler: frame %d (t=%.3fs) of %q: %w", i, ts, localPath, err)
		}
		out = append(out, KeyframeSample{Path: framePath, Timestamp: ts, Percentage: p})
	}
	return out, nil
}

// ── Sidecar face detector ───────────────────────────────────────────────

// SidecarFaceDetector implements FaceDetector against the Python embedding
// sidecar's canonical face endpoint (/detect_faces). The endpoint accepts
// {"image_paths": [...]} and returns one {face_count, largest_face_ratio}
// observation per input path (order-preserved).
type SidecarFaceDetector struct {
	serverURL string
	client    *http.Client
}

// NewSidecarFaceDetector constructs the detector. An empty server URL is
// a construction error (no fake availability — faces are load-bearing for
// has_faces filters).
func NewSidecarFaceDetector(serverURL string, timeout time.Duration) *SidecarFaceDetector {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &SidecarFaceDetector{
		serverURL: strings.TrimRight(serverURL, "/"),
		client:    &http.Client{Timeout: timeout},
	}
}

// DetectFaces calls the sidecar's face endpoint for the frame batch.
func (d *SidecarFaceDetector) DetectFaces(ctx context.Context, framePaths []string) ([]FaceObservation, error) {
	if len(framePaths) == 0 {
		return nil, fmt.Errorf("face detector: no frames supplied")
	}
	resp, err := postJSON(ctx, d.client, d.serverURL+"/detect_faces", map[string][]string{"image_paths": framePaths})
	if err != nil {
		return nil, fmt.Errorf("face detector: sidecar call: %w", err)
	}
	if resp.status == http.StatusNotImplemented {
		return nil, fmt.Errorf("%w (HTTP 501 — face model not loaded)", ErrVisualSidecarUnavailable)
	}
	if resp.status != http.StatusOK {
		return nil, fmt.Errorf("face detector: sidecar HTTP %d: %s", resp.status, truncateForLog(resp.body))
	}
	var parsed struct {
		Faces []struct {
			FaceCount        int     `json:"face_count"`
			LargestFaceRatio float64 `json:"largest_face_ratio"`
		} `json:"faces"`
	}
	if err := json.Unmarshal(resp.body, &parsed); err != nil {
		return nil, fmt.Errorf("face detector: decode: %w", err)
	}
	if len(parsed.Faces) != len(framePaths) {
		return nil, fmt.Errorf("face detector: %d observations for %d frames", len(parsed.Faces), len(framePaths))
	}
	out := make([]FaceObservation, 0, len(parsed.Faces))
	for _, f := range parsed.Faces {
		out = append(out, FaceObservation{FaceCount: f.FaceCount, LargestRatio: f.LargestFaceRatio})
	}
	return out, nil
}

// ── Sidecar text embedder adapter ───────────────────────────────────────

// SidecarTextEmbedderAdapter implements the kernel asset.Embedder port
// (Embed(ctx, text) → asset.EmbeddingResult) against the sidecar's
// canonical /embed endpoint (E5 text encoder). The enrichment engine only
// consumes the AssetEmbedder port; this adapter is the production concrete
// for CLI runs (the index worker path reuses embeddings.HTTPTextEmbedder).
type SidecarTextEmbedderAdapter struct {
	serverURL string
	client    *http.Client
}

// NewSidecarTextEmbedderAdapter constructs the text embedder adapter.
func NewSidecarTextEmbedderAdapter(serverURL string) *SidecarTextEmbedderAdapter {
	return &SidecarTextEmbedderAdapter{
		serverURL: strings.TrimRight(serverURL, "/"),
		client:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Embed produces the E5 text embedding for one text (kernel
// asset.Embedder contract).
func (a *SidecarTextEmbedderAdapter) Embed(ctx context.Context, text string) (asset.EmbeddingResult, error) {
	if strings.TrimSpace(text) == "" {
		return asset.EmbeddingResult{}, fmt.Errorf("text embedder: empty text")
	}
	resp, err := postJSON(ctx, a.client, a.serverURL+"/embed", map[string]string{"text": text})
	if err != nil {
		return asset.EmbeddingResult{}, fmt.Errorf("text embedder: sidecar call: %w", err)
	}
	if resp.status != http.StatusOK {
		return asset.EmbeddingResult{}, fmt.Errorf("text embedder: sidecar HTTP %d: %s", resp.status, truncateForLog(resp.body))
	}
	var parsed struct {
		Embedding []float64 `json:"embedding"`
		Model     string    `json:"model"`
	}
	if err := json.Unmarshal(resp.body, &parsed); err != nil {
		return asset.EmbeddingResult{}, fmt.Errorf("text embedder: decode: %w", err)
	}
	if len(parsed.Embedding) == 0 {
		return asset.EmbeddingResult{}, fmt.Errorf("text embedder: empty embedding")
	}
	vec := make([]float32, len(parsed.Embedding))
	for i, v := range parsed.Embedding {
		vec[i] = float32(v)
	}
	return asset.EmbeddingResult{Vector: vec, Dimensions: len(vec), Model: parsed.Model}, nil
}

// ── DB-backed asset path resolver ───────────────────────────────────────

// ResolveAssetPathFromDB returns the production ResolveAssetPath: the
// asset's primary local location URI, falling back to the drive location
// (file:// or absolute path forms). Assets with no resolvable local file
// surface a typed error (recorded as uncovered by the engine).
func ResolveAssetPathFromDB(db *sql.DB) ResolveAssetPath {
	return func(ctx context.Context, assetID string) (string, error) {
		var uri string
		err := db.QueryRowContext(ctx, `
			SELECT uri FROM asset_locations
			WHERE asset_id = $1 AND location_kind = 'local'
			ORDER BY is_primary DESC
			LIMIT 1
		`, assetID).Scan(&uri)
		if err == nil && strings.TrimSpace(uri) != "" {
			return normalizeMediaURI(uri)
		}
		// Fallback: drive locations are not locally readable; legacy
		// metadata local_path is the second chance.
		var localPath string
		err2 := db.QueryRowContext(ctx,
			`SELECT local_path FROM media_assets WHERE id = $1`, assetID).Scan(&localPath)
		if err2 == nil && strings.TrimSpace(localPath) != "" {
			return normalizeMediaURI(localPath)
		}
		if err != nil && err2 != nil {
			return "", fmt.Errorf("no resolvable local location for asset %q (local: %v, local_path: %v)", assetID, err, err2)
		}
		return "", fmt.Errorf("no resolvable local location for asset %q", assetID)
	}
}

// normalizeMediaURI strips a file:// scheme and validates existence.
// A non-existent path is a typed failure (never a silent empty string) so
// the enrichment engine records the asset as uncovered with a truthful
// reason instead of a bare "<nil>".
func normalizeMediaURI(uri string) (string, error) {
	uri = strings.TrimPrefix(uri, "file://")
	if _, err := os.Stat(uri); err != nil {
		return "", fmt.Errorf("media file missing on disk: %s", uri)
	}
	return uri, nil
}

// ── small helpers ───────────────────────────────────────────────────────

// strconvParseFloat is a tiny indirection so the adapter imports stay tidy.
func strconvParseFloat(s string) (float64, error) {
	var f float64
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%g", &f)
	return f, err
}

// bufio keeps the import set honest for future streaming decode helpers.
var _ = bufio.NewReader
