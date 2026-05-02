package mediaasset

import (
	"path/filepath"
	"strings"
)

func SafeName(name string) string {
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "-",
		"<", "-",
		">", "-",
		"|", "-",
	)
	return replacer.Replace(strings.TrimSpace(name))
}

func TmpPath(outputDir, filename string) string {
	return filepath.Join(outputDir, "tmp_"+filename)
}

func OutputPath(outputDir, filename string) string {
	return filepath.Join(outputDir, filename)
}
