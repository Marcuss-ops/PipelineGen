package render

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// sha256File returns the hex-encoded SHA-256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("sha256 open: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("sha256 hash: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// validateProducedClip validates a produced clip file, returns its
// ffprobe-reported duration (seconds), its SHA-256 hex digest, and an
// error if the file is not a valid video clip.
//
// Fase 1 contract: a clip is considered valid only when:
//   - the file exists and is non-empty;
//   - ffprobe can parse it;
//   - it contains at least one video stream;
//   - its duration is positive.
//
// When probeAfterCut is disabled, ffprobe is skipped and only the
// existence/size/hash checks run. This supports test fixtures and
// watcher paths that intentionally do not validate.
func (c *FFmpegCutter) validateProducedClip(ctx context.Context, path string) (durationSec float64, sha string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return 0, "", fmt.Errorf("clip stat failed: %w", statErr)
	}
	if info.Size() <= 0 {
		return 0, "", errors.New("clip is empty")
	}

	if c.probeAfterCut {
		probeInfo, probeErr := c.proc.Probe(ctx, path)
		if probeErr != nil {
			return 0, "", fmt.Errorf("clip ffprobe validation failed: %w", probeErr)
		}
		if probeInfo == nil {
			return 0, "", errors.New("clip ffprobe returned nil info")
		}
		if !probeInfo.HasVideo {
			return 0, "", errors.New("clip has no video stream")
		}
		if probeInfo.Duration <= 0 {
			return 0, "", errors.New("clip has non-positive duration")
		}
		durationSec = probeInfo.Duration.Seconds()
	}

	sha, hashErr := sha256File(path)
	if hashErr != nil {
		return 0, "", fmt.Errorf("clip sha256 failed: %w", hashErr)
	}

	return durationSec, sha, nil
}

// validateCanonicalClip validates a produced clip against the canonical
// stock profile. In addition to the checks performed by
// validateProducedClip, it enforces 1920×1080/24 fps H.264 yuv420p
// video and, when noAudio is false, AAC 48 kHz stereo audio. The
// returned duration must match expectedDurationSec within a small
// tolerance; pass expectedDurationSec <= 0 to skip the duration check.
func (c *FFmpegCutter) validateCanonicalClip(ctx context.Context, path string, noAudio bool, expectedDurationSec float64) (durationSec float64, sha string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return 0, "", fmt.Errorf("clip stat failed: %w", statErr)
	}
	if info.Size() <= 0 {
		return 0, "", errors.New("clip is empty")
	}

	if !c.probeAfterCut {
		// Canonical validation requires ffprobe; if probing is disabled
		// fall back to the basic existence/hash checks only.
		computedSHA, hashErr := sha256File(path)
		if hashErr != nil {
			return 0, "", fmt.Errorf("clip sha256 failed: %w", hashErr)
		}
		return 0, computedSHA, nil
	}

	probeInfo, probeErr := c.proc.Probe(ctx, path)
	if probeErr != nil {
		return 0, "", fmt.Errorf("clip ffprobe validation failed: %w", probeErr)
	}
	if probeInfo == nil {
		return 0, "", errors.New("clip ffprobe returned nil info")
	}
	if !probeInfo.HasVideo {
		return 0, "", errors.New("clip has no video stream")
	}
	if probeInfo.Duration <= 0 {
		return 0, "", errors.New("clip has non-positive duration")
	}
	durationSec = probeInfo.Duration.Seconds()

	// Canonical video profile guards.
	if probeInfo.Width != 1920 {
		return 0, "", fmt.Errorf("canonical width violation: got %d, want 1920", probeInfo.Width)
	}
	if probeInfo.Height != 1080 {
		return 0, "", fmt.Errorf("canonical height violation: got %d, want 1080", probeInfo.Height)
	}
	if math.Abs(probeInfo.FPS-24.0) > 0.5 {
		return 0, "", fmt.Errorf("canonical fps violation: got %.2f, want ~24", probeInfo.FPS)
	}
	if probeInfo.VideoCodec != "h264" {
		return 0, "", fmt.Errorf("canonical video codec violation: got %q, want h264", probeInfo.VideoCodec)
	}
	if probeInfo.PixelFormat != "yuv420p" {
		return 0, "", fmt.Errorf("canonical pixel format violation: got %q, want yuv420p", probeInfo.PixelFormat)
	}

	// Canonical audio profile guards.
	if !noAudio {
		if !probeInfo.HasAudio {
			return 0, "", errors.New("canonical audio violation: no audio stream present")
		}
		if probeInfo.AudioCodec != "aac" {
			return 0, "", fmt.Errorf("canonical audio codec violation: got %q, want aac", probeInfo.AudioCodec)
		}
		if probeInfo.SampleRate != 48000 {
			return 0, "", fmt.Errorf("canonical sample rate violation: got %d, want 48000", probeInfo.SampleRate)
		}
		if probeInfo.Channels != 2 {
			return 0, "", fmt.Errorf("canonical channels violation: got %d, want 2", probeInfo.Channels)
		}
	} else {
		if probeInfo.HasAudio {
			return 0, "", errors.New("canonical audio violation: audio stream present but noAudio requested")
		}
	}

	// Duration guard.
	if expectedDurationSec > 0 {
		const durationTolerance = 0.25
		if math.Abs(durationSec-expectedDurationSec) > durationTolerance {
			return 0, "", fmt.Errorf("canonical duration violation: got %.3fs, want %.3fs ± %.3fs",
				durationSec, expectedDurationSec, durationTolerance)
		}
	}

	sha, hashErr := sha256File(path)
	if hashErr != nil {
		return 0, "", fmt.Errorf("clip sha256 failed: %w", hashErr)
	}

	return durationSec, sha, nil
}
