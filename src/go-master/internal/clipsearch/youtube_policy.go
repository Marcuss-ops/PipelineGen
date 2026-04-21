package clipsearch

import "strings"

func shouldPreferYouTubeKeyword(keyword string) bool {
	k := strings.ToLower(strings.TrimSpace(keyword))
	if k == "" {
		return false
	}
	markers := []string{
		"interview", "intervista", "podcast", "press conference",
		"post fight", "conference", "speech", "talk",
	}
	for _, m := range markers {
		if strings.Contains(k, m) {
			return true
		}
	}
	return false
}
