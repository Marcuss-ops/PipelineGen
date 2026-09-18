package client

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// ollamaGenerationEndpoints are the paths that hand work to a model runner and
// therefore consume one of the server's OLLAMA_NUM_PARALLEL slots.
var ollamaGenerationEndpoints = []string{"/api/chat", "/api/generate", "/api/embeddings"}

// TestOnlyOllamaClientPackageTargetsGenerationEndpoints keeps the admission
// budget from being bypassable.
//
// The budget in admission.go works because every generation request in the
// process funnels through this package. A single package that dials
// `/api/chat` with its own http.Client would silently escape the ceiling and
// reintroduce the exact starvation this budget removes — the pools would look
// bounded while the server still saw an unbounded number of runners. Comments
// are ignored by parsing WITHOUT go/ast comments, so prose that names an
// endpoint (the SSOT notes in the enrichment adapter, for instance) is not a
// violation.
func TestOnlyOllamaClientPackageTargetsGenerationEndpoints(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// .../internal/platform/ollama/client/ollama_endpoint_guard_test.go → repo root
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename)))))

	// The canonical owner of the Ollama wire contract, plus its own tests.
	exemptPrefixes := []string{
		filepath.Join(root, "internal", "platform", "ollama"),
	}

	scanned, violations := scanDirectGenerationEndpointCalls(
		[]string{filepath.Join(root, "internal"), filepath.Join(root, "cmd")},
		exemptPrefixes,
		root,
	)

	// A guard that scanned nothing would pass vacuously; the repo has orders of
	// magnitude more Go files than this floor.
	if scanned < 100 {
		t.Fatalf("guard scanned only %d Go files; the repo root or walk is wrong", scanned)
	}
	if len(violations) > 0 {
		t.Fatalf("Ollama generation endpoints must only be called through internal/platform/ollama/client (the admission budget lives there), found %d bypass(es):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}

// TestOllamaEndpointGuardDetectsBypass proves the guard above can fail: a
// guard that cannot fail is not a guard.
func TestOllamaEndpointGuardDetectsBypass(t *testing.T) {
	base := t.TempDir()
	bypass := filepath.Join(base, "rogue.go")
	body := "package rogue\n\nconst endpoint = \"http://127.0.0.1:11434/api/chat\"\n"
	if err := os.WriteFile(bypass, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// The same prose in a COMMENT must stay legal: only real string literals
	// count, which is why the scan parses without comments.
	commentOnly := filepath.Join(base, "notes.go")
	commentBody := "package rogue\n\n// The canonical /api/generate wire contract lives elsewhere.\nconst ok = 1\n"
	if err := os.WriteFile(commentOnly, []byte(commentBody), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	scanned, violations := scanDirectGenerationEndpointCalls([]string{base}, nil, base)
	if scanned != 2 {
		t.Fatalf("scanned = %d, want 2", scanned)
	}
	if len(violations) != 1 {
		t.Fatalf("violations = %v, want exactly the rogue.go literal", violations)
	}
	if !strings.Contains(violations[0], "rogue.go") || !strings.Contains(violations[0], "/api/chat") {
		t.Fatalf("violation = %q, want it to name rogue.go and the endpoint", violations[0])
	}
}

// scanDirectGenerationEndpointCalls walks Go sources and reports string
// literals that name a model-runner endpoint, i.e. calls that would bypass the
// shared admission budget.
func scanDirectGenerationEndpointCalls(roots, exemptPrefixes []string, displayRoot string) (int, []string) {
	var (
		scanned    int
		violations []string
	)
	fset := token.NewFileSet()

	for _, base := range roots {
		_ = filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				name := entry.Name()
				if name == "node_modules" || name == ".git" || name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			for _, prefix := range exemptPrefixes {
				if strings.HasPrefix(path, prefix+string(os.PathSeparator)) {
					return nil
				}
			}

			file, parseErr := parser.ParseFile(fset, path, nil, 0) // no comments: prose is not a violation
			if parseErr != nil {
				return nil
			}
			scanned++
			rel, relErr := filepath.Rel(displayRoot, path)
			if relErr != nil {
				rel = path
			}

			ast.Inspect(file, func(node ast.Node) bool {
				literal, isStringLiteral := node.(*ast.BasicLit)
				if !isStringLiteral || literal.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr != nil {
					return true
				}
				for _, endpoint := range ollamaGenerationEndpoints {
					if strings.Contains(value, endpoint) {
						violations = append(violations, rel+":"+
							strconv.Itoa(fset.Position(literal.Pos()).Line)+
							" calls "+endpoint+" directly")
					}
				}
				return true
			})
			return nil
		})
	}

	return scanned, violations
}
