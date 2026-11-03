package wiring

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// canonicalAVCC is the frozen SPS/PPS record for assembly-ready H.264.
const canonicalAVCC = "01640028ffe1001e67640028acb403c0113f2e02d4040405000003000100000300300f1832a001000468ef0fcbfdf8f800"

// copyAVC3Window remuxes a closed-GOP H.264 window without decoding or
// encoding. h264_mp4toannexb places the source SPS/PPS in-band at the IDR;
// the MP4 sample entry is changed to avc3 and carries the canonical avcC so
// the existing copy certification remains meaningful. Different per-window
// SPS/PPS therefore stay attached to their own slices instead of being
// misapplied from the first segment across the concatenated file.
func (p *finalJobSegmentPreparer) copyAVC3Window(ctx context.Context, source, output string, request scriptgen.FinalJobVideoSegmentRequest) error {
	ffmpeg := p.ffmpeg
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return fmt.Errorf("ffmpeg is required to packet-copy AVC3 windows: %w", err)
		}
	}
	tsPath := output + ".ts"
	defer os.Remove(tsPath)
	start := strconv.FormatFloat(float64(request.SourceInUS)/1_000_000, 'f', 6, 64)
	first := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-ss", start, "-i", source,
		"-map", "0:v:0", "-an", "-c:v", "copy", "-bsf:v", "h264_mp4toannexb",
		"-frames:v", strconv.FormatInt(request.FrameCount, 10), "-f", "mpegts", tsPath}
	if outputBytes, err := exec.CommandContext(ctx, ffmpeg, first...).CombinedOutput(); err != nil {
		return fmt.Errorf("packet-copy final-job window to Annex-B: %w: %s", err, strings.TrimSpace(string(outputBytes)))
	}
	second := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", tsPath,
		"-map", "0:v:0", "-an", "-c:v", "copy", "-tag:v", "avc3",
		"-video_track_timescale", strconv.Itoa(finalJobDelivery.VideoTimeBase.Den),
		"-avoid_negative_ts", "disabled", "-movflags", "+negative_cts_offsets", "-map_metadata", "-1", output}
	if outputBytes, err := exec.CommandContext(ctx, ffmpeg, second...).CombinedOutput(); err != nil {
		return fmt.Errorf("remux packet-copied window as avc3: %w: %s", err, strings.TrimSpace(string(outputBytes)))
	}
	if err := patchAVC3CanonicalAVCC(output); err != nil {
		return fmt.Errorf("set canonical avc3 decoder configuration: %w", err)
	}
	got, err := readAVCDecoderConfigurationSHA256(output)
	if err != nil {
		return err
	}
	if got != finalJobDelivery.VideoExtradataSHA256 {
		return fmt.Errorf("avc3 avcC %s != canonical %s", got, finalJobDelivery.VideoExtradataSHA256)
	}
	return nil
}

// patchAVC3CanonicalAVCC rewrites the trailing moov atom, replaces avcC with
// the canonical record and changes the visual sample entry from avc1 to avc3.
// It refuses fast-start files: changing moov size before mdat would invalidate
// chunk offsets, while our remux deliberately writes moov after mdat.
func patchAVC3CanonicalAVCC(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	canonical, err := hex.DecodeString(canonicalAVCC)
	if err != nil {
		return err
	}
	var rebuilt []byte
	mdatEnd, moovStart := -1, -1
	for offset := 0; offset < len(data); {
		box, next, err := parseMP4BoxBytes(data, offset, len(data))
		if err != nil {
			return err
		}
		if box.typ == "mdat" {
			mdatEnd = next
		}
		if box.typ == "moov" {
			moovStart = offset
			patched, patchErr := rewriteAVC3Box(data, box, canonical)
			if patchErr != nil {
				return patchErr
			}
			rebuilt = append(rebuilt, patched...)
		} else {
			rebuilt = append(rebuilt, data[offset:next]...)
		}
		offset = next
	}
	if moovStart < 0 || mdatEnd < 0 || moovStart < mdatEnd {
		return fmt.Errorf("expected trailing moov after mdat to preserve chunk offsets")
	}
	tmp := path + ".avc3-patch"
	if err := os.WriteFile(tmp, rebuilt, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

type byteMP4Box struct {
	typ       string
	payload   int
	end       int
	headerLen int
}

func parseMP4BoxBytes(data []byte, offset, limit int) (byteMP4Box, int, error) {
	if offset < 0 || offset+8 > limit || limit > len(data) {
		return byteMP4Box{}, 0, fmt.Errorf("invalid MP4 box bounds %d..%d", offset, limit)
	}
	size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	header := 8
	if size == 1 {
		if offset+16 > limit {
			return byteMP4Box{}, 0, fmt.Errorf("truncated extended MP4 box")
		}
		u64 := binary.BigEndian.Uint64(data[offset+8 : offset+16])
		if u64 > uint64(limit-offset) {
			return byteMP4Box{}, 0, fmt.Errorf("oversized extended MP4 box")
		}
		size, header = int(u64), 16
	} else if size == 0 {
		size = limit - offset
	}
	if size < header || offset+size > limit {
		return byteMP4Box{}, 0, fmt.Errorf("invalid MP4 box size %d", size)
	}
	return byteMP4Box{typ: string(data[offset+4 : offset+8]), payload: offset + header, end: offset + size, headerLen: header}, offset + size, nil
}

func rewriteAVC3Box(data []byte, box byteMP4Box, canonical []byte) ([]byte, error) {
	payload := data[box.payload:box.end]
	typ := box.typ
	switch typ {
	case "avcC":
		return makeMP4Box("avcC", canonical), nil
	case "moov", "trak", "mdia", "minf", "stbl":
		children, err := rewriteAVC3Children(data, box.payload, box.end, canonical)
		if err != nil {
			return nil, err
		}
		return makeMP4Box(typ, children), nil
	case "stsd":
		if len(payload) < 8 {
			return nil, fmt.Errorf("truncated stsd")
		}
		children, err := rewriteAVC3Children(data, box.payload+8, box.end, canonical)
		if err != nil {
			return nil, err
		}
		return makeMP4Box(typ, append(append([]byte{}, payload[:8]...), children...)), nil
	case "avc1", "avc3":
		if len(payload) < 78 {
			return nil, fmt.Errorf("truncated AVC visual sample entry")
		}
		children, err := rewriteAVC3Children(data, box.payload+78, box.end, canonical)
		if err != nil {
			return nil, err
		}
		fixed := append([]byte{}, payload[:78]...)
		fixed = append(fixed, children...)
		return makeMP4Box("avc3", fixed), nil
	default:
		return append([]byte{}, data[box.payload-box.headerLen:box.end]...), nil
	}
}

func rewriteAVC3Children(data []byte, start, end int, canonical []byte) ([]byte, error) {
	var out []byte
	for offset := start; offset < end; {
		box, next, err := parseMP4BoxBytes(data, offset, end)
		if err != nil {
			return nil, err
		}
		patched, err := rewriteAVC3Box(data, box, canonical)
		if err != nil {
			return nil, err
		}
		out = append(out, patched...)
		offset = next
	}
	return out, nil
}

func makeMP4Box(typ string, payload []byte) []byte {
	box := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	copy(box[4:8], typ)
	copy(box[8:], payload)
	return box
}
