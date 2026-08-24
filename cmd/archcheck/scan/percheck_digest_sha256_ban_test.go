package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeDigestSHA256Fixture writes a .go file under root/<rel> and returns
// the repo-relative slash path.
func writeDigestSHA256Fixture(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return filepath.ToSlash(rel)
}

// digestSHA256AllowlistContent is a minimal allowlist for fixtures that
// need one.
func digestSHA256AllowlistContent(entries ...string) string {
	var sb strings.Builder
	sb.WriteString("# fixture allowlist\n")
	for _, e := range entries {
		sb.WriteString(e + "  # owner: test; deadline: 2026-12-31\n")
	}
	return sb.String()
}

func TestScanDigestSHA256Ban_BannedImportViolates(t *testing.T) {
	root := t.TempDir()
	writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt", digestSHA256AllowlistContent())
	writeDigestSHA256Fixture(t, root, "internal/application/foo/service.go",
		"package foo\n\nimport (\n\t\"crypto/sha256\"\n)\n\nvar _ = sha256.New\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	if len(r.Violations) == 0 {
		t.Fatal("expected a violation for crypto/sha256 import outside digest")
	}
	found := false
	for _, v := range r.Violations {
		if v.Rule == digestSHA256BanRule && v.File == "internal/application/foo/service.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected percheck_digest_sha256_ban violation on the fixture, got %#v", r.Violations)
	}
}

func TestScanDigestSHA256Ban_SSOTRootExempt(t *testing.T) {
	root := t.TempDir()
	writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt", digestSHA256AllowlistContent())
	writeDigestSHA256Fixture(t, root, "internal/kernel/digest/sha256.go",
		"package digest\n\nimport \"crypto/sha256\"\n\nvar _ = sha256.New\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestSHA256BanRule {
			t.Fatalf("internal/kernel/digest must be exempt, got violation %#v", v)
		}
	}
}

func TestScanDigestSHA256Ban_TLSProtocolExempt(t *testing.T) {
	cases := []string{
		"pkg/hmacsign/signer.go",
		"pkg/tlsload/tlsload.go",
		"internal/platform/delivery/signer.go",
	}
	for _, rel := range cases {
		root := t.TempDir()
		writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt", digestSHA256AllowlistContent())
		writeDigestSHA256Fixture(t, root, rel,
			"package p\n\nimport \"crypto/sha256\"\n\nvar _ = sha256.New\n")
		r := &report.Report{}
		ScanDigestSHA256Ban(root, &policy.Policy{}, r)
		for _, v := range r.Violations {
			if v.Rule == digestSHA256BanRule {
				t.Fatalf("%s is a permanent TLS/protocol exemption, got violation %#v", rel, v)
			}
		}
	}
}

func TestScanDigestSHA256Ban_AllowlistedFileExempt(t *testing.T) {
	root := t.TempDir()
	allowlisted := "internal/capabilities/overlays/model.go"
	writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt",
		digestSHA256AllowlistContent(allowlisted))
	writeDigestSHA256Fixture(t, root, allowlisted,
		"package overlays\n\nimport \"crypto/sha256\"\n\nvar _ = sha256.New\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestSHA256BanRule {
			t.Fatalf("allowlisted file must be exempt, got violation %#v", v)
		}
	}
}

func TestScanDigestSHA256Ban_TestFileExempt(t *testing.T) {
	root := t.TempDir()
	writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt", digestSHA256AllowlistContent())
	writeDigestSHA256Fixture(t, root, "internal/application/foo/service_test.go",
		"package foo\n\nimport \"crypto/sha256\"\n\nvar _ = sha256.New\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestSHA256BanRule {
			t.Fatalf("_test.go files must be exempt, got violation %#v", v)
		}
	}
}

func TestScanDigestSHA256Ban_CommentOnlyWarns(t *testing.T) {
	root := t.TempDir()
	writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt", digestSHA256AllowlistContent())
	writeDigestSHA256Fixture(t, root, "internal/application/foo/service.go",
		"package foo\n\n// uses \"crypto/sha256\" in prose only\nfunc F() {}\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestSHA256BanRule {
			t.Fatalf("comment-only reference must not violate, got %#v", v)
		}
	}
	warned := false
	for _, w := range r.Warnings {
		if strings.Contains(w, digestSHA256BanRule) {
			warned = true
			break
		}
	}
	if !warned {
		t.Fatal("comment-only reference must be residue-accounted as WARN")
	}
}

func TestScanDigestSHA256Ban_MissingAllowlistFailsClosed(t *testing.T) {
	root := t.TempDir()
	// No allowlist file on disk.
	writeDigestSHA256Fixture(t, root, "internal/application/foo/service.go",
		"package foo\n\nimport \"crypto/sha256\"\n\nvar _ = sha256.New\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.Rule == digestSHA256AllowlistMissingRule {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing allowlist must fail closed with %s, got %#v", digestSHA256AllowlistMissingRule, r.Violations)
	}
}

func TestScanDigestSHA256Ban_StaleAllowlistEntryWarns(t *testing.T) {
	root := t.TempDir()
	stale := "internal/application/foo/migrated.go"
	writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt",
		digestSHA256AllowlistContent(stale))
	// The file no longer imports crypto/sha256 (it was migrated).
	writeDigestSHA256Fixture(t, root, stale, "package foo\n\nfunc F() {}\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.Rule == digestSHA256AllowlistStaleRule {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stale allowlist entry must trip %s, got %#v", digestSHA256AllowlistStaleRule, r.Violations)
	}
}

func TestScanDigestSHA256Ban_OutOfScopeIgnored(t *testing.T) {
	root := t.TempDir()
	writeDigestSHA256Fixture(t, root, "docs/migrations/digest-sha256-imports-allowlist.txt", digestSHA256AllowlistContent())
	writeDigestSHA256Fixture(t, root, "docs/somepage.md", "import \"crypto/sha256\"\n")
	writeDigestSHA256Fixture(t, root, "examples/scratch/main.go", "package main\n\nimport \"crypto/sha256\"\n\nvar _ = sha256.New\n")

	r := &report.Report{}
	ScanDigestSHA256Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestSHA256BanRule {
			t.Fatalf("out-of-scope files must be ignored, got %#v", v)
		}
	}
}
