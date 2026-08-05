package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan"
)

// TestReportContract replaces the stale full-repository golden snapshot with
// deterministic schema and source-identity assertions. The complete report is
// an operational CI artifact; this test keeps only the load-bearing contract in
// the repository.
func TestReportContract(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(pkgDir))
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "archcheck_report_contract_test")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = pkgDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	first := runArchcheckForReport(t, binPath, projectRoot)
	second := runArchcheckForReport(t, binPath, projectRoot)
	if string(first) != string(second) {
		t.Fatalf("report output is not deterministic\n%s", firstNLines(string(first), 40))
	}

	var got struct {
		GitCommitSHA string `json:"git_commit_sha"`
		Policy       struct {
			MaxClipIngestPipelineFields int `json:"MaxClipIngestPipelineFields"`
		} `json:"policy_snapshot"`
		Violations []struct {
			Rule string `json:"rule"`
		} `json:"violations"`
	}
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}

	headCmd := exec.Command("git", "rev-parse", "HEAD")
	headCmd.Dir = projectRoot
	headOut, err := headCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	wantSHA := strings.TrimSpace(string(headOut))
	if got.GitCommitSHA != wantSHA {
		t.Fatalf("git_commit_sha=%q, want HEAD %q", got.GitCommitSHA, wantSHA)
	}
	if got.Policy.MaxClipIngestPipelineFields != 9 {
		t.Fatalf("policy_snapshot.MaxClipIngestPipelineFields=%d, want 9", got.Policy.MaxClipIngestPipelineFields)
	}
	for _, violation := range got.Violations {
		if strings.HasSuffix(violation.Rule, "_doc_missing") || strings.HasSuffix(violation.Rule, "_doc_incomplete") {
			t.Errorf("canonical architecture document gate is not green: %s", violation.Rule)
		}
	}
}

func runArchcheckForReport(t *testing.T, binPath, projectRoot string) []byte {
	t.Helper()
	cmd := exec.Command(binPath,
		"--root=.",
		"--policy=architecture/policy.yaml",
		"--phase=0",
	)
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err == nil {
		return out
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("archcheck run failed: %v\nstdout:\n%s", err, out)
	}
	// Exit 1 is an expected report state while unrelated active violations
	// remain. Exit 2 (load/parse failure) is never accepted.
	if exitErr.ExitCode() != ExitViolations {
		t.Fatalf("archcheck exit=%d, want 0 or %d", exitErr.ExitCode(), ExitViolations)
	}
	return out
}

func TestKernelSubzoneHardGatesFailClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "kernel", "observability"), 0o755); err != nil {
		t.Fatalf("mkdir kernel fixture: %v", err)
	}
	policyPath := filepath.Join(root, "policy.yaml")
	policyText := "kernel_subzones: asset\n" +
		"hard_gates:\n" +
		"  - kernel_subzone_undeclared\n" +
		"  - kernel_subzone_missing\n"
	if err := os.WriteFile(policyPath, []byte(policyText), 0o644); err != nil {
		t.Fatalf("write policy fixture: %v", err)
	}

	pol, err := policy.Load(policyPath)
	if err != nil {
		t.Fatalf("load policy fixture: %v", err)
	}
	r := &report.Report{}
	scan.ScanKernelSubzoneIntegrity(root, pol, r)
	foundRule := false
	for _, violation := range r.Violations {
		if violation.Rule == "kernel_subzone_undeclared" || violation.Rule == "kernel_subzone_missing" {
			foundRule = true
			break
		}
	}
	if !foundRule {
		t.Fatalf("fixture must emit a kernel integrity rule, got %#v", r.Violations)
	}

	if code, err := Run(context.Background(), root, policyPath, "test", false, false); err != nil || code != ExitViolations {
		t.Fatalf("kernel integrity hard gates must fail closed: code=%d err=%v", code, err)
	}
}

func TestProjectRootContainsPolicyAndCatalog(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	projectRoot := filepath.Dir(filepath.Dir(pkgDir))
	for _, rel := range []string{"architecture/policy.yaml", "architecture/catalog.yaml"} {
		path := filepath.Join(projectRoot, filepath.FromSlash(rel))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to exist: %v", path, err)
		}
	}
}

func firstNLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n... [%d more lines truncated]", len(lines)-n)
}
