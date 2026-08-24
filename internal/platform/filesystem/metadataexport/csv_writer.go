// Package metadataexport — CSV combined-file writer.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the legacy
// metadata_export.go::writeCSV method inlined the csv.Writer header-
// +rows + flush + error-check + atomic rename. After split, the CSV
// emission lives here as a single method on the file-writer struct.
//
// The application's service.go (internal/capabilities/jobs/outbox/
// metadataexport/service.go::writeCSVRows) does the row assembly:
//   - decodes each per-asset snapshot body,
//   - builds the 4-column row (asset_id, exported_at, includes,
//     sections_json),
//   - hands [][]string to this method.
//
// This method owns the encoder-shaped concerns (csv.Writer +
// canonical quoting) and the FS-shaped concerns (header flush +
// atomic rename). It does NOT do row validation; that's the
// service's job per the pre-Step-2 `csvWellFormed` helper.
// Writer scope: encodes the canonical header + the caller's rows
// and persists atomically. csv.Writer applies Go's standard
// quoting (RFC 4180) so cells with embedded commas / quotes /
// newlines are escaped safely.
package metadataexport

import (
	"bytes"
	"encoding/csv"

	appexport "github.com/Marcuss-ops/PipelineGen/internal/application/assets/metadataexport"
)

// Compile-time assertion reuses the same FileWriter struct as the
// JSON + JSONL writers.
var _ appexport.ExportWriter = (*FileWriter)(nil)

// WriteCSV writes the canonical header + caller-supplied data rows
// to dir + "/" + jobID + ".csv". Returns the first non-nil error
// from the encoder (csv.Writer.Error() catches mid-write issues
// even when the underlying Writer returns nil from .Write).
//
// Atomicity: assembled into a bytes.Buffer first, then persisted
// via atomicWrite's POSIX .tmp + os.Rename. Streaming csv.Writer
// directly to the final path would expose a partial CSV to readers
// during the write — same rationale as WriteJSONL.
func (w *FileWriter) WriteCSV(dir string, jobID string, header []string, rows [][]string) error {
	if err := ensureDir(dir); err != nil {
		return err
	}

	buf := bytes.NewBuffer(make([]byte, 0, 256*(1+len(rows))))
	cw := csv.NewWriter(buf)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return atomicWrite(dir+"/"+jobID+".csv", buf.Bytes())
}
