// Package indexing — payload_builder_semanticdoc_test.go pins the item-7
// integration: BuildPayloadFromDocument stamps the composed semantic-document
// identity (semantic_document_version + semantic_document_hash) and the hash
// is the exact SHA-256 of the canonical composer's DocumentText.
package indexing

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestBuildPayloadFromDocument_SemanticDocumentIdentityStamped(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Title = "Jackie Chan Interview"
	doc.Metadata.Entities = []string{"Jackie Chan"}

	p := BuildPayloadFromDocument(doc, nil)

	if v, ok := p["semantic_document_version"]; !ok || v != "v3" {
		t.Fatalf("payload[semantic_document_version] = %v (ok=%v), want v3", v, ok)
	}
	hash, ok := p["semantic_document_hash"].(string)
	if !ok || len(hash) != 64 {
		t.Fatalf("payload[semantic_document_hash] = %v (ok=%v), want 64-char sha256", p["semantic_document_hash"], ok)
	}
	// The payload hash must equal the composer's hash of the same document.
	if want := composeSemanticDocument(doc).Hash; hash != want {
		t.Fatalf("payload[semantic_document_hash] = %q, want composer hash %q", hash, want)
	}
}

// TestResolveIndexedEvidence_Precedence pins the canonical evidence
// precedence at the indexing airlock: transcript → visual_summary → summary →
// description (semantic_summary is not yet on the IndexedMetadata airlock).
func TestResolveIndexedEvidence_Precedence(t *testing.T) {
	t.Run("transcript wins", func(t *testing.T) {
		doc := &IndexDocument{
			AssetID: "a",
			Metadata: IndexedMetadata{
				Transcript:    "T",
				VisualSummary: "V",
				Summary:       "S",
				Description:   "D",
			},
		}
		ev := resolveIndexedEvidence(doc)
		if ev.Text != "T" || ev.SourceType != asset.EvidenceTranscript {
			t.Fatalf("evidence = %+v, want transcript T", ev)
		}
	})

	t.Run("visual_summary then summary then description", func(t *testing.T) {
		ev := resolveIndexedEvidence(&IndexDocument{AssetID: "a", Metadata: IndexedMetadata{VisualSummary: "V", Summary: "S", Description: "D"}})
		if ev.Text != "V" || ev.SourceType != asset.EvidenceVisualSummary {
			t.Fatalf("evidence = %+v, want visual_summary V", ev)
		}
		ev = resolveIndexedEvidence(&IndexDocument{AssetID: "a", Metadata: IndexedMetadata{Summary: "S", Description: "D"}})
		if ev.Text != "S" || ev.SourceType != asset.EvidenceSummary {
			t.Fatalf("evidence = %+v, want summary S", ev)
		}
		ev = resolveIndexedEvidence(&IndexDocument{AssetID: "a", Metadata: IndexedMetadata{Description: "D"}})
		if ev.Text != "D" || ev.SourceType != asset.EvidenceDescription {
			t.Fatalf("evidence = %+v, want description D", ev)
		}
	})

	t.Run("nil doc is not groundable", func(t *testing.T) {
		if resolveIndexedEvidence(nil).Groundable() {
			t.Fatal("nil doc must yield not-groundable evidence")
		}
	})
}
