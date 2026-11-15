package main

// burn_subtitles.go owns the delivery burn. The remote Master assembles the
// final video and the Producer never creates one locally, so the certified
// artifact is the only place where captions can be added after assembly. This
// command re-encodes that artifact once, verifies that every scene with speech
// actually carries visible caption pixels, and leaves the receipt untouched.

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	platformconfig "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/deliveryburn"
	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

const captionPixelThreshold = 250

type sceneCaptionCheck struct {
	SceneID      string  `json:"scene_id"`
	Cue          string  `json:"cue"`
	SampleAtS    float64 `json:"sample_at_s"`
	CaptionPx    int     `json:"caption_px"`
	CaptionFound bool    `json:"caption_found"`
}

func cmdBurnSubtitles(args []string) int {
	fs, err := parseFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: %v\n", err)
		return exitUsage
	}
	input := fs.get("input", fs.get("i", ""))
	output := fs.get("out", fs.get("o", ""))
	if input == "" || output == "" {
		fmt.Fprintln(os.Stderr, "velox burn-subtitles: --input FILE and --out FILE are required")
		return exitUsage
	}
	absIn, err := filepath.Abs(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: resolve input: %v\n", err)
		return exitUsage
	}
	absOut, err := filepath.Abs(output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: resolve output: %v\n", err)
		return exitUsage
	}
	if absIn == absOut {
		fmt.Fprintln(os.Stderr, "velox burn-subtitles: --out must differ from --input (the certified artifact is never overwritten)")
		return exitUsage
	}
	if _, err := os.Stat(absIn); err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: input: %v\n", err)
		return exitUsage
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Fprintln(os.Stderr, "velox burn-subtitles: ffmpeg is not on PATH")
		return exitFailure
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		fmt.Fprintln(os.Stderr, "velox burn-subtitles: ffprobe is not on PATH")
		return exitFailure
	}

	var cues []subtitleCue
	var style assStyle
	var assText []byte
	if assPath := fs.get("ass", ""); assPath != "" {
		assText, err = os.ReadFile(assPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: read --ass: %v\n", err)
			return exitUsage
		}
	} else {
		jobFull := fs.get("job-full", "")
		if jobFull == "" {
			fmt.Fprintln(os.Stderr, "velox burn-subtitles: --job-full FILE or --ass FILE is required")
			return exitUsage
		}
		raw, readErr := os.ReadFile(jobFull)
		if readErr != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: read --job-full: %v\n", readErr)
			return exitUsage
		}
		cues, style, err = buildSubtitles(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: %v\n", err)
			return exitFailure
		}
		if font := strings.TrimSpace(fs.get("font", "")); font != "" {
			style.Font = font
		}
		if size := attrInt(fs.get("font-size", ""), 0); size > 0 {
			style.FontSize = size
		}
		var buf bytes.Buffer
		if err := writeASS(&buf, cues, style); err != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: build ASS: %v\n", err)
			return exitFailure
		}
		assText = buf.Bytes()
	}

	tmpDir, err := os.MkdirTemp("", "velox-burn-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: temp dir: %v\n", err)
		return exitFailure
	}
	defer os.RemoveAll(tmpDir)
	assFile := filepath.Join(tmpDir, "subtitles.ass")
	if err := atomicwrite.WriteFile(assFile, assText, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: write ASS: %v\n", err)
		return exitFailure
	}
	if dir := strings.TrimSpace(fs.get("keep-ass", "")); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: keep-ass: %v\n", err)
			return exitFailure
		}
		if err := atomicwrite.WriteFile(filepath.Join(dir, "velox-subtitles.ass"), assText, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: keep-ass: %v\n", err)
			return exitFailure
		}
	}

	tmpOut := absOut + ".tmp.mp4"
	defer os.Remove(tmpOut)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	// Run from the temp dir so the filter argument is a plain relative name:
	// absolute paths with drive separators or quotes would need ASS filter
	// escaping, and that escaping is exactly where delivery scripts break.
	policy := (platformconfig.VideoConfig{
		Codec:  platformconfig.CanonicalStockEncoderCodec,
		Preset: platformconfig.CanonicalStockEncoderPreset,
		CRF:    18,
	}).EncoderPolicy()
	if err := deliveryburn.Burn(ctx, deliveryburn.BurnOptions{
		WorkDir:    tmpDir,
		ASSName:    "subtitles.ass",
		InputPath:  absIn,
		OutputPath: tmpOut,
		Codec:      policy.Codec,
		Preset:     policy.Preset,
		CRF:        policy.CRF,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: %v\n", err)
		return exitFailure
	}
	if err := os.Rename(tmpOut, absOut); err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: publish output: %v\n", err)
		return exitFailure
	}

	probe, err := deliveryburn.ProbeMedia(ctx, absOut)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: probe output: %v\n", err)
		return exitFailure
	}
	inputProbe, err := deliveryburn.ProbeMedia(ctx, absIn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: probe input: %v\n", err)
		return exitFailure
	}
	width, height, videoCodec := 0, 0, ""
	audioCodec := ""
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			if width == 0 {
				width, height, videoCodec = stream.Width, stream.Height, stream.CodecName
			}
		case "audio":
			if audioCodec == "" {
				audioCodec = stream.CodecName
			}
		}
	}
	if width <= 0 || height <= 0 {
		fmt.Fprintln(os.Stderr, "velox burn-subtitles: output has no video stream")
		return exitFailure
	}
	durationOut := probe.DurationSeconds
	durationIn := inputProbe.DurationSeconds
	if durationIn > 0 && (durationOut < durationIn-0.2 || durationOut > durationIn+0.2) {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: duration changed: input %.3fs output %.3fs\n", durationIn, durationOut)
		return exitFailure
	}

	var checks []sceneCaptionCheck
	if !fs.has("no-verify") && len(cues) > 0 {
		colour, colourErr := hexRGB(style.Primary)
		if colourErr != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: verify colour: %v\n", colourErr)
			return exitFailure
		}
		checks, err = verifySceneCaptions(ctx, absOut, cues, colour, width, height)
		if err != nil {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: verify captions: %v\n", err)
			return exitFailure
		}
		failed := 0
		for _, check := range checks {
			if !check.CaptionFound {
				failed++
			}
		}
		if failed > 0 {
			fmt.Fprintf(os.Stderr, "velox burn-subtitles: %d/%d scenes have no visible captions\n", failed, len(checks))
			return exitFailure
		}
	}

	sum, _, err := digest.SHA256File(absOut)
	if err != nil {
		fmt.Fprintf(os.Stderr, "velox burn-subtitles: hash output: %v\n", err)
		return exitFailure
	}
	first, last := 0.0, 0.0
	if len(cues) > 0 {
		first, last = cues[0].Start, cues[len(cues)-1].End
	}
	if fs.has("json") {
		return emitJSON(map[string]any{
			"input":                absIn,
			"output":               absOut,
			"sha256":               sum,
			"duration_s":           durationOut,
			"cues":                 len(cues),
			"first_cue_s":          first,
			"last_cue_s":           last,
			"font":                 style.Font,
			"font_size_px":         style.FontSize,
			"video_codec":          videoCodec,
			"width":                width,
			"height":               height,
			"audio_codec":          audioCodec,
			"scene_caption_checks": checks,
		})
	}
	fmt.Printf("burned %d cues (%.2fs..%.2fs) → %s\n", len(cues), first, last, absOut)
	fmt.Printf("video %s %dx%d, audio %s, duration %.3fs, font %s %dpx\n",
		videoCodec, width, height, audioCodec, durationOut, style.Font, style.FontSize)
	if len(checks) > 0 {
		minPx := checks[0].CaptionPx
		for _, check := range checks {
			if check.CaptionPx < minPx {
				minPx = check.CaptionPx
			}
		}
		fmt.Printf("caption check: %d/%d scenes visible (min %d px)\n", len(checks), len(checks), minPx)
	}
	fmt.Printf("sha256 %s\n", sum)
	return exitOK
}

func hexRGB(colour string) ([3]uint8, error) {
	value := strings.TrimPrefix(strings.TrimSpace(colour), "#")
	if len(value) != 6 {
		return [3]uint8{}, fmt.Errorf("invalid colour %q", colour)
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return [3]uint8{}, err
	}
	return [3]uint8{raw[0], raw[1], raw[2]}, nil
}

// verifySceneCaptions samples one frame per scene at the middle of its longest
// cue and counts pixels close to the requested caption colour in the lower
// part of the frame. This is the check that separates a real burn from a file
// that merely carries the same duration.
func verifySceneCaptions(ctx context.Context, path string, cues []subtitleCue, colour [3]uint8, width, height int) ([]sceneCaptionCheck, error) {
	longest := make(map[string]subtitleCue)
	order := make([]string, 0)
	for _, cue := range cues {
		if _, seen := longest[cue.SceneID]; !seen {
			order = append(order, cue.SceneID)
		}
		if len(cue.Text) > len(longest[cue.SceneID].Text) {
			longest[cue.SceneID] = cue
		}
	}
	checks := make([]sceneCaptionCheck, 0, len(order))
	for _, sceneID := range order {
		cue := longest[sceneID]
		at := cue.Start + (cue.End-cue.Start)/2
		if at <= cue.Start {
			at = cue.Start + 0.3
		}
		frame, err := deliveryburn.SampleFrame(ctx, path, at, width, height)
		if err != nil {
			return nil, fmt.Errorf("scene %s at %.2fs: %w", sceneID, at, err)
		}
		px := countColourPixels(frame, width, height, colour)
		checks = append(checks, sceneCaptionCheck{
			SceneID:      sceneID,
			Cue:          cue.Text,
			SampleAtS:    at,
			CaptionPx:    px,
			CaptionFound: px >= captionPixelThreshold,
		})
	}
	return checks, nil
}

// countColourPixels looks only at the lower 60% of the frame: captions are
// bottom-anchored, and the upper half of a stock shot is where warm colours
// could otherwise produce false positives.
func countColourPixels(frame []byte, width, height int, colour [3]uint8) int {
	startRow := height * 40 / 100
	count := 0
	for y := startRow; y < height; y++ {
		row := frame[y*width*3 : (y+1)*width*3]
		for x := 0; x < width; x++ {
			r, g, b := row[x*3], row[x*3+1], row[x*3+2]
			if absInt(int(r)-int(colour[0])) <= 45 &&
				absInt(int(g)-int(colour[1])) <= 45 &&
				absInt(int(b)-int(colour[2])) <= 60 {
				count++
			}
		}
	}
	return count
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
