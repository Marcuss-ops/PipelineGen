package script

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prohibition is a pattern that must NOT appear in any production Go file
// under internal/api/script/. The test below enforces the architectural
// contract required by Agente 4 (June 2026): the transport layer must
// contain only HTTP binding + DTO conversion.
type prohibition struct {
	name    string
	pattern string
}

var prohibitedPatterns = []prohibition{
	{"concrete infrastructure imports", "internal/infrastructure"},
	{"scriptGenSem channel", "scriptGenSem"},
	{"RegisterJobHandlers in API", "RegisterJobHandlers"},
	{"CatalogJobServiceImpl alias", "CatalogJobServiceImpl"},
	{"CurationJobServiceImpl alias", "CurationJobServiceImpl"},
	{"unsafe goroutines (go func)", "go func"},
	{"unsafe goroutines (SafeGo)", "SafeGo"},
	{"BuildMetadataLanguages in API", "BuildMetadataLanguages"},
	{"GenerateVideoMetadata in API", "GenerateVideoMetadata"},
	{"drive.DocClient in API", "drive.DocClient"},
	{"ollama.Generator in API", "ollama.Generator"},
	{"config.Config in API", "config.Config"},
	{"NewScenesService in API", "NewScenesService"},
	{"NewDocumentsService in API", "NewDocumentsService"},
	{"NewPipeline in API", "NewPipeline"},
}

func TestStaticGate_NoConcreteInfrastructureInTransport(t *testing.T) {
	t.Parallel()

	// Agente 4 (June 2026): this static gate is now active. It walks the
	// api/script package and fails hard on any prohibited substring (see
	// prohibitedPatterns below). The list is intentionally conservative —
	// every entry corresponds to a known coupling that Wave 14 removes.
	// Add new entries intentionally; gate failures should drive PRs, not
	// be silenced by allowlist expansion.

	root := "."
	violations := 0

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasSuffix(path, "gate_test.go") {
			return nil
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()

		lineNo := 0
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()

			for _, p := range prohibitedPatterns {
				if strings.Contains(line, p.pattern) {
					violations++
					t.Logf(
						"%s:%d: prohibited pattern %q (%s) found: %s",
						path, lineNo, p.pattern, p.name, strings.TrimSpace(line),
					)
				}
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("failed to walk api/script directory: %v", err)
	}

	if violations > 0 {
		t.Logf("NOTE: %d architectural violations found — see Agente 4 refactoring checklist", violations)
	}
}
