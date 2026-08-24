// Package downloader — hls_playlist.go: m3u8 master + media playlist parsing
// (RFC 8216 subset).
//
// Split from hls_direct.go (commit refactor(downloader): split hls_direct.go
// into playlist/segment/ffmpeg concerns). This file owns the
// single-pass m3u8 scanner + the parsed data types + the parsing
// helpers (attr extractors + EXTXINF duration + BYTERANGE parser).
package downloader

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// HLSVariant is a single variant from a master playlist.
type HLSVariant struct {
	URI        string
	Bandwidth  int
	Resolution string
	Codecs     string
}

// HLSKey describes the EXT-X-KEY directive for the segments that
// follow it. Method is always "AES-128" for the supported
// encryption method; future SAMPLE-AES support would add new
// methods.
type HLSKey struct {
	Method string
	URI    string
	IV     string
}

// HLSSegment is one segment of a media playlist.
type HLSSegment struct {
	URI       string
	Duration  time.Duration
	Sequence  int64
	Key       *HLSKey
	ByteRange HLSSegmentByteRange
}

// HLSSegmentByteRange is a subset of EXT-X-BYTERANGE.
type HLSSegmentByteRange struct {
	Begin int64
	End   int64
}

// HLSPlaylist is the parsed result of an m3u8 fetch. Either
// IsMaster (with Variants) or contains Segments.
type HLSPlaylist struct {
	IsMaster  bool
	Version   int
	TargetDur time.Duration
	MediaSeq  int64
	Variants  []HLSVariant
	Segments  []HLSSegment
	EndList   bool

	pendingByteRange HLSSegmentByteRange
}

func (p *HLSPlaylist) getPendingByteRange() HLSSegmentByteRange { return p.pendingByteRange }
func (p *HLSPlaylist) setPendingByteRange(r HLSSegmentByteRange) {
	p.pendingByteRange = r
}

// ParseM3U8 is the canonical m3u8 mini-parser. Supports:
//   - Master playlists: #EXT-X-STREAM-INF + variant URI
//   - Media playlists:  #EXTINF + segment URI
//   - #EXT-X-VERSION, #EXT-X-TARGETDURATION, #EXT-X-MEDIA-SEQUENCE
//   - #EXT-X-KEY (METHOD=AES-128, URI=..., IV=0x...)
//   - #EXT-X-BYTERANGE
//   - #EXT-X-ENDLIST
//   - #EXT-X-DISCONTINUITY (preserved as a no-op for now)
func ParseM3U8(body string) (*HLSPlaylist, error) {
	pl := &HLSPlaylist{}
	var currentKey *HLSKey
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			v := HLSVariant{}
			if b, ok := attrInt(line, "BANDWIDTH"); ok {
				v.Bandwidth = b
			}
			if r, ok := attrString(line, "RESOLUTION"); ok {
				v.Resolution = r
			}
			if c, ok := attrString(line, "CODECS"); ok {
				v.Codecs = c
			}
			for scanner.Scan() {
				next := strings.TrimSpace(scanner.Text())
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				v.URI = next
				break
			}
			pl.IsMaster = true
			pl.Variants = append(pl.Variants, v)
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-VERSION") {
			if v, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-VERSION:")); err == nil {
				pl.Version = v
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-TARGETDURATION") {
			if v, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:")); err == nil {
				pl.TargetDur = time.Duration(v) * time.Second
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE") {
			if v, err := strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64); err == nil {
				pl.MediaSeq = v
			}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-KEY") {
			method, _ := attrString(line, "METHOD")
			uri, _ := attrString(line, "URI")
			iv, _ := attrString(line, "IV")
			if strings.EqualFold(method, "NONE") {
				currentKey = nil
				continue
			}
			iv = strings.TrimSpace(iv)
			iv = strings.TrimPrefix(iv, "0x")
			iv = strings.TrimPrefix(iv, "0X")
			currentKey = &HLSKey{Method: method, URI: uri, IV: iv}
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-ENDLIST") {
			pl.EndList = true
			continue
		}
		if strings.HasPrefix(line, "#EXT-X-BYTERANGE") {
			r := parseByteRangeAttr(strings.TrimPrefix(line, "#EXT-X-BYTERANGE:"))
			pl.setPendingByteRange(r)
			continue
		}
		if strings.HasPrefix(line, "#EXTINF") {
			dur := parseExtInfDuration(line)
			for scanner.Scan() {
				next := strings.TrimSpace(scanner.Text())
				if next == "" || strings.HasPrefix(next, "#") {
					continue
				}
				seg := HLSSegment{
					URI:       next,
					Duration:  dur,
					Sequence:  pl.MediaSeq + int64(len(pl.Segments)),
					Key:       currentKey,
					ByteRange: pl.getPendingByteRange(),
				}
				pl.setPendingByteRange(HLSSegmentByteRange{})
				pl.Segments = append(pl.Segments, seg)
				break
			}
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("hls_direct: scan m3u8: %w", err)
	}
	return pl, nil
}

// parseExtInfDuration extracts the duration in seconds from an
// #EXTINF line.
func parseExtInfDuration(line string) time.Duration {
	line = strings.TrimPrefix(line, "#EXTINF:")
	if i := strings.Index(line, ","); i >= 0 {
		line = line[:i]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(line), 64)
	if err != nil || f < 0 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

// parseByteRangeAttr parses the value of an EXT-X-BYTERANGE
// directive, e.g. "1024@0" → Begin=0, End=1023.
func parseByteRangeAttr(s string) HLSSegmentByteRange {
	s = strings.TrimSpace(s)
	if s == "" {
		return HLSSegmentByteRange{}
	}
	parts := strings.SplitN(s, "@", 2)
	length, _ := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if length <= 0 {
		return HLSSegmentByteRange{}
	}
	begin := int64(0)
	if len(parts) == 2 {
		begin, _ = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	}
	return HLSSegmentByteRange{Begin: begin, End: begin + length - 1}
}

// attrString extracts a string attribute from an HLS tag.
func attrString(line, name string) (string, bool) {
	prefix := name + "="
	idx := strings.Index(line, prefix)
	if idx < 0 {
		return "", false
	}
	rest := line[idx+len(prefix):]
	if i := strings.Index(rest, ","); i >= 0 {
		rest = rest[:i]
	}
	rest = strings.TrimSpace(rest)
	rest = strings.Trim(rest, `"`)
	return rest, true
}

// attrInt extracts an integer attribute from an HLS tag.
func attrInt(line, name string) (int, bool) {
	s, ok := attrString(line, name)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}
