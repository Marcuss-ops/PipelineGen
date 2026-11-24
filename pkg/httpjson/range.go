package httpjson

import (
	"strconv"
	"strings"
)

// ParseByteRange parses the single-range form used by resumable clients:
// "bytes=start-", "bytes=start-end", or "bytes=-suffix". Multi-range
// requests are rejected because the download is a single media stream.
func ParseByteRange(raw string, size int64) (start, end int64, ok bool) {
	if size <= 0 || !strings.HasPrefix(raw, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimSpace(strings.TrimPrefix(raw, "bytes="))
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	startPart := strings.TrimSpace(parts[0])
	endPart := strings.TrimSpace(parts[1])
	if startPart == "" {
		suffix, err := strconv.ParseInt(endPart, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true
	}
	start, err := strconv.ParseInt(startPart, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end = size - 1
	if endPart != "" {
		parsed, err := strconv.ParseInt(endPart, 10, 64)
		if err != nil || parsed < start {
			return 0, 0, false
		}
		if parsed < end {
			end = parsed
		}
	}
	return start, end, true
}
