package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ── Import-graph / token validation helpers ───────────────────────────

// extractGoPathTokens scans a scalar value for substrings that
// START with one of the canonical goPathPrefixes, extending
// greedily through path chars. Tokens are de-duplicated (a single
// scalar can mention the same path twice — we report it once).
func extractGoPathTokens(s string) []string {
	var tokens []string
	seen := map[string]bool{}
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		tokens = append(tokens, t)
	}

	for _, pref := range goPathPrefixes {
		from := 0
		for from < len(s) {
			idx := strings.Index(s[from:], pref)
			if idx < 0 {
				break
			}
			absStart := from + idx
			end := absStart + len(pref)
			for end < len(s) && isPathChar(s[end]) {
				end++
			}
			tok := trimTrailingPunctuation(s[absStart:end])
			// Drop single-char noise (e.g. `cmd/.` retrimmed
			// to `cmd/`) — a token must add at least one char
			// beyond the prefix.
			if len(tok) > len(pref) {
				add(tok)
			}
			if end > absStart+1 {
				from = end - 1
			} else {
				from = end
			}
		}
	}

	return tokens
}

// classifyToken picks one of the canonical Kind strings. The
// classifier is purely lexical (no IO) so callers can use it even
// when validateToken would otherwise side-effect (e.g. while
// previewing the kind of a token before running the validator).
func classifyToken(tok string) string {
	switch {
	case strings.HasPrefix(tok, "internal/") && strings.HasSuffix(tok, ".go"):
		return "internal_go_file"
	case strings.HasPrefix(tok, "internal/"):
		return "internal_pkg"
	case strings.HasPrefix(tok, "pkg/") && strings.HasSuffix(tok, ".go"):
		return "pkg_go_file"
	case strings.HasPrefix(tok, "pkg/"):
		return "pkg_pkg"
	case strings.HasPrefix(tok, "cmd/") && strings.HasSuffix(tok, ".go"):
		return "cmd_go_file"
	case strings.HasPrefix(tok, "cmd/"):
		return "cmd_binary"
	}
	return "go_file"
}

// validateToken returns "" when the token resolves cleanly, or a
// non-empty failure reason otherwise. The reason embeds the
// kind-specific IO failure mode for the operator (which file is
// missing, which go list module-path failed). The function is the
// single routing choke-point.
func validateToken(tok string) string {
	if strings.HasSuffix(tok, ".go") {
		if _, err := os.Stat(tok); err != nil {
			return fmt.Sprintf("missing .go file: %s", tok)
		}
		return ""
	}

	switch {
	case strings.HasPrefix(tok, "internal/"):
		if err := runGoList(modulePath + "/" + tok); err != nil {
			return fmt.Sprintf("go list failed: %s", err)
		}
		return ""
	case strings.HasPrefix(tok, "pkg/"):
		if err := runGoList("./" + tok); err != nil {
			return fmt.Sprintf("go list failed: %s", err)
		}
		return ""
	case strings.HasPrefix(tok, "cmd/"):
		main := filepath.Join(tok, "main.go")
		if _, err := os.Stat(main); err != nil {
			return fmt.Sprintf("missing cmd main: %s", main)
		}
		return ""
	}
	return "not a Go-like path"
}

// runGoList shells out to the Go toolchain to validate the package
// path. We use CombinedOutput so the caller sees both stderr and
// stdout as a single error-detail block (useful when a stale
// import path triggers the standard `go list` error chain with
// module-resolution hints).
func runGoList(pkg string) error {
	cmd := exec.Command("go", "list", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", pkg, strings.TrimSpace(string(out)))
	}
	return nil
}
