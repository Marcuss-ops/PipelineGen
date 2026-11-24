package jsonutil

// FindMapByKey returns the first nested map[string]any that carries key,
// searching depth-first through maps and slices. It is the shared resolver for
// envelope documents whose payload location is not fixed.
func FindMapByKey(v any, key string) (map[string]any, bool) {
	switch node := v.(type) {
	case map[string]any:
		if child, ok := node[key]; ok {
			if m, ok := child.(map[string]any); ok {
				return m, true
			}
		}
		for _, child := range node {
			if m, ok := FindMapByKey(child, key); ok {
				return m, true
			}
		}
	case []any:
		for _, child := range node {
			if m, ok := FindMapByKey(child, key); ok {
				return m, true
			}
		}
	}
	return nil, false
}
