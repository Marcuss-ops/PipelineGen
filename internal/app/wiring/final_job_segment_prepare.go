package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	finalJobSegmentProfile = "VELOX_ASSEMBLY_READY_V1"
	finalJobSegmentFPS     = int64(24)
	finalJobSegmentGOP     = int64(48)
	finalJobSegmentMaxSize = int64(2 << 30)
)

// finalJobSegmentPreparer creates only timeline fragments, never the final
// video. Inputs that already carry a complete, trusted assembly certificate
// are reused; incompatible windows are cut and encoded once locally, then
// uploaded to Objectstore. Worker 51 receives only HTTPS, copy-ready pieces.
type finalJobSegmentPreparer struct {
	ffmpeg string
	policy mediaexec.EncoderPolicy
	client *http.Client
}

func newFinalJobSegmentPreparer(ffmpeg string, policy mediaexec.EncoderPolicy) *finalJobSegmentPreparer {
	return &finalJobSegmentPreparer{
		ffmpeg: strings.TrimSpace(ffmpeg),
		policy: policy,
		client: &http.Client{Timeout: 20 * time.Minute},
	}
}

func (p *finalJobSegmentPreparer) PrepareFinalJobVideoSegment(ctx context.Context, source map[string]any, request scriptgen.FinalJobVideoSegmentRequest) (map[string]any, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("local fragment encoder is unavailable")
	}
	if request.FrameCount <= 0 || request.SourceInUS < 0 || request.SourceDurationUS <= 0 || request.TimelineStartFrame < 0 || request.TimelineRevision <= 0 || len(request.TimelineSHA256) != 64 {
		return nil, fmt.Errorf("segment has invalid source or timeline bounds")
	}
	inputID := strings.TrimSpace(stringValue(source["asset_id"]))
	inputSHA := strings.ToLower(strings.TrimSpace(stringValue(source["sha256"])))
	inputSize := int64Value(source["size_bytes"])
	sourceURL := strings.TrimSpace(stringValue(source["source_uri"]))
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(stringValue(source["url"]))
	}
	if inputID == "" || len(inputSHA) != 64 || inputSize <= 0 || !strings.HasPrefix(strings.ToLower(sourceURL), "https://") {
		return nil, fmt.Errorf("source asset is missing an immutable HTTPS identity")
	}
	assetID := finalJobPreparedAssetID(request, inputSHA)
	durationUS := framesDurationUS(request.FrameCount)
	if finalJobSourceIsExactPreparedFragment(source, request, durationUS) {
		// The existing source already has a signed HTTPS locator and a verified
		// content hash. Change only the per-segment identity; the signed URL is
		// bound to the immutable hash-addressed object, not to asset_id.
		ref := make(map[string]any, len(source)+1)
		for key, value := range source {
			if key == "drive_file_id" || key == "url" || key == "local_path" {
				continue
			}
			ref[key] = value
		}
		ref["asset_id"] = assetID
		prepared := finalJobPreparedRef(ref, inputSHA, inputSize, durationUS, request)
		prepared["role"] = stringValue(source["role"])
		return prepared, nil
	}

	tmpDir, err := os.MkdirTemp("", "velox-final-fragment-")
	if err != nil {
		return nil, fmt.Errorf("create fragment staging directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	sourcePath := filepath.Join(tmpDir, "source")
	if err := p.downloadAndVerify(ctx, sourceURL, inputSHA, inputSize, sourcePath); err != nil {
		return nil, err
	}
	outPath := filepath.Join(tmpDir, "fragment.mp4")
	if err := p.encodeFragment(ctx, sourcePath, outPath, request); err != nil {
		return nil, err
	}
	if err := verifyFinalJobFragment(ctx, outPath, request.FrameCount); err != nil {
		return nil, err
	}
	sha, size, err := digest.SHA256File(outPath)
	if err != nil {
		return nil, fmt.Errorf("hash prepared fragment: %w", err)
	}
	ref, err := scriptgen.PublishFinalJobLocalObject(ctx, assetID, outPath, sha, size)
	if err != nil {
		return nil, fmt.Errorf("publish prepared fragment to HTTPS Objectstore: %w", err)
	}
	prepared := finalJobPreparedRef(ref, sha, size, durationUS, request)
	prepared["role"] = stringValue(source["role"])
	return prepared, nil
}

func (p *finalJobSegmentPreparer) downloadAndVerify(ctx context.Context, sourceURL, expectedSHA string, expectedSize int64, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("build HTTPS source request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("download source fragment over HTTPS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTPS source returned status %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != expectedSize {
		return fmt.Errorf("HTTPS source length %d does not match declared size %d", resp.ContentLength, expectedSize)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged source fragment: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, finalJobSegmentMaxSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("stage source fragment: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged source fragment: %w", closeErr)
	}
	if written != expectedSize || written > finalJobSegmentMaxSize {
		return fmt.Errorf("staged source size %d does not match declared size %d or exceeds limit", written, expectedSize)
	}
	sha, size, err := digest.SHA256File(target)
	if err != nil {
		return fmt.Errorf("verify staged source identity: %w", err)
	}
	if !strings.EqualFold(sha, expectedSHA) || size != expectedSize {
		return fmt.Errorf("staged source bytes do not match the declared SHA-256 and size")
	}
	return nil
}

func (p *finalJobSegmentPreparer) encodeFragment(ctx context.Context, source, output string, request scriptgen.FinalJobVideoSegmentRequest) error {
	ffmpeg := p.ffmpeg
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return fmt.Errorf("ffmpeg is required to prepare local final-job fragments: %w", err)
		}
	}
	start := float64(request.SourceInUS) / 1_000_000
	padDuration := float64(request.FrameCount) / float64(finalJobSegmentFPS)
	codec := strings.TrimSpace(p.policy.Codec)
	preset := strings.TrimSpace(p.policy.Preset)
	if !strings.Contains(strings.ToLower(codec), "264") || preset == "" || p.policy.CRF <= 0 {
		return fmt.Errorf("final-job fragment requires a resolved H.264 encoder policy")
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-ss", strconv.FormatFloat(start, 'f', 6, 64), "-i", source,
	}
	videoFilter := "fps=24/1,scale=1920:1080:force_original_aspect_ratio=decrease:flags=lanczos,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,format=yuv420p,tpad=stop_mode=clone:stop_duration=" + strconv.FormatFloat(padDuration, 'f', 6, 64)
	args = append(args, "-map", "0:v:0", "-an", "-vf", videoFilter)
	args = append(args,
		// Bound output by the plan's exact frame count. Do not use -t here:
		// timestamp rounding at a short source boundary can cut the last planned
		// frame even with tpad enabled. tpad supplies enough cloned tail for the
		// encoder to reach the frame cap; the cap prevents any longer freeze.
		"-frames:v", strconv.FormatInt(request.FrameCount, 10), "-vsync", "cfr",
		"-c:v", codec, "-preset", preset,
	)
	if strings.Contains(strings.ToLower(codec), "nvenc") {
		args = append(args, "-rc", "vbr", "-cq", strconv.Itoa(p.policy.CRF), "-b:v", "0")
	} else {
		args = append(args, "-crf", strconv.Itoa(p.policy.CRF))
	}
	args = append(args,
		"-profile:v", "high", "-level:v", "4.0",
		"-pix_fmt", "yuv420p", "-r", "24", "-g", strconv.FormatInt(finalJobSegmentGOP, 10),
		"-keyint_min", strconv.FormatInt(finalJobSegmentGOP, 10), "-sc_threshold", "0", "-bf", "0", "-flags", "+cgop",
		"-video_track_timescale", "90000", "-map_metadata", "-1", "-movflags", "+faststart", output,
	)
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("encode assembly-ready fragment: %w: %s", err, strings.TrimSpace(string(outputBytes)))
	}
	return nil
}

func finalJobSourceIsExactPreparedFragment(source map[string]any, request scriptgen.FinalJobVideoSegmentRequest, durationUS int64) bool {
	return strings.EqualFold(strings.TrimSpace(stringValue(source["profile_id"])), finalJobSegmentProfile) &&
		boolFromAny(source["copy_eligible"]) && boolFromAny(source["first_frame_keyframe"]) && boolFromAny(source["closed_gop"]) &&
		int64Value(source["source_in_us"]) == 0 && int64Value(source["duration_us"]) == durationUS &&
		int64Value(source["frame_count"]) == request.FrameCount &&
		int64Value(source["width"]) == 1920 && int64Value(source["height"]) == 1080 &&
		int64Value(source["fps_num"]) == finalJobSegmentFPS && int64Value(source["fps_den"]) == 1
}

func finalJobPreparedRef(base map[string]any, sha string, size, durationUS int64, request scriptgen.FinalJobVideoSegmentRequest) map[string]any {
	ref := make(map[string]any, len(base)+13)
	for key, value := range base {
		ref[key] = value
	}
	ref["sha256"], ref["size_bytes"], ref["duration_us"] = strings.ToLower(sha), size, durationUS
	ref["duration_ms"], ref["frame_count"] = (durationUS+999)/1000, request.FrameCount
	ref["mime"], ref["kind"] = "video/mp4", "video"
	ref["profile_id"], ref["copy_eligible"] = finalJobSegmentProfile, true
	ref["first_frame_keyframe"], ref["closed_gop"] = true, true
	ref["width"], ref["height"] = 1920, 1080
	ref["fps_num"], ref["fps_den"] = finalJobSegmentFPS, int64(1)
	ref["timeline_revision"], ref["timeline_sha256"] = request.TimelineRevision, strings.ToLower(request.TimelineSHA256)
	ref["timeline_start_frame"] = request.TimelineStartFrame
	return ref
}

func finalJobPreparedAssetID(request scriptgen.FinalJobVideoSegmentRequest, sourceSHA string) string {
	material := fmt.Sprintf("%s\n%s\n%d\n%d\n%d\n%s", request.SegmentID, sourceSHA, request.SourceInUS, request.FrameCount, request.TimelineStartFrame, request.TimelineSHA256)
	return "final-fragment-" + digest.SHA256String(material)[:32]
}

func framesDurationUS(frames int64) int64 {
	return (frames*1_000_000 + finalJobSegmentFPS/2) / finalJobSegmentFPS
}

func verifyFinalJobFragment(ctx context.Context, path string, frameCount int64) error {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return fmt.Errorf("ffprobe is required to certify prepared final-job fragments: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0", "-show_streams", "-show_format", "-of", "json", path)
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("probe prepared fragment: %w", err)
	}
	var probe struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
			Profile   string `json:"profile"`
			Level     int    `json:"level"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			PixelFmt  string `json:"pix_fmt"`
			AvgFPS    string `json:"avg_frame_rate"`
			Frames    string `json:"nb_frames"`
			BFrames   int    `json:"has_b_frames"`
			TimeBase  string `json:"time_base"`
			StartTime string `json:"start_time"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("decode prepared fragment probe: %w", err)
	}
	if len(probe.Streams) != 1 {
		return fmt.Errorf("prepared fragment must contain one video stream and no audio streams")
	}
	s := probe.Streams[0]
	if s.CodecName != "h264" || s.Profile != "High" || s.Level != 40 || s.Width != 1920 || s.Height != 1080 || s.PixelFmt != "yuv420p" || s.AvgFPS != "24/1" || s.BFrames != 0 || s.TimeBase != "1/90000" {
		return fmt.Errorf("prepared fragment stream does not match VELOX_ASSEMBLY_READY_V1")
	}
	if strings.TrimSpace(s.StartTime) != "0.000000" {
		return fmt.Errorf("prepared fragment does not start at timestamp zero")
	}
	if frames, parseErr := strconv.ParseInt(strings.TrimSpace(s.Frames), 10, 64); parseErr != nil || frames != frameCount {
		return fmt.Errorf("prepared fragment has %q frames; expected %d", s.Frames, frameCount)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64)
	if err != nil || time.Duration(seconds*float64(time.Second)) < time.Duration(framesDurationUS(frameCount)-1_000)*time.Microsecond || time.Duration(seconds*float64(time.Second)) > time.Duration(framesDurationUS(frameCount)+1_000)*time.Microsecond {
		return fmt.Errorf("prepared fragment duration does not match its frame count")
	}
	keyframesCmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "frame=key_frame", "-of", "csv=p=0", path)
	keyframes, err := keyframesCmd.Output()
	if err != nil {
		return fmt.Errorf("verify prepared fragment keyframes: %w", err)
	}
	frameIndex := int64(0)
	for _, line := range strings.Fields(string(keyframes)) {
		isKey := strings.TrimSpace(strings.Split(line, ",")[0]) == "1"
		shouldKey := frameIndex == 0 || frameIndex%finalJobSegmentGOP == 0
		if isKey != shouldKey {
			return fmt.Errorf("prepared fragment GOP is not closed and fixed at frame %d", frameIndex)
		}
		frameIndex++
	}
	if frameIndex != frameCount {
		return fmt.Errorf("prepared fragment keyframe probe returned %d frames; expected %d", frameIndex, frameCount)
	}
	return nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func int64Value(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		v, _ := n.Int64()
		return v
	case string:
		v, _ := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return v
	default:
		return 0
	}
}

func boolFromAny(value any) bool {
	b, _ := value.(bool)
	return b
}

var _ scriptgen.FinalJobVideoSegmentPreparer = (*finalJobSegmentPreparer)(nil)
