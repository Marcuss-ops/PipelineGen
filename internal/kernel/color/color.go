// Package color owns the canonical "#RRGGBB" spelling shared by overlay,
// cliprender, clipsequence and media-analysis payloads. Those payloads all
// declare the same seven-character form, so the acceptance rule lives here
// once instead of in one copy per validator.
package color

import "fmt"

// HexRGBLen is the byte length of a "#RRGGBB" value.
const HexRGBLen = 7

// IsHexRGB reports whether value is exactly "#RRGGBB" with six hexadecimal
// digits. Case is accepted on input and irrelevant to the comparison; callers
// that emit a value must still write the canonical lowercase form themselves.
func IsHexRGB(value string) bool {
	if len(value) != HexRGBLen || value[0] != '#' {
		return false
	}
	for i := 1; i < HexRGBLen; i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// ParseHexRGB decodes a "#RRGGBB" value into its three 0..255 components. The
// error names the rejected value so a payload validator can wrap it directly.
func ParseHexRGB(value string) (r, g, b int, err error) {
	if !IsHexRGB(value) {
		return 0, 0, 0, fmt.Errorf("invalid #RRGGBB colour %q", value)
	}
	return hexPair(value[1:3]), hexPair(value[3:5]), hexPair(value[5:7]), nil
}

func hexPair(pair string) int {
	high := hexDigit(pair[0])
	low := hexDigit(pair[1])
	return high<<4 | low
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default:
		return int(c-'A') + 10
	}
}
