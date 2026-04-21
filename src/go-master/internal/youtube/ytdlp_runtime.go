package youtube

import (
	"os"
	"os/exec"
	"strings"
	"time"
)

// BuildYtDlpAuthArgs returns auth-related args from request/config/env.
func BuildYtDlpAuthArgs(explicitCookiesFile string, defaultCookiesFile string) []string {
	args := make([]string, 0, 8)

	cookiesFile := strings.TrimSpace(explicitCookiesFile)
	if cookiesFile == "" {
		cookiesFile = strings.TrimSpace(defaultCookiesFile)
	}
	if cookiesFile == "" {
		cookiesFile = firstNonEmptyEnv("VELOX_YTDLP_COOKIES_FILE", "YTDLP_COOKIES_FILE")
	}
	if cookiesFile == "" {
		cookiesFile = detectLocalCookiesFile()
	}
	if cookiesFile != "" {
		args = append(args, "--cookies", cookiesFile)
	}

	cookiesFromBrowser := firstNonEmptyEnv("VELOX_YTDLP_COOKIES_FROM_BROWSER", "YTDLP_COOKIES_FROM_BROWSER")
	if cookiesFromBrowser != "" {
		args = append(args, "--cookies-from-browser", cookiesFromBrowser)
	}

	jsRuntimes := firstNonEmptyEnv("VELOX_YTDLP_JS_RUNTIMES", "YTDLP_JS_RUNTIMES")
	if jsRuntimes != "" {
		args = append(args, "--js-runtimes", jsRuntimes)
	} else if hasCommand("node") {
		// Default to node so yt-dlp can solve JS challenges.
		args = append(args, "--js-runtimes", "node")
	}

	remoteComponents := firstNonEmptyEnv("VELOX_YTDLP_REMOTE_COMPONENTS", "YTDLP_REMOTE_COMPONENTS")
	if remoteComponents != "" {
		args = append(args, "--remote-components", remoteComponents)
	} else {
		// Needed by yt-dlp to fetch challenge solver lib (EJS).
		args = append(args, "--remote-components", "ejs:github")
	}

	return args
}

// YouTubeExtractorArgsVariants returns extractor-args strategies in priority order.
func YouTubeExtractorArgsVariants() []string {
	custom := firstNonEmptyEnv("VELOX_YTDLP_EXTRACTOR_ARGS", "YTDLP_EXTRACTOR_ARGS")
	if custom != "" {
		return uniqueNonEmpty([]string{
			custom,
			"youtube:player_client=mweb,android,web_embedded,tv_embedded,tv",
			"youtube:player_client=android,mweb,web_embedded,tv_embedded,tv",
			"youtube:player_client=web,android,mweb,tv_embedded,tv",
			"youtube:player_client=mweb,tv_embedded,web_embedded,tv",
			"youtube:player_client=tv_embedded,web_embedded,tv",
		})
	}
	return []string{
		"youtube:player_client=mweb,android,web_embedded,tv_embedded,tv",
		"youtube:player_client=android,mweb,web_embedded,tv_embedded,tv",
		"youtube:player_client=web,android,mweb,tv_embedded,tv",
		"youtube:player_client=mweb,tv_embedded,web_embedded,tv",
		"youtube:player_client=tv_embedded,web_embedded,tv",
		"",
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func detectLocalCookiesFile() string {
	candidates := detectLocalCookiesFiles()
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

func detectLocalCookiesFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	candidates := []string{
		"cookineronees.txt",
		home + "/Downloads/cookineronees.txt",
		home + "/Downloads/coo1kies.txt",
		home + "/Downloads/cookies.txt",
		home + "/cookies.txt",
	}
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			out = append(out, candidate)
		}
	}
	return uniqueNonEmpty(out)
}

// collectCookiesCandidates returns ordered cookie files to try for auth rotation.
func collectCookiesCandidates(explicitCookiesFile string, defaultCookiesFile string) []string {
	candidates := []string{
		strings.TrimSpace(explicitCookiesFile),
		strings.TrimSpace(defaultCookiesFile),
		firstNonEmptyEnv("VELOX_YTDLP_COOKIES_FILE", "YTDLP_COOKIES_FILE"),
	}
	candidates = append(candidates, detectLocalCookiesFiles()...)
	return uniqueNonEmpty(candidates)
}

func cookieAge(path string) time.Duration {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return time.Since(info.ModTime())
}
