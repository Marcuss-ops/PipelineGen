package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	configpkg "github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestScanVideoEncoderPolicy_HardcodedArgFails(t *testing.T) {
	root := t.TempDir()
	writeEncoderPolicyFixture(t, root, "internal/infrastructure/media/ffmpeg/bad.go", `package ffmpeg
func bad(args []string) []string { return append(args, "-c:v", "libx264") }
`)

	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)

	if len(r.Violations) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(r.Violations), r.Violations)
	}
	v := r.Violations[0]
	if v.Rule != videoEncoderPolicyRule || v.MatchedRule != videoEncoderPolicyMatchedRule {
		t.Fatalf("unexpected violation identity: %+v", v)
	}
	if !strings.Contains(v.Note, "encoder=libx264") {
		t.Fatalf("violation note = %q, want encoder literal", v.Note)
	}
}

func TestScanVideoEncoderPolicy_NonBuilderProductionLiteralFails(t *testing.T) {
	root := t.TempDir()
	writeEncoderPolicyFixture(t, root, "internal/application/video_defaults.go", `package application
const defaultCodec = "libx264"
`)

	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(r.Violations), r.Violations)
	}
	if r.Violations[0].File != "internal/application/video_defaults.go" {
		t.Fatalf("violation file = %q, want internal/application/video_defaults.go", r.Violations[0].File)
	}
}

func TestScanVideoEncoderPolicy_HardcodedOutsideMediaPackageFails(t *testing.T) {
	root := t.TempDir()
	writeEncoderPolicyFixture(t, root, "cmd/admin/bad.go", `package main
func bad() string { return "h264_nvenc" }
`)

	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("violations = %d, want 1: %+v", len(r.Violations), r.Violations)
	}
	if r.Violations[0].File != "cmd/admin/bad.go" {
		t.Fatalf("violation file = %q, want cmd/admin/bad.go", r.Violations[0].File)
	}
}

func TestScanVideoEncoderPolicy_HardcodedAssignmentAndComparisonFail(t *testing.T) {
	root := t.TempDir()
	writeEncoderPolicyFixture(t, root, "internal/infrastructure/media/ffmpeg/bad.go", `package ffmpeg
func bad(opts *Options) { opts.Codec = "libx264" }
func worse() string { codec := "h264_nvenc"; return codec }
func worst(codec string) bool { return codec == "libx265" }
`)

	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)
	if len(r.Violations) != 3 {
		t.Fatalf("violations = %d, want 3: %+v", len(r.Violations), r.Violations)
	}
}

func TestScanVideoEncoderPolicy_ProductionConfigIsGPUOriented(t *testing.T) {
	root := findVideoEncoderPolicyRepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "config.production.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg configpkg.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("production example YAML is invalid: %v", err)
	}
	if cfg.Video.Codec != "h264_nvenc" {
		t.Fatalf("production example codec = %q, want h264_nvenc", cfg.Video.Codec)
	}
	if cfg.Video.Preset != "p1" {
		t.Fatalf("production example preset = %q, want p1", cfg.Video.Preset)
	}
}

func TestScanVideoEncoderPolicy_PolicyInputOwnersPass(t *testing.T) {
	root := t.TempDir()
	writeEncoderPolicyFixture(t, root, "internal/platform/config/video.go", `package config
const defaultCodec = "libx264"
`)
	writeEncoderPolicyFixture(t, root, "internal/capabilities/assets/providers/stock/stockpipeline/service_types.go", `package stockpipeline
const defaultCodec = "libx264"
`)

	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("policy input owners must pass: %+v", r.Violations)
	}
}

func TestScanVideoEncoderPolicy_CanonicalResolverPasses(t *testing.T) {
	root := t.TempDir()
	writeEncoderPolicyFixture(t, root, "internal/infrastructure/media/ffmpeg/encoder_resolver.go", `package ffmpeg
const EncoderLibX264 = "libx264"
func args() []string { return []string{"-c:v", "libx264"} }
`)
	writeEncoderPolicyFixture(t, root, "cmd/archcheck/scan/self.go", `package scan
var probe = "-c:v", "h264_nvenc"
`)

	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("canonical files must pass: %+v", r.Violations)
	}
}

func TestScanVideoEncoderPolicy_TestFilesDoNotFail(t *testing.T) {
	root := t.TempDir()
	writeEncoderPolicyFixture(t, root, "internal/infrastructure/media/ffmpeg/fixture_test.go", `package ffmpeg
func testArgs() []string { return []string{"-c:v", "libx264"} }
`)

	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("test files must not fail: %+v", r.Violations)
	}
}

func TestScanVideoEncoderPolicy_RealRepositoryIsClean(t *testing.T) {
	root := findVideoEncoderPolicyRepoRoot(t)
	r := &report.Report{}
	ScanVideoEncoderPolicy(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("real FFmpeg paths contain hardcoded encoders: %+v", r.Violations)
	}
}

func findVideoEncoderPolicyRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := wd
	for root != filepath.Dir(root) {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
		root = filepath.Dir(root)
	}
	t.Fatalf("repository root not found from %s", wd)
	return ""
}

func writeEncoderPolicyFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
