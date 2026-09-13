// Package scan — companion test for percheck_media_txn_boundary.go.
//
// Pins:
//
//	(a) "violation trip" — a transaction-bound media mutation method
//	    declared outside internal/platform/postgres/media/ emits a
//	    violation.
//	(b) "canonical owner exempt" — the same declaration inside the
//	    canonical PostgreSQL media package is NOT a violation.
//	(c) "comment-only is ignored" — a comment referencing the demolished
//	    names does not trip the gate.
//	(d) "test file exempt" — a *_test.go file is not scanned.
//	(e) "boundary must stay tx-free" — re-adding `*sql.Tx` to the
//	    CanonicalAssetWriter interface trips the gate.
package boundaries

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func TestScanMediaTxBoundary_ViolationTrip(t *testing.T) {
	tmp := t.TempDir()
	fakeDir := filepath.Join(tmp, "internal", "capabilities", "somefeature")
	if err := os.MkdirAll(fakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package somefeature

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func (r *Repo) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	return nil
}
`
	if err := os.WriteFile(filepath.Join(fakeDir, "writer.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaTxBoundary(tmp, &policy.Policy{}, r)

	found := false
	for _, v := range r.Violations {
		if v.Rule == mediaTxnBoundaryRule && v.MatchedRule == "tx_bound_media_method" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a %s violation for UpsertClipTx outside the canonical media package, got %+v", mediaTxnBoundaryRule, r.Violations)
	}
}

func TestScanMediaTxBoundary_CanonicalOwnerExempt(t *testing.T) {
	tmp := t.TempDir()
	ownerDir := filepath.Join(tmp, "internal", "platform", "postgres", "media")
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package media

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func (c *Committer) setIndexStateTx(ctx context.Context, tx *sql.Tx, assetID string, state asset.IndexState) error {
	return nil
}

func (c *Committer) UpsertClipTx(ctx context.Context, tx *sql.Tx, clip *asset.Asset) error {
	return nil
}
`
	if err := os.WriteFile(filepath.Join(ownerDir, "writer.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaTxBoundary(tmp, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == mediaTxnBoundaryRule && v.MatchedRule == "tx_bound_media_method" {
			t.Errorf("canonical media package must be exempt, got: %+v", v)
		}
	}
}

func TestScanMediaTxBoundary_CommentOnlyIgnored(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "internal", "capabilities", "somefeature")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package somefeature

// The demolished UpsertClipTx / SetIndexStateTx seam is gone.
func helper() {}
`
	if err := os.WriteFile(filepath.Join(dir, "doc.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaTxBoundary(tmp, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == mediaTxnBoundaryRule && v.MatchedRule == "tx_bound_media_method" {
			t.Errorf("comment-only reference must not trip the gate, got: %+v", v)
		}
	}
}

func TestScanMediaTxBoundary_TestFileExempt(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "internal", "capabilities", "somefeature")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package somefeature

import "testing"

func TestX(t *testing.T) {
	_ = "UpsertClipTx"
	_ = "SetIndexStateTx"
}
`
	if err := os.WriteFile(filepath.Join(dir, "writer_test.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaTxBoundary(tmp, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == mediaTxnBoundaryRule {
			t.Errorf("test file must be exempt, got: %+v", v)
		}
	}
}

func TestScanMediaTxBoundary_CanonicalBoundaryMustStayTxFree(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "internal", "capabilities", "assets", "persistence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package persistence

import "database/sql"

type AssetCommitter interface {
	CommitAndIndex(ctx context.Context, req CommitRequest) (CommitResult, error)
}

type CanonicalAssetWriter interface {
	AssetCommitter
	UpsertClipTx(ctx interface{}, tx *sql.Tx, assetID string) error
}
`
	if err := os.WriteFile(filepath.Join(dir, "mutator.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaTxBoundary(tmp, &policy.Policy{}, r)

	found := false
	for _, v := range r.Violations {
		if v.Rule == mediaTxnBoundaryRule && v.MatchedRule == "tx_bound_canonical_boundary" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a tx_bound_canonical_boundary violation when CanonicalAssetWriter names *sql.Tx, got %+v", r.Violations)
	}
}

func TestScanMediaTxBoundary_TxFreeBoundaryPasses(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "internal", "capabilities", "assets", "persistence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `package persistence

type AssetCommitter interface {
	CommitAndIndex(ctx context.Context, req CommitRequest) (CommitResult, error)
}

type AssetMutator interface {
	PatchAsset(ctx context.Context, patch AssetPatch) error
}

type CanonicalAssetWriter interface {
	AssetCommitter
	AssetMutator
}
`
	if err := os.WriteFile(filepath.Join(dir, "mutator.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &report.Report{}
	ScanMediaTxBoundary(tmp, &policy.Policy{}, r)
	for _, v := range r.Violations {
		if v.Rule == mediaTxnBoundaryRule {
			t.Errorf("a tx-free CanonicalAssetWriter must pass, got: %+v", v)
		}
	}
}

func TestInterfaceBody_ExtractsBracedBody(t *testing.T) {
	source := `type CanonicalAssetWriter interface {
	AssetCommitter
	UpsertClipTx(ctx context.Context, tx *sql.Tx) error
}

type Other struct{}
`
	body, start := interfaceBody(source, "CanonicalAssetWriter")
	if body == "" {
		t.Fatal("interfaceBody returned empty body")
	}
	if !strings.Contains(body, "*sql.Tx") {
		t.Errorf("body must contain the tx parameter, got %q", body)
	}
	if start <= 0 {
		t.Errorf("start offset must point at the declaration, got %d", start)
	}
}
