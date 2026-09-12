package renderinggen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
)

// CASContinuationStore is the canonical implementation of
// cliprender.ContinuationStore over the content-addressed store.
//
// It exists so the clip.render settle phase can resume from a small,
// verifiable payload: the submit phase writes ONE resume document (sealed plan
// + request + preparation results) and hands the settle phase only its
// address. The document is content-addressed, so:
//
//   - a retried submit re-derives the same address (the document is
//     deterministic for a given sealed plan) and the store deduplicates it;
//   - the settle phase verifies the bytes against the address it was given, so
//     a truncated or drifted document is detected instead of acted on;
//   - the job payload stays small, which matters because the Master persists
//     result_json and the broker copies the payload on every retry.
type CASContinuationStore struct {
	store *cas.Store
}

// NewCASContinuationStore wires the canonical CAS as the continuation store.
// Fail-fast: a nil store is a composition error, never a silent no-op.
func NewCASContinuationStore(store *cas.Store) (*CASContinuationStore, error) {
	if store == nil {
		return nil, fmt.Errorf("renderinggen continuation store: CAS store is required")
	}
	return &CASContinuationStore{store: store}, nil
}

// Compile-time assertion: the adapter satisfies the capability-owned port.
var _ cliprender.ContinuationStore = (*CASContinuationStore)(nil)

// PutResumeDocument stores the document and returns its content address.
// Fail-closed: an invalid document is rejected BEFORE anything is written, so
// the store can never hold a document the settle phase would have to refuse.
func (s *CASContinuationStore) PutResumeDocument(ctx context.Context, doc cliprender.ResumeDocument) (cliprender.ContinuationRef, error) {
	if s == nil || s.store == nil {
		return cliprender.ContinuationRef{}, fmt.Errorf("renderinggen continuation store: not wired")
	}
	if err := doc.Validate(); err != nil {
		return cliprender.ContinuationRef{}, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return cliprender.ContinuationRef{}, fmt.Errorf("renderinggen continuation store: encode resume document: %w", err)
	}
	obj, err := s.store.Put(ctx, bytes.NewReader(raw))
	if err != nil {
		return cliprender.ContinuationRef{}, fmt.Errorf("renderinggen continuation store: put resume document: %w", err)
	}
	return cliprender.ContinuationRef{SHA256: obj.SHA256, SizeBytes: obj.SizeBytes}, nil
}

// GetResumeDocument loads the document at ref.
//
// The bytes are verified against BOTH the declared size and the declared
// digest before they are decoded. The CAS already addresses objects by SHA-256
// and would refuse a corrupted object, but the reference travels through a job
// payload (which the broker persists and redelivers), so the settle phase must
// not trust it: a payload-level drift is a fail-closed error here rather than
// a render published from the wrong plan.
func (s *CASContinuationStore) GetResumeDocument(ctx context.Context, ref cliprender.ContinuationRef) (cliprender.ResumeDocument, error) {
	var doc cliprender.ResumeDocument
	if s == nil || s.store == nil {
		return doc, fmt.Errorf("renderinggen continuation store: not wired")
	}
	if err := ref.Validate(); err != nil {
		return doc, err
	}
	reader, err := s.store.Open(ctx, ref.SHA256)
	if err != nil {
		return doc, fmt.Errorf("renderinggen continuation store: open resume document %s: %w", ref.SHA256, err)
	}
	defer reader.Close()

	// Read one byte past the declared size so an oversized object is detected
	// without buffering it.
	raw, err := io.ReadAll(io.LimitReader(reader, ref.SizeBytes+1))
	if err != nil {
		return doc, fmt.Errorf("renderinggen continuation store: read resume document %s: %w", ref.SHA256, err)
	}
	if int64(len(raw)) != ref.SizeBytes {
		return doc, fmt.Errorf("renderinggen continuation store: resume document %s is %d bytes, want %d", ref.SHA256, len(raw), ref.SizeBytes)
	}
	// digest.SHA256Bytes is the SSOT helper (godlike/06): the SHA-256
	// algorithm lives in internal/kernel/digest, never in this package.
	got := digest.SHA256Bytes(raw)
	if !strings.EqualFold(got, ref.SHA256) {
		return doc, fmt.Errorf("renderinggen continuation store: resume document digest %s, want %s", got, ref.SHA256)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, fmt.Errorf("renderinggen continuation store: decode resume document: %w", err)
	}
	if err := doc.Validate(); err != nil {
		return doc, err
	}
	// Bind the document to the address it was fetched from: the decoded
	// document must still describe the plan whose digest the address encodes.
	if err := doc.Attributes(doc.Plan.RunID, doc.Plan.PlanSHA256); err != nil {
		return doc, err
	}
	return doc, nil
}
