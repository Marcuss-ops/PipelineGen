// Package metadataexport — JSON sidecar file writer.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the legacy
// metadata_export.go::writeOne method called the legacy atomicWrite
// helper inline. After split, the per-asset sidecar write lands here
// as a thin method on the file-writer struct. The application layer's
// service goes the canonical path:
//
//	s.writer.WriteJSON(s.outputDir, assetID, body)
//
// and the application never imports os or path/filepath for FS
// concerns — it just hands bytes to this method.
package metadataexport

import (
	appexport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/metadataexport"
)

// Compile-time assertion: the file-writer concrete must satisfy the
// typed port. Establishes the canonical coupling between the
// application-layer declared surface and the infra-layer adapter.
var _ appexport.ExportWriter = (*FileWriter)(nil)

// FileWriter is the single concrete ExportWriter; its methods are
// split across the 4 files of this package (WriteJSON here,
// WriteJSONL in jsonl_writer.go, WriteCSV in csv_writer.go, EnsureDir
// also declared here as a thin pass-through to the package-private
// atomic_writer.go primitive). One struct, file-split per format —
// the responsibility is FS-shape, the split is per-method for the
// file-size cap (AGENTS.md Pattern 5).
type FileWriter struct{}

// EnsureDir delegates to the package-private primitive in
// atomic_writer.go. Declared here (not in atomic_writer.go) so the
// FileWriter concrete carries the method on the struct the interface
// assertion pins (see `var _ appexport.ExportWriter = (*FileWriter)(nil)`
// above).
func (w *FileWriter) EnsureDir(dir string) error {
	return ensureDir(dir)
}

// WriteJSON writes a single per-asset JSON sidecar at dir + "/" +
// assetID + ".json". body is the already-marshalled JSON (the
// application-side service.go owns the marshalling so the writer
// stays a pure FS-shaping layer).
//
// The .tmp+rename atomicity is delegated to atomicWrite (POSIX-atomic
// on linux/macos when both paths share a filesystem — always true
// here because both live under OutputDir).
func (w *FileWriter) WriteJSON(dir string, assetID string, body []byte) error {
	if err := w.EnsureDir(dir); err != nil {
		return err
	}
	return atomicWrite(dir+"/"+assetID+".json", body)
}
