package scheduling

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadConfig reads process environment first, then the protected .env created
// for the external worker integration. Secret values are never logged.
func LoadConfig() (baseURL, apiKey string) {
	baseURL = os.Getenv("INSTAEDIT_BASE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("INSTAEDIT_URL")
	}
	apiKey = os.Getenv("INSTAEDIT_API_KEY")
	if baseURL != "" && apiKey != "" {
		return
	}
	home, _ := os.UserHomeDir()
	candidates := []string{os.Getenv("INSTAEDIT_CALENDAR_ENV_FILE"), filepath.Join(home, "src/go-master/projects/Pyt/VeloxEditing/InstaeditScheduling/.env"), filepath.Join("..", "InstaeditScheduling", ".env")}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), "\"' ")
			switch strings.TrimSpace(key) {
			case "INSTAEDIT_BASE_URL", "INSTAEDIT_URL":
				if baseURL == "" {
					baseURL = value
				}
			case "INSTAEDIT_API_KEY":
				if apiKey == "" {
					apiKey = value
				}
			}
		}
		_ = f.Close()
		if baseURL != "" && apiKey != "" {
			return
		}
	}
	return
}
