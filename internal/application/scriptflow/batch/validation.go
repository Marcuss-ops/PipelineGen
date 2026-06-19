package batch

import (
	"strings"
)

func SupportedScriptLanguages(cfgLangs []string, sourceLang string) map[string]struct{} {
	supported := make(map[string]struct{}, len(cfgLangs)+1)
	if sourceLang = strings.TrimSpace(strings.ToLower(sourceLang)); sourceLang != "" {
		supported[sourceLang] = struct{}{}
	}
	for _, lang := range cfgLangs {
		if lang = strings.TrimSpace(strings.ToLower(lang)); lang != "" {
			supported[lang] = struct{}{}
		}
	}
	return supported
}
