package finaljob

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Int64 is the single coercion used by every final-job transport field. The
// payload is decoded from JSON in several places (map[string]any, json.Number,
// typed structs), so the boundary accepts all the numeric representations the
// Master contract can produce.
func Int64(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case uint:
		return int64(n)
	case uint32:
		return int64(n)
	case uint64:
		return int64(n)
	case float32:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		trimmed := strings.TrimSpace(n)
		if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return i
		}
		var i int64
		_, _ = fmt.Sscan(trimmed, &i)
		return i
	default:
		return 0
	}
}

// String is the single string coercion. nil and the fmt "<nil>" sentinel map
// to the empty string; every other value keeps its trimmed textual form.
func String(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

// Bool is the single boolean coercion. The wire normally carries a JSON bool;
// string forms are accepted for the legacy map payloads without changing the
// meaning of a real bool.
func Bool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, _ := strconv.ParseBool(strings.TrimSpace(v))
		return parsed
	default:
		return false
	}
}
