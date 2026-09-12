package wiring

import (
	"context"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// recordingDocClient is a Drive DocClient that records every idempotency key it
// is asked to publish under. A re-keyed document shows up here as a second,
// different key for the same logical document — i.e. a duplicate Google Doc.
type recordingDocClient struct {
	keys []string
}

var _ drive.DocClient = (*recordingDocClient)(nil)

func (f *recordingDocClient) CreateDoc(_ context.Context, title, content, folderID string) (*drive.Doc, error) {
	return f.doc("doc-direct"), nil
}

func (f *recordingDocClient) CreateDocIdempotent(_ context.Context, title, content, folderID, idempotencyKey string, forceRefresh bool) (*drive.Doc, error) {
	f.keys = append(f.keys, idempotencyKey)
	return f.doc("doc-" + idempotencyKey), nil
}

func (f *recordingDocClient) ShareDoc(_ context.Context, docID, email, role string) error { return nil }

func (f *recordingDocClient) ListRecentDocs(_ context.Context, folderID string, limit int) ([]drive.Doc, error) {
	return nil, nil
}

func (f *recordingDocClient) UpdateDoc(_ context.Context, docID, title, content string) error {
	return nil
}

func (f *recordingDocClient) doc(id string) *drive.Doc {
	return &drive.Doc{ID: id, URL: "https://docs.google.com/document/d/" + id + "/edit"}
}

// TestDocumentPathsNeverReKeyExistingGoogleDoc is the behavior-parity guard for
// the two live document publication paths that share the canonical
// scriptgen.DocumentPublisher:
//
//   - the durable runner (single-item generation), which publishes through
//     scriptGenerationDocumentPublisher with scriptgen.DocumentIdempotencyKey
//   - the post-processor (batch generation), which publishes through
//     processor.DocumentsProcessor with the explicit historical key
//
// Both paths are driven twice with the identical logical document. The Drive
// idempotency key captured on the second publication MUST equal the first, so
// CreateDocIdempotent finds and updates the existing Doc instead of creating a
// duplicate. This pins the invariant across retries, resumes and restarts.
func TestDocumentPathsNeverReKeyExistingGoogleDoc(t *testing.T) {
	const (
		runID    = "run-42"
		language = "it"
	)

	fake := &recordingDocClient{}
	publisher := &scriptGenerationDocumentPublisher{client: fake}

	// ── durable runner path ────────────────────────────────────────────────
	// This is byte-for-byte the DocumentInput the runner builds
	// (see internal/capabilities/scripts/runner_phase_document.go).
	runnerInput := scriptgen.DocumentInput{
		RunID:    runID,
		Language: scriptgen.Language(language),
		Title:    "T_" + language,
		Content:  "document body",
		FolderID: "folder-1",
	}
	for i := 0; i < 2; i++ {
		if _, err := publisher.UpsertDocument(context.Background(), runnerInput); err != nil {
			t.Fatalf("runner publication %d: %v", i+1, err)
		}
	}

	// ── post-processor path ────────────────────────────────────────────────
	processor := processor.NewDocumentsProcessor(publisher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		ID:            runID,
		Title:         "T",
		Language:      language,
		DocsEnabled:   true,
		DocsLanguages: []string{language},
		DocsFolderID:  "folder-1",
	}
	for i := 0; i < 2; i++ {
		if _, err := processor.Process(context.Background(), plan, adapters.ProcessInput{
			Text:      "generated text",
			SpecScene: scriptpkg.SpecSceneOutput{Version: 1},
		}); err != nil {
			t.Fatalf("post-processor publication %d: %v", i+1, err)
		}
	}

	if len(fake.keys) != 4 {
		t.Fatalf("captured %d publication keys, want 4 (%v)", len(fake.keys), fake.keys)
	}
	runnerFirst, runnerRetry := fake.keys[0], fake.keys[1]
	processorFirst, processorRetry := fake.keys[2], fake.keys[3]

	// Runner path: stable key, derived by the canonical capability helper.
	if want := scriptgen.DocumentIdempotencyKey(runID, scriptgen.Language(language)); runnerFirst != want {
		t.Fatalf("runner key = %q, want %q", runnerFirst, want)
	}
	if runnerRetry != runnerFirst {
		t.Fatalf("runner re-keyed on republish: %q -> %q", runnerFirst, runnerRetry)
	}

	// Post-processor path: stable key, derived by the canonical capability helper.
	if want := scriptgen.DocumentPostProcessorIdempotencyKey(runID, language); processorFirst != want {
		t.Fatalf("post-processor key = %q, want %q", processorFirst, want)
	}
	if processorRetry != processorFirst {
		t.Fatalf("post-processor re-keyed on republish: %q -> %q", processorFirst, processorRetry)
	}

	// The two historical conventions are deliberately distinct: both already
	// have Documents in the wild, so unifying them would orphan the existing
	// Doc and create a duplicate. The invariant this test protects is that
	// EACH path is stable — never that the paths share one key. A future
	// unification must therefore ship a Drive-side migration, not a silent
	// separator change (pinned by TestDocumentIdempotencyKeys_Parity).
	if runnerFirst == processorFirst {
		t.Logf("runner and post-processor now share the key %q; if this is an intentional unification, add the Drive migration and update TestDocumentIdempotencyKeys_Parity", runnerFirst)
	}
}
