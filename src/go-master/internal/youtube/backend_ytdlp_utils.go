package youtube

import (
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func mapSearchSortForYtDlp(sortBy string) string {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "views", "view", "view_count":
		return "view_count"
	case "date", "upload_date", "newest":
		return "upload_date"
	case "rating":
		return "rating"
	default:
		return "relevance"
	}
}

func sortSearchResults(results []SearchResult, sortBy string) {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "views", "view", "view_count":
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].Views > results[j].Views
		})
	case "date", "upload_date", "newest":
		sort.SliceStable(results, func(i, j int) bool {
			return results[i].UploadDate > results[j].UploadDate
		})
	}
}

func mapUploadDateForYtDlp(uploadDate string) string {
	now := time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(uploadDate)) {
	case "hour":
		return now.Add(-1 * time.Hour).Format("20060102")
	case "today":
		return now.Add(-24 * time.Hour).Format("20060102")
	case "week":
		return now.Add(-7 * 24 * time.Hour).Format("20060102")
	case "month":
		return now.Add(-30 * 24 * time.Hour).Format("20060102")
	case "year":
		return now.Add(-365 * 24 * time.Hour).Format("20060102")
	default:
		return ""
	}
}

func mapDurationForYtDlp(duration string) string {
	switch strings.ToLower(strings.TrimSpace(duration)) {
	case "short":
		return "duration < 240"
	case "medium":
		return "duration >= 240 & duration <= 1200"
	case "long":
		return "duration > 1200"
	default:
		return ""
	}
}

func extractVideoID(url string) string {
	// Handle various YouTube URL formats
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?:v=|/v/|/embed/|youtu\.be/)([a-zA-Z0-9_-]{11})`),
		regexp.MustCompile(`youtube\.com/watch\?v=([a-zA-Z0-9_-]{11})`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(url)
		if len(matches) > 1 {
			return matches[1]
		}
	}

	return ""
}

func findDownloadedFiles(pattern string) ([]string, error) {
	// Try common extensions
	extensions := []string{".mp4", ".webm", ".mkv", ".m4a", ".mp3", ".opus"}
	var files []string

	for _, ext := range extensions {
		basePath := strings.TrimSuffix(pattern, ".%(ext)s")
		filePath := basePath + ext
		// Use os.Stat() to check if file exists on filesystem (not PATH lookup)
		if info, err := os.Stat(filePath); err == nil && !info.IsDir() {
			files = append(files, filePath)
		}
	}

	return files, nil
}

func getFileSize(path string) int64 {
	info, err := exec.Command("stat", "-c", "%s", path).Output()
	if err != nil {
		return 0
	}

	size, _ := strconv.ParseInt(strings.TrimSpace(string(info)), 10, 64)
	return size
}

func readFile(path string) ([]byte, error) {
	return exec.Command("cat", path).Output()
}
