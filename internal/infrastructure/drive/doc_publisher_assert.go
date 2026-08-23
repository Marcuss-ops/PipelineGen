// Package drive — doc_publisher_assert.go (P1-6, July 2026)
//
// Pattern 0 compile-time assertion: *DocClientImpl structurally
// satisfies delivery.DocPublisher. If a method is added/removed from
// either side, the build breaks here.
//
// The assertion lives here (infrastructure package) rather than in
// delivery (application-layer port package) to avoid an import cycle:
// drive already imports delivery for the Publisher port, so
// delivery cannot import drive.
//
// Mirrors the existing `var _ Admin = (*Uploader)(nil)` pattern in
// ports.go.
package drive

import "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"

// Compile-time: *DocClientImpl satisfies the application-layer
// delivery.DocPublisher port. Return types are any in the interface
// (to break the drive↔delivery import cycle), and *Doc / []Doc
// satisfy any in Go's structural type system.
var _ delivery.DocPublisher = (*DocClientImpl)(nil)
