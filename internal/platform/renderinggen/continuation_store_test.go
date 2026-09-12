package renderinggen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	artifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
)

// newContinuationStoreFixture builds the REAL CAS (temp root) with the REAL
// LocalStore stager, exactly as production composition wires it, so the test
// exercises the adapter against the canonical store rather than a double.
func newContinuationStoreFixture(t *testing.T) *CASContinuationStore {
	t.Helper()
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	stager, err := artifacts.NewLocalStore(artifacts.Config{Workspace: workspace, MinFreeBytes: 1})
	if err != nil {
		t.Fatalf("build LocalStore stager: %v", err)
	}
	store, err := cas.NewStore(cas.Config{Root: filepath.Join(t.TempDir(), "cas"), Stager: stager})
	if err != nil {
		t.Fatalf("cas.NewStore: %v", err)
	}
	adapter, err := NewCASContinuationStore(store)
	if err != nil {
		t.Fatalf("NewCASContinuationStore: %v", err)
	}
	return adapter
}

func resumeDocumentFixture(t *testing.T) cliprender.ResumeDocument {
	t.Helper()
	return cliprender.ResumeDocument{
		Plan:            validClipPlan(t),
		Request:         cliprender.RenderRequest{SourceAssetID: "source-asset-001"},
		PublishFolderID: "drive-folder-001",
		Subtitles:       nil,
	}
}

// TestCASContinuationStoreRoundTrips certifies the store half of the boundary:
// the resume document survives a put/get cycle through the canonical CAS and
// the settle phase can verify it against the address it was handed.
func TestCASContinuationStoreRoundTrips(t *testing.T) {
	store := newContinuationStoreFixture(t)
	doc := resumeDocumentFixture(t)

	ref, err := store.PutResumeDocument(context.Background(), doc)
	if err != nil {
		t.Fatalf("PutResumeDocument: %v", err)
	}
	if len(ref.SHA256) != 64 || ref.SizeBytes <= 0 {
		t.Fatalf("address = %+v, want a 64-hex digest and a positive size", ref)
	}

	got, err := store.GetResumeDocument(context.Background(), ref)
	if err != nil {
		t.Fatalf("GetResumeDocument: %v", err)
	}
	if got.Plan.RunID != doc.Plan.RunID || got.Plan.PlanSHA256 != doc.Plan.PlanSHA256 {
		t.Fatalf("plan did not round-trip: run=%q sha=%q", got.Plan.RunID, got.Plan.PlanSHA256)
	}
	if got.Request.SourceAssetID != doc.Request.SourceAssetID {
		t.Fatalf("request did not round-trip: %+v", got.Request)
	}
	if got.PublishFolderID != doc.PublishFolderID {
		t.Fatalf("publish folder did not round-trip: %q", got.PublishFolderID)
	}

	// Determinism is what makes a retried submit idempotent: the same document
	// yields the same address (and the CAS deduplicates the bytes).
	second, err := store.PutResumeDocument(context.Background(), doc)
	if err != nil {
		t.Fatalf("second PutResumeDocument: %v", err)
	}
	if second.SHA256 != ref.SHA256 || second.SizeBytes != ref.SizeBytes {
		t.Fatalf("address is not deterministic: %+v vs %+v", second, ref)
	}
}

// TestCASContinuationStoreFailsClosed certifies that a drifted or missing
// address can never be resumed: the reference travels through a job payload
// the broker persists and redelivers, so it must be verified, not trusted.
func TestCASContinuationStoreFailsClosed(t *testing.T) {
	store := newContinuationStoreFixture(t)
	doc := resumeDocumentFixture(t)
	ref, err := store.PutResumeDocument(context.Background(), doc)
	if err != nil {
		t.Fatalf("PutResumeDocument: %v", err)
	}

	t.Run("unknown digest", func(t *testing.T) {
		missing := cliprender.ContinuationRef{SHA256: strings.Repeat("9", 64), SizeBytes: ref.SizeBytes}
		if _, err := store.GetResumeDocument(context.Background(), missing); err == nil {
			t.Fatal("an unknown address must fail closed")
		}
	})
	t.Run("declared size drift", func(t *testing.T) {
		drifted := ref
		drifted.SizeBytes = ref.SizeBytes + 1
		if _, err := store.GetResumeDocument(context.Background(), drifted); err == nil {
			t.Fatal("a size that does not match the stored object must fail closed")
		}
	})
	t.Run("malformed address", func(t *testing.T) {
		if _, err := store.GetResumeDocument(context.Background(), cliprender.ContinuationRef{SHA256: "nope"}); err == nil {
			t.Fatal("a malformed address must fail closed")
		}
	})
	t.Run("invalid document is never written", func(t *testing.T) {
		broken := doc
		broken.Plan.PlanSHA256 = ""
		if _, err := store.PutResumeDocument(context.Background(), broken); err == nil {
			t.Fatal("an invalid resume document must be rejected before it is stored")
		}
	})
}

// TestCASContinuationStoreRejectsDriftedDocument certifies the digest binding
// at the document level: even a well-formed object whose plan does not match
// the plan it claims is refused.
func TestCASContinuationStoreRejectsDriftedDocument(t *testing.T) {
	store := newContinuationStoreFixture(t)
	doc := resumeDocumentFixture(t)
	// A document whose plan is internally inconsistent must not be stored: the
	// digest binding is checked by Validate via Attributes.
	doc.Plan.RunID = "some-other-clip"
	if _, err := store.PutResumeDocument(context.Background(), doc); err == nil {
		t.Fatal("a resume document whose plan contradicts its sealed digest must be rejected")
	}
}
