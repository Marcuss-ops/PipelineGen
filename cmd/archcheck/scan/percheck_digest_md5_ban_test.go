package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeDigestMD5Fixture writes a .go file under root/<rel>.
func writeDigestMD5Fixture(t *testing.T, root, rel, content string) string {
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

func TestScanDigestMD5Ban_BannedImportViolates(t *testing.T) {
	root := t.TempDir()
	writeDigestMD5Fixture(t, root, "docs/migrations/digest-md5-imports-allowlist.txt", "# empty allowlist\n")
	writeDigestMD5Fixture(t, root, "internal/application/foo/service.go",
		"package foo\n\nimport (\n\t\"crypto/md5\"\n)\n\nvar _ = md5.New\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	if len(r.Violations) == 0 {
		t.Fatal("expected a violation for crypto/md5 import outside checksum")
	}
	found := false
	for _, v := range r.Violations {
		if v.Rule == digestMD5BanRule && v.File == "internal/application/foo/service.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected percheck_digest_md5_ban violation on the fixture, got %#v", r.Violations)
	}
}

func TestScanDigestMD5Ban_SSOTRootExempt(t *testing.T) {
	root := t.TempDir()
	writeDigestMD5Fixture(t, root, "docs/migrations/digest-md5-imports-allowlist.txt", "# empty allowlist\n")
	writeDigestMD5Fixture(t, root, "internal/platform/checksum/checksum.go",
		"package checksum\n\nimport \"crypto/md5\"\n\nvar _ = md5.New\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestMD5BanRule {
			t.Fatalf("internal/platform/checksum must be exempt, got violation %#v", v)
		}
	}
}

func TestScanDigestMD5Ban_AllowlistedFileExempt(t *testing.T) {
	root := t.TempDir()
	allowlisted := "internal/capabilities/overlays/model.go"
	alContent := "# allowlist\n" + allowlisted + "\n"
	writeDigestMD5Fixture(t, root, "docs/migrations/digest-md5-imports-allowlist.txt", alContent)
	writeDigestMD5Fixture(t, root, allowlisted,
		"package overlays\n\nimport \"crypto/md5\"\n\nvar _ = md5.New\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestMD5BanRule {
			t.Fatalf("allowlisted file must be exempt, got violation %#v", v)
		}
	}
}

func TestScanDigestMD5Ban_TestFileExempt(t *testing.T) {
	root := t.TempDir()
	writeDigestMD5Fixture(t, root, "docs/migrations/digest-md5-imports-allowlist.txt", "# empty allowlist\n")
	writeDigestMD5Fixture(t, root, "internal/application/foo/service_test.go",
		"package foo\n\nimport \"crypto/md5\"\n\nvar _ = md5.New\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestMD5BanRule {
			t.Fatalf("_test.go files must be exempt, got violation %#v", v)
		}
	}
}

func TestScanDigestMD5Ban_CommentOnlyWarns(t *testing.T) {
	root := t.TempDir()
	writeDigestMD5Fixture(t, root, "docs/migrations/digest-md5-imports-allowlist.txt", "# empty allowlist\n")
	writeDigestMD5Fixture(t, root, "internal/application/foo/service.go",
		"package foo\n\n// uses \"crypto/md5\" in prose only\nfunc F() {}\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestMD5BanRule {
			t.Fatalf("comment-only reference must not violate, got %#v", v)
		}
	}
	warned := false
	for _, w := range r.Warnings {
		if strings.Contains(w, digestMD5BanRule) {
			warned = true
			break
		}
	}
	if !warned {
		t.Fatal("comment-only reference must be residue-accounted as WARN")
	}
}

func TestScanDigestMD5Ban_MissingAllowlistFailsClosed(t *testing.T) {
	root := t.TempDir()
	// No allowlist file on disk.
	writeDigestMD5Fixture(t, root, "internal/application/foo/service.go",
		"package foo\n\nimport \"crypto/md5\"\n\nvar _ = md5.New\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.Rule == digestMD5AllowlistMissingRule {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing allowlist must fail closed with %s, got %#v", digestMD5AllowlistMissingRule, r.Violations)
	}
}

func TestScanDigestMD5Ban_StaleAllowlistEntryWarns(t *testing.T) {
	root := t.TempDir()
	stale := "internal/application/foo/migrated.go"
	alContent := "# allowlist\n" + stale + "\n"
	writeDigestMD5Fixture(t, root, "docs/migrations/digest-md5-imports-allowlist.txt", alContent)
	// The file no longer imports crypto/md5 (it was migrated).
	writeDigestMD5Fixture(t, root, stale, "package foo\n\nfunc F() {}\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	found := false
	for _, v := range r.Violations {
		if v.Rule == digestMD5AllowlistStaleRule {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stale allowlist entry must trip %s, got %#v", digestMD5AllowlistStaleRule, r.Violations)
	}
}

func TestScanDigestMD5Ban_OutOfScopeIgnored(t *testing.T) {
	root := t.TempDir()
	writeDigestMD5Fixture(t, root, "docs/migrations/digest-md5-imports-allowlist.txt", "# empty allowlist\n")
	writeDigestMD5Fixture(t, root, "docs/somepage.md", "import \"crypto/md5\"\n")
	writeDigestMD5Fixture(t, root, "examples/scratch/main.go", "package main\n\nimport \"crypto/md5\"\n\nvar _ = md5.New\n")

	r := &report.Report{}
	ScanDigestMD5Ban(root, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == digestMD5BanRule {
			t.Fatalf("out-of-scope files must be ignored, got %#v", v)
		}
	}
}
