// Package delivery — doc_publisher.go (P1-6, July 2026)
//
// DocPublisher is the canonical application-layer port for Google Docs
// operations. It covers a subset of drive.DocClient (declared in
// internal/platform/drive/doc_client.go) — only the methods
// whose signatures do not reference *drive.Doc / []drive.Doc.
//
// CreateDoc and ListRecentDocs are intentionally excluded because they
// return *drive.Doc / []drive.Doc types that live in the infrastructure
// package (internal/platform/drive), which already imports
// delivery for the Publisher port. Including those methods would create
// an import cycle.
//
// Callers that previously depended on drive.DocClient directly
// (wire_script_adapters.go, lessons/service.go) now depend on
// delivery.DocPublisher — the same concrete, a narrower dependency
// graph, and Pattern 0 discipline (application owns the port,
// infrastructure owns the concrete).
//
// The compile-time assertion that *drive.DocClientImpl satisfies
// DocPublisher lives in internal/platform/drive/doc_publisher_assert.go.
package delivery

import "context"

// DocPublisher is the canonical port for Google Docs operations whose
// signatures avoid *drive.Doc references (to prevent an import cycle).
//
// Methods:
//   - ShareDoc: shares a doc with a user by email.
//   - UpdateDoc: updates title and content of an existing doc.
//
// For CreateDoc / ListRecentDocs, callers should route through the
// concrete *drive.DocClientImpl or a future delivery-owned Doc type.
type DocPublisher interface {
	ShareDoc(ctx context.Context, docID, email, role string) error
	UpdateDoc(ctx context.Context, docID, title, content string) error
}
