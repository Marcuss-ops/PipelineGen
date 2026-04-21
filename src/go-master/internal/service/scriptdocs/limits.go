package scriptdocs

import "strings"

func limitStringList(values []string, limit int) []string {
	if limit <= 0 || len(values) == 0 {
		return []string{}
	}
	out := make([]string, 0, limit)
	seen := make(map[string]bool)
	for _, v := range values {
		k := strings.ToLower(strings.TrimSpace(v))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(v))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func limitEntityImageMap(values map[string]string, limit int) map[string]string {
	out := make(map[string]string)
	if limit <= 0 || len(values) == 0 {
		return out
	}
	count := 0
	for k, v := range values {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
		count++
		if count >= limit {
			break
		}
	}
	return out
}

