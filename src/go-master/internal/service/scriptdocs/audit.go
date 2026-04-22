package scriptdocs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ScriptDocAudit stores the full technical trace for a generated document.
type ScriptDocAudit struct {
	GeneratedAt string           `json:"generated_at"`
	Request     ScriptDocRequest `json:"request"`
	Result      *ScriptDocResult `json:"result"`
}

func saveScriptDocAuditJSON(req ScriptDocRequest, result *ScriptDocResult) (string, error) {
	if result == nil {
		return "", nil
	}
	dir := filepath.Join(os.TempDir(), "velox-scriptdoc-audits")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%s_%d.json", safeFileName(req.Topic), time.Now().Unix()))
	payload := ScriptDocAudit{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Request:     req,
		Result:      result,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}
