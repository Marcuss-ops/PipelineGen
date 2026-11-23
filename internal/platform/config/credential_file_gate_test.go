package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canonicalCredentialFile is assembled at runtime so this gate does not
// trip on its own source.
var canonicalCredentialFile = "/etc/pipelinegen/" + "pipelinegen.env"

// TestCredentialFileStaysBehindTheAuthWrapper pins the authentication SSOT
// (AGENTS.md, "Authentication SSOT (Velox admin token)"): the canonical
// credential file is loaded by scripts/with-velox-auth, never read or sourced
// by code, tests or operational scripts. A direct read is not a style issue —
// it copies the secret into a process that was never vetted for it, and it
// makes the rotation procedure (rewrite the file, restart the unit) silently
// ineffective for that reader.
//
// The scan covers Go, shell, Python, make and systemd units. Documentation and
// the repository-local `.env` loader are out of scope: prose may name the file,
// and scripts/lib/dotenv.sh is explicitly the *development* overlay.
func TestCredentialFileStaysBehindTheAuthWrapper(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	// Files whose whole purpose is to name the file: the loader itself, the
	// operator shell that forwards TOKEN_FILE, and the make variable that
	// exports it to child processes.
	allowed := map[string]bool{
		filepath.Join(root, "scripts", "with-velox-auth"):                                     true,
		filepath.Join(root, "scripts", "ops.sh"):                                              true,
		filepath.Join(root, "scripts", "lib", "dotenv.sh"):                                    true,
		filepath.Join(root, "make", "build.mk"):                                               true,
		filepath.Join(root, "internal", "platform", "config", "credential_file_gate_test.go"): true,
	}
	scanned := map[string]bool{".go": true, ".sh": true, ".mk": true, ".py": true, ".service": true, ".conf": true}
	skipDirs := map[string]bool{
		"docs": true, "third_party": true, "node_modules": true, "vendor": true, ".git": true, "data": true,
	}
	var offenses []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (skipDirs[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !scanned[filepath.Ext(path)] || allowed[path] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(data), canonicalCredentialFile) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relative = path
		}
		// systemd units declare the file as EnvironmentFile: that is the
		// sanctioned delivery mechanism, not a reader.
		if strings.HasPrefix(filepath.ToSlash(relative), "scripts/systemd/") &&
			(filepath.Ext(path) == ".service" || filepath.Ext(path) == ".conf") {
			return nil
		}
		offenses = append(offenses, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo for credential-file reads: %v", err)
	}
	if len(offenses) > 0 {
		t.Fatalf("direct reads of %s outside scripts/with-velox-auth: %v (load the token from the environment instead)", canonicalCredentialFile, offenses)
	}
}
