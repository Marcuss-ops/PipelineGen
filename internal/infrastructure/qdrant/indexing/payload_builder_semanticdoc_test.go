// Package indexing — payload_builder_semanticdoc_test.go pins the item-7
// integration: BuildPayloadFromDocument stamps the composed semantic-document
// identity (semantic_document_version + semantic_document_hash) and the hash
// is the exact SHA-256 of the canonical composer's DocumentText.
package indexing

import "testing"

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
