// Package audioasset — mp3_duration.go (TTS critical path, 2026-10-29).
//
// The sentence-chunked TTS path needs the duration of every synthesized
// chunk to remap word boundaries onto the merged track. Until now that
// duration came from the shared Rust media executor (`Probe`), which has a
// hard 4-runner pool and also serves the final audio render. With ~140
// chunks per run the probe queue was the dominant local cost of TTS and it
// ran inside the provider request slot.
//
// Edge TTS writes MPEG-1 Layer III audio (24 kHz mono, 48 kbps CBR). Summing
// the frame headers gives an exact, dependency-free duration in microseconds
// without spawning ffprobe. Anything unexpected (ID3-only prefix, VBR header,
// non-MP3 or truncated frames) returns an error and the caller keeps the
// existing media-executor probe as the authoritative fallback.
package audioasset

import (
	"fmt"
	"os"
	"time"
)

// mp3Frame is the subset of an MPEG audio frame header needed for duration.
type mp3Frame struct {
	frameLen   int
	sampleRate int
	samples    int
}

// mp3Layer3BitrateKbps maps the bitrate index to kbps for Layer III.
// Index 0 is "free", 15 is invalid.
var (
	mp3Layer3BitrateMPEG1 = [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mp3Layer3BitrateMPEG2 = [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	mp3SampleRates        = [...][3]int{
		{44100, 48000, 32000}, // MPEG 1
		{0, 0, 0},             // reserved
		{22050, 24000, 16000}, // MPEG 2
		{11025, 12000, 8000},  // MPEG 2.5
	}
)

// mp3Duration returns the exact duration of an MP3 file by walking its frame
// headers. It intentionally accepts only Layer III streams with a stable
// sample rate; every other shape is left to the media-executor probe.
func mp3Duration(path string) (time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	// Skip an ID3v2 tag ("ID3" + version + flags + synchsafe size).
	offset := 0
	if len(data) >= 10 && string(data[:3]) == "ID3" {
		size := int(data[6]&0x7F)<<21 | int(data[7]&0x7F)<<14 | int(data[8]&0x7F)<<7 | int(data[9]&0x7F)
		offset = 10 + size
		if offset < 0 || offset >= len(data) {
			return 0, fmt.Errorf("mp3: invalid ID3v2 size")
		}
	}
	// Drop a trailing ID3v1 tag so it is never mistaken for audio.
	end := len(data)
	if end-offset >= 128 && string(data[end-128:end-125]) == "TAG" {
		end -= 128
	}

	var totalSamples int64
	sampleRate := 0
	audioFrames := 0
	parsedFrames := 0
	for offset+4 <= end {
		frame, next, ok := parseMP3Frame(data, offset, end)
		if !ok {
			if parsedFrames == 0 {
				return 0, fmt.Errorf("mp3: no MPEG Layer III frame at offset %d", offset)
			}
			break
		}
		parsedFrames++
		// The first frame may be a Xing/Info metadata frame (Edge TTS
		// writes one). ffmpeg does not decode it as audio, so counting its
		// 576/1152 samples would shift every later boundary by one frame
		// (~24 ms). Its bitrate/sample rate belong to the header, not the
		// stream, so it must not establish the sample rate either.
		if parsedFrames == 1 && isMP3InfoFrame(data, offset, frame) {
			offset = next
			continue
		}
		if sampleRate == 0 {
			sampleRate = frame.sampleRate
		} else if frame.sampleRate != sampleRate {
			return 0, fmt.Errorf("mp3: sample rate changed within the stream")
		}
		totalSamples += int64(frame.samples)
		audioFrames++
		offset = next
	}
	if audioFrames == 0 || sampleRate <= 0 {
		return 0, fmt.Errorf("mp3: no decodable frames")
	}
	// Microsecond rounding is deliberate: the merged timeline and word
	// boundaries are integer microseconds, and the chunk offsets are summed
	// in microseconds by chunkedTTSProvider.
	return time.Duration(totalSamples*1_000_000/int64(sampleRate)) * time.Microsecond, nil
}

// isMP3InfoFrame reports whether the frame carries a Xing/Info VBR header
// instead of audio samples. The tag sits after the Layer III side info.
func isMP3InfoFrame(data []byte, offset int, frame mp3Frame) bool {
	versionBits := (data[offset+1] >> 3) & 0x03
	mono := data[offset+3]>>6 == 0x03
	sideInfo := 17 // MPEG 1 mono
	if versionBits != 3 {
		sideInfo = 9 // MPEG 2 / 2.5 mono
	}
	if !mono {
		sideInfo = 32
		if versionBits != 3 {
			sideInfo = 17
		}
	}
	tagOffset := offset + 4 + sideInfo
	if tagOffset+4 > offset+frame.frameLen || tagOffset+4 > len(data) {
		return false
	}
	tag := string(data[tagOffset : tagOffset+4])
	return tag == "Xing" || tag == "Info"
}

// parseMP3Frame parses one Layer III frame header and returns its descriptor
// plus the offset of the next frame. It rejects the reserved and unsupported
// encodings instead of guessing.
func parseMP3Frame(data []byte, offset, end int) (mp3Frame, int, bool) {
	if offset+4 > end || data[offset] != 0xFF || data[offset+1]&0xE0 != 0xE0 {
		return mp3Frame{}, 0, false
	}
	versionBits := (data[offset+1] >> 3) & 0x03
	layerBits := (data[offset+1] >> 1) & 0x03
	bitrateIndex := int(data[offset+2] >> 4)
	sampleRateIndex := int((data[offset+2] >> 2) & 0x03)
	padding := int((data[offset+2] >> 1) & 0x01)
	if versionBits == 1 || layerBits != 1 || bitrateIndex == 0 || bitrateIndex == 15 || sampleRateIndex == 3 {
		return mp3Frame{}, 0, false
	}
	var bitrateKbps, samples int
	var sampleRate int
	if versionBits == 3 { // MPEG 1
		bitrateKbps = mp3Layer3BitrateMPEG1[bitrateIndex]
		samples = 1152
		sampleRate = mp3SampleRates[0][sampleRateIndex]
	} else { // MPEG 2 or 2.5
		bitrateKbps = mp3Layer3BitrateMPEG2[bitrateIndex]
		samples = 576
		sampleRate = mp3SampleRates[versionBits][sampleRateIndex]
	}
	if bitrateKbps <= 0 || sampleRate <= 0 {
		return mp3Frame{}, 0, false
	}
	frameLen := 144*bitrateKbps*1000/sampleRate + padding
	if versionBits != 3 {
		frameLen = 72*bitrateKbps*1000/sampleRate + padding
	}
	if frameLen <= 4 || offset+frameLen > end {
		return mp3Frame{}, 0, false
	}
	return mp3Frame{frameLen: frameLen, sampleRate: sampleRate, samples: samples}, offset + frameLen, true
}
