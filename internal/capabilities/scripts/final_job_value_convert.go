package scriptgeneration

import (
	"encoding/json"
	"fmt"
	"strings"
)

func finalJobInt64(value any) int64 {
	switch n := value.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var i int64
		_, _ = fmt.Sscan(strings.TrimSpace(n), &i)
		return i
	default:
		return 0
	}
}

func finalJobInt(value any) int {
	return int(finalJobInt64(value))
}
