// Package scan — percheck_brain_infra_ban_test.go
//
// Pins the brain/mediamemory infrastructure ban. Synthetic files only.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func containsWarning(haystacks []string, needle string) bool {
	for _, h := range haystacks {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

func makeBrainInfraFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanBrainInfraBan_BannedImportViolates(t *testing.T) {
	root := t.TempDir()
	makeBrainInfraFile(t, root, "internal/application/brain/core/core.go",
		`package core

import _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
`)

	r := &report.Report{}
	ScanBrainInfraBan(root, &policy.Policy{}, r)

	if len(r.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(r.Violations))
	}
	if r.Violations[0].Rule != brainInfraBanRule {
		t.Errorf("rule = %q, want %q", r.Violations[0].Rule, brainInfraBanRule)
	}
}

func TestScanBrainInfraBan_FFmpegLiteralViolates(t *testing.T) {
	root := t.TempDir()
	makeBrainInfraFile(t, root, "internal/application/mediamemory/scene_visual_plan_generator.go",
		`package mediamemory

import "os/exec"

func runFFmpeg() {
	_ = exec.Command("ffmpeg", "-i", "input")
}
`)

	r := &report.Report{}
	ScanBrainInfraBan(root, &policy.Policy{}, r)

	found := false
	for _, v := range r.Violations {
		if v.MatchedRule == "brain_infra_banned_call" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ffmpeg banned-call violation, got %+v", r.Violations)
	}
}

func TestScanBrainInfraBan_TestFilesExempt(t *testing.T) {
	root := t.TempDir()
	makeBrainInfraFile(t, root, "internal/application/brain/normalizer/normalizer_test.go",
		`package normalizer

import _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
`)

	r := &report.Report{}
	ScanBrainInfraBan(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		t.Errorf("_test.go must be exempt, got violation %+v", v)
	}
}

func TestScanBrainInfraBan_CommentOnlyWarns(t *testing.T) {
	root := t.TempDir()
	makeBrainInfraFile(t, root, "internal/application/brain/core/doc.go",
		`package core

// NOTE: the brain must not import
// "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite".
`)

	r := &report.Report{}
	ScanBrainInfraBan(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		t.Errorf("comment-only must not violate, got %+v", v)
	}
	if !containsWarning(r.Warnings, brainInfraBanRule) {
		t.Fatalf("expected comment-only warn for %s, got %v", brainInfraBanRule, r.Warnings)
	}
}

func TestScanBrainInfraBan_OtherPackageIgnored(t *testing.T) {
	root := t.TempDir()
	makeBrainInfraFile(t, root, "internal/application/assets/service.go",
		`package assets

import _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
`)

	r := &report.Report{}
	ScanBrainInfraBan(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if strings.Contains(v.File, "internal/application/assets/") {
			t.Errorf("assets package must be out of scope, got %+v", v)
		}
	}
}

func TestScanBrainInfraBan_BannedCallViolates(t *testing.T) {
	root := t.TempDir()
	makeBrainInfraFile(t, root, "internal/application/brain/core/core.go",
		`package core

func callQdrant() {
	_, _ = qdrant.NewClient(nil)
}
`)

	r := &report.Report{}
	ScanBrainInfraBan(root, &policy.Policy{}, r)

	found := false
	for _, v := range r.Violations {
		if v.MatchedRule == "brain_infra_banned_call" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected banned-call violation, got %+v", r.Violations)
	}
}

func TestScanBrainInfraBan_AdaptersSubdirExempt(t *testing.T) {
	root := t.TempDir()
	// The adapters/ subdir is the canonical deliberate infrastructure
	// bridge zone; a file there must remain exempt even if the real
	// qdrant adapter now lives in infrastructure/qdrant/qdrantmm.
	makeBrainInfraFile(t, root, "internal/application/mediamemory/adapters/generic_bridge.go",
		`package adapters

import _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
`)

	r := &report.Report{}
	ScanBrainInfraBan(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if strings.Contains(v.File, "internal/application/mediamemory/adapters/") {
			t.Errorf("adapters/ subdir must be exempt, got %+v", v)
		}
	}
}
