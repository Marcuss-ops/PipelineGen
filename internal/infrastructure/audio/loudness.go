// Package audioasset — loudness.go (PR-VO-LOUDNESS-GATE, August 2026):
// minimum-loudness gate for synthesized voiceover.
//
// The TTS bridge occasionally returns a non-empty but silent audio file
// (edge-tts glitch) which the empty-file check alone cannot catch. This
// gate measures the synthesized VO's peak level via ffmpeg volumedetect
// and fails closed with ErrSilentAudio when the audio never rises above the
// audible floor, so Generate retries the synthesis exactly like the
// empty-audio condition.
//
// Architecture note: the measurement itself (exec.CommandContext("ffmpeg",
// ...)) is an ARCH-ALLOWLIST'd process invocation at the same seam as the
// legacy TTS spawn-per-call path (processor.go::generateLegacy) — the only
// other external-process site in this package. The narrow loudnessProber
// port keeps the gate testable without shelling out.
package audioasset

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// MinAudibleMaxVolumeDB is the loudness floor for a synthesized VO. A file
// whose peak (volumedetect max_volume) never rises above this level is
// treated as inaudible/silent. Normal Edge TTS speech peaks around -2..-6 dB;
// digital silence measures -91 dB (or -inf), so -45 dB leaves a wide margin
// between real speech and a silent capture.
const MinAudibleMaxVolumeDB = -45.0

// Loudness is a volumedetect measurement of a local audio file.
type Loudness struct {
	// MeanDB is the mean_volume across the whole file (-inf for silence).
	MeanDB float64
	// MaxDB is the max_volume peak across the whole file (-inf/-91 for silence).
	MaxDB float64
}

// IsSilent reports whether the measurement is below the audible floor. A
// non-finite peak (NaN or -inf) is treated as silence — there is no
// measurable peak.
func (l Loudness) IsSilent() bool {
	if math.IsNaN(l.MaxDB) || math.IsInf(l.MaxDB, -1) {
		return true
	}
	return l.MaxDB < MinAudibleMaxVolumeDB
}

// loudnessProber is the narrow port the TTS synthesis depends on for the
// minimum-loudness gate. The concrete ffmpegLoudnessProber satisfies it; the
// interface exists so tests can inject a fake without shelling out.
type loudnessProber interface {
	MeasureLoudness(ctx context.Context, path string) (Loudness, error)
}

// ffmpegLoudnessProber measures loudness via `ffmpeg -af volumedetect`.
type ffmpegLoudnessProber struct {
	ffmpegBin string
}

// NewFFmpegLoudnessProber builds the production loudness prober for a given
// ffmpeg binary path. An empty path falls back to "ffmpeg" on PATH.
func NewFFmpegLoudnessProber(ffmpegBin string) loudnessProber {
	if strings.TrimSpace(ffmpegBin) == "" {
		ffmpegBin = "ffmpeg"
	}
	return ffmpegLoudnessProber{ffmpegBin: ffmpegBin}
}

func (p ffmpegLoudnessProber) MeasureLoudness(ctx context.Context, path string) (Loudness, error) {
	return measureVolume(ctx, p.ffmpegBin, path)
}

// measureVolume runs `ffmpeg -hide_banner -i <path> -af volumedetect -f null -`
// and parses the mean_volume / max_volume lines from stderr.
func measureVolume(ctx context.Context, ffmpegBin, path string) (Loudness, error) {
	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-i", path, "-af", "volumedetect", "-f", "null", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Loudness{}, fmt.Errorf("audioasset: volumedetect %q failed: %w: %s",
			path, err, strings.TrimSpace(string(out)))
	}
	return parseVolumedetect(out)
}

// parseVolumedetect extracts mean_volume and max_volume from ffmpeg's
// volumedetect stderr. It is a pure function so it is unit-testable without
// ffmpeg.
func parseVolumedetect(output []byte) (Loudness, error) {
	l := Loudness{MeanDB: math.Inf(-1), MaxDB: math.Inf(-1)}
	var foundMean, foundMax bool
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		for i, tok := range fields {
			switch tok {
			case "mean_volume:":
				v, err := parseVolumeValue(fields, i, line)
				if err != nil {
					return Loudness{}, err
				}
				l.MeanDB = v
				foundMean = true
			case "max_volume:":
				v, err := parseVolumeValue(fields, i, line)
				if err != nil {
					return Loudness{}, err
				}
				l.MaxDB = v
				foundMax = true
			}
		}
	}
	if !foundMean || !foundMax {
		return Loudness{}, fmt.Errorf("audioasset: volumedetect produced no mean_volume/max_volume")
	}
	return l, nil
}

// parseVolumeValue parses the dB value that follows "mean_volume:" or
// "max_volume:" at fields[i], including the "-inf" sentinel for digital
// silence.
func parseVolumeValue(fields []string, i int, line string) (float64, error) {
	if i+2 >= len(fields) || fields[i+2] != "dB" {
		return 0, fmt.Errorf("audioasset: volumedetect stat malformed: %q", line)
	}
	if fields[i+1] == "-inf" {
		return math.Inf(-1), nil
	}
	v, err := strconv.ParseFloat(fields[i+1], 64)
	if err != nil {
		return 0, fmt.Errorf("audioasset: volumedetect stat malformed: %q: %w", line, err)
	}
	return v, nil
}
