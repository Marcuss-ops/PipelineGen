package wiring

// final_job_avcc.go owns the fast, dependency-free read of the exact
// AVCDecoderConfigurationRecord (the MP4 `avcC` payload) carried by a fragment.
//
// The avcC is the packet-copy identity: profile, level, geometry and fps can
// agree while SPS/PPS differ, and `concat -c copy` writes only the first
// segment's avcC. Reading the box directly (instead of spawning ffprobe on
// every produced fragment) keeps the post-encode gate cheap enough to run on
// the hot path. Only the small metadata boxes are read; `mdat` is skipped.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

type mp4Box struct {
	typ          string
	payloadStart int64
	end          int64
}

func readMP4Box(r io.ReaderAt, offset, limit int64) (mp4Box, error) {
	var header [8]byte
	if offset < 0 || offset+8 > limit {
		return mp4Box{}, fmt.Errorf("mp4: box header at %d is outside the file", offset)
	}
	if _, err := r.ReadAt(header[:], offset); err != nil {
		return mp4Box{}, fmt.Errorf("mp4: read box header at %d: %w", offset, err)
	}
	size := int64(binary.BigEndian.Uint32(header[:4]))
	typ := string(header[4:8])
	headerLen := int64(8)
	switch size {
	case 0:
		size = limit - offset
	case 1:
		var extended [8]byte
		if offset+16 > limit {
			return mp4Box{}, fmt.Errorf("mp4: extended box header at %d is truncated", offset)
		}
		if _, err := r.ReadAt(extended[:], offset+8); err != nil {
			return mp4Box{}, fmt.Errorf("mp4: read extended box header at %d: %w", offset, err)
		}
		extendedSize := binary.BigEndian.Uint64(extended[:])
		if extendedSize > uint64(limit-offset) {
			return mp4Box{}, fmt.Errorf("mp4: box %q at %d overruns the file", typ, offset)
		}
		size = int64(extendedSize)
		headerLen = 16
	}
	if size < headerLen || offset+size > limit {
		return mp4Box{}, fmt.Errorf("mp4: box %q at %d has invalid size %d", typ, offset, size)
	}
	return mp4Box{typ: typ, payloadStart: offset + headerLen, end: offset + size}, nil
}

func findMP4Child(r io.ReaderAt, parent mp4Box, want string) (mp4Box, bool, error) {
	for offset := parent.payloadStart; offset+8 <= parent.end; {
		box, err := readMP4Box(r, offset, parent.end)
		if err != nil {
			return mp4Box{}, false, err
		}
		if box.typ == want {
			return box, true, nil
		}
		offset = box.end
	}
	return mp4Box{}, false, nil
}

func mp4HandlerType(r io.ReaderAt, mdia mp4Box) (string, error) {
	hdlr, ok, err := findMP4Child(r, mdia, "hdlr")
	if err != nil || !ok {
		return "", err
	}
	// FullBox version/flags (4) + pre_defined (4) + handler_type (4).
	if hdlr.payloadStart+12 > hdlr.end {
		return "", fmt.Errorf("mp4: hdlr box is truncated")
	}
	var handler [4]byte
	if _, err := r.ReadAt(handler[:], hdlr.payloadStart+8); err != nil {
		return "", fmt.Errorf("mp4: read hdlr handler_type: %w", err)
	}
	return string(handler[:]), nil
}

func avcCFromSampleEntry(r io.ReaderAt, entry mp4Box) ([]byte, error) {
	// VisualSampleEntry fixed fields: 78 bytes after the box header, then the
	// child boxes (avcC is one of them).
	for offset := entry.payloadStart + 78; offset+8 <= entry.end; {
		box, err := readMP4Box(r, offset, entry.end)
		if err != nil {
			return nil, err
		}
		if box.typ == "avcC" {
			payload := make([]byte, box.end-box.payloadStart)
			if _, err := r.ReadAt(payload, box.payloadStart); err != nil {
				return nil, fmt.Errorf("mp4: read avcC payload: %w", err)
			}
			return payload, nil
		}
		offset = box.end
	}
	return nil, fmt.Errorf("mp4: sample entry %q carries no avcC box", entry.typ)
}

func avcCFromMoov(r io.ReaderAt, moov mp4Box) ([]byte, error) {
	for offset := moov.payloadStart; offset+8 <= moov.end; {
		trak, err := readMP4Box(r, offset, moov.end)
		if err != nil {
			return nil, err
		}
		offset = trak.end
		if trak.typ != "trak" {
			continue
		}
		mdia, ok, err := findMP4Child(r, trak, "mdia")
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		handler, err := mp4HandlerType(r, mdia)
		if err != nil || handler != "vide" {
			continue
		}
		minf, ok, err := findMP4Child(r, mdia, "minf")
		if err != nil || !ok {
			continue
		}
		stbl, ok, err := findMP4Child(r, minf, "stbl")
		if err != nil || !ok {
			continue
		}
		stsd, ok, err := findMP4Child(r, stbl, "stsd")
		if err != nil || !ok {
			continue
		}
		// stsd is a FullBox: version/flags (4) + entry_count (4), then entries.
		if stsd.payloadStart+8 > stsd.end {
			return nil, fmt.Errorf("mp4: stsd box is truncated")
		}
		var countBytes [4]byte
		if _, err := r.ReadAt(countBytes[:], stsd.payloadStart+4); err != nil {
			return nil, fmt.Errorf("mp4: read stsd entry count: %w", err)
		}
		count := binary.BigEndian.Uint32(countBytes[:])
		entryOffset := stsd.payloadStart + 8
		for i := uint32(0); i < count && entryOffset+8 <= stsd.end; i++ {
			entry, err := readMP4Box(r, entryOffset, stsd.end)
			if err != nil {
				return nil, err
			}
			entryOffset = entry.end
			if entry.typ != "avc1" && entry.typ != "avc3" {
				continue
			}
			return avcCFromSampleEntry(r, entry)
		}
	}
	return nil, fmt.Errorf("mp4: no video track with an avcC box")
}

// readAVCDecoderConfiguration returns the exact avcC payload of the first
// H.264 video track.
func readAVCDecoderConfiguration(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open fragment for avcC: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat fragment for avcC: %w", err)
	}
	limit := info.Size()
	for offset := int64(0); offset+8 <= limit; {
		box, err := readMP4Box(file, offset, limit)
		if err != nil {
			return nil, err
		}
		offset = box.end
		if box.typ != "moov" {
			continue
		}
		return avcCFromMoov(file, box)
	}
	return nil, fmt.Errorf("mp4: moov box not found")
}

// readAVCDecoderConfigurationSHA256 is the packet-copy identity of the
// fragment's SPS/PPS. Empty/error means the bytes cannot be certified.
func readAVCDecoderConfigurationSHA256(path string) (string, error) {
	payload, err := readAVCDecoderConfiguration(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
