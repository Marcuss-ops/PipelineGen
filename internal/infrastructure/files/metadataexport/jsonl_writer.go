// Package metadataexport — JSONL combined-file writer.
//
// Step 2 of the post-architettura 2026 plan (June 2026): the legacy
// metadata_export.go::writeJSONL method built a combined JSONL via
// json.NewEncoder + os.Create + os.Rename. After split, the JSONL
// emission lives here — the application supplies each item as a
// pre-marshalled []byte (service.go writes the per-item bodies once
// and reuses them) and the writer emits them newline-delimited.
//
// Atomicity: same POSIX .tmp+rename pattern as the JSON sidecar.
// Cancellation behaviour: NOT honoured mid-write (the encoder
// appends each line then closes atomically). For very large jobs the
// outbox pool's batch step is the cancellation boundary, not the
// per-event writer — see the legacy comment "Combiner file for
// jsonl/csv: also written atomically".
package metadataexport

import (
	"bytes"
	"encoding/json"

	appexport "github.com/Marcuss-ops/PipelineGen/internal/application/assets/metadataexport"
)

// Compile-time assertion reuses the same FileWriter struct as the JSON
// + CSV writers. Single struct, 4 format-specific methods.
var _ appexport.ExportWriter = (*FileWriter)(nil)

// WriteJSONL emits a newline-delimited JSONL stream from the supplied
// items. Each item is a pre-marshalled JSON object; the writer
// appends a newline after each so downstream JSONL parsers (jq, ripgrep,
// DuckDB, etc.) read it as a stream. The assembled byte buffer is
// persisted atomically via atomicWrite so partial file states are
// never observable.
//
// Up-front buffer assembly (vs. streaming json.NewEncoder into the
// final file): chosen so the atomic guarantee holds with a single
// final os.Rename. Streaming json.NewEncoder into the final path
// would expose a partial JSONL stream to readers during the write —
// the legacy implementation avoided this by writing to the .tmp first.
// We keep the exact pre-rename write-buffer pattern for parity.
func (w *FileWriter) WriteJSONL(dir string, jobID string, items [][]byte) error {
	if err := ensureDir(dir); err != nil {
		return err
	}
	buf := bytes.NewBuffer(make([]byte, 0, 1024*len(items)))
	for _, item := range items {
		if !json.Valid(item) {
			// Validate each item before appending so a malformed line
			// fails the write rather than corrupting the JSONL stream.
			// Matches the legacy behaviour: "if err := enc.Encode(snap);
			// err != nil { ... return err }".
			return invalidJSONLError{assetCount: len(items)}
		}
		buf.Write(item)
		buf.WriteByte('\n')
	}
	return atomicWrite(dir+"/"+jobID+".jsonl", buf.Bytes())
}

// invalidJSONLError surfaces the malformed-item case from WriteJSONL
// without forcing an extra json.RawMessage import at the package
// boundary. Implementations of the legacy behaviour kept this an
// "enc.Encode failed" error; we mirror the diagnostic granularity
// without parking the encoder on the writer.
type invalidJSONLError struct{ assetCount int }

func (e invalidJSONLError) Error() string {
	return "metadata_export: jsonl item is not valid JSON"
}
