package scriptgeneration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// runner_gate_doc_publisher_test.go — see TestPublishFinalAudioNeeded... pins
// below; this stub satisfies the DocumentPublisher port without doing real
// work; only its presence (nil vs non-nil) matters to the gate.
type gateDocPublisher struct{}

func (gateDocPublisher) UpsertDocument(context.Context, DocumentInput) (DocumentReference, error) {
	return DocumentReference{}, nil
}

// overlayOnlyRequest is the request shape with no documents and no remote
// final render: the only consumers of a Drive-published master audio are the
// documents catalog and the final_job payload, so this shape must skip it.
func overlayOnlyRequest() GenerateRequest {
	req := GenerateRequest{Title: "overlay-only"}
	req.Docs.Enabled = false
	req.DocsEnabled = false
	req.Languages = []Language{"it"}
	req.FinalJob = false
	return req
}

// TestPublishFinalAudioNeededSkipsOverlayOnly pins the skip: no documents, no
// final job, no consumer.
func TestPublishFinalAudioNeededSkipsOverlayOnly(t *testing.T) {
	r := &Runner{docPublisher: nil, log: zap.NewNop()}
	req := overlayOnlyRequest()
	require.False(t, publishFinalAudioNeeded(req, r),
		"an overlay-only run has no consumer of the published master audio")
}

// TestPublishFinalAudioNeededForFinalJob pins that the final_job payload is a
// consuming contract: the certified asset ID is referenced in the payload.
func TestPublishFinalAudioNeededForFinalJob(t *testing.T) {
	r := &Runner{docPublisher: nil, log: zap.NewNop()}
	req := overlayOnlyRequest()
	req.FinalJob = true
	require.True(t, publishFinalAudioNeeded(req, r),
		"final_job consumes the certified audio asset ID")
}

// TestPublishFinalAudioNeededForDocuments pins the documents consumer: the
// catalog renders the final_audio link (DocumentRenderOptions.FinalAudio).
// The docPublisher must be wired: a nil publisher mirrors documentSkipped.
func TestPublishFinalAudioNeededForDocuments(t *testing.T) {
	req := overlayOnlyRequest()
	req.Docs.Enabled = true
	req.Docs.Languages = []Language{"it"}

	r := &Runner{docPublisher: nil, log: zap.NewNop()}
	require.False(t, publishFinalAudioNeeded(req, r),
		"docs enabled but no publisher wired mirrors documentSkipped")

	r2 := &Runner{docPublisher: gateDocPublisher{}, log: zap.NewNop()}
	require.True(t, publishFinalAudioNeeded(req, r2),
		"documents are active: the catalog consumes the published link")
}

// TestPublishFinalAudioNeededMatchesDocumentSkippedGate pins the equivalence
// with runDocumentPhase's skip predicate: publishFinalAudioNeeded and
// documentSkipped must be the same decision for the same request shape,
// otherwise the run publishes nothing yet still pays Drive-upload wall.
func TestPublishFinalAudioNeededMatchesDocumentSkippedGate(t *testing.T) {
	cases := []struct {
		name        string
		docsEnabled bool
		langs       []Language
		publisher   bool
		finalJob    bool
		wantPublish bool
	}{
		{"overlay-only", false, nil, false, false, false},
		{"docs-enabled", true, []Language{"it"}, true, false, true},
		{"docs-without-publisher", true, []Language{"it"}, false, false, false},
		{"final-job-only", false, nil, false, true, true},
		{"everything-on", true, []Language{"it"}, true, true, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := overlayOnlyRequest()
			req.Docs.Enabled = tc.docsEnabled
			req.Docs.Languages = tc.langs
			req.FinalJob = tc.finalJob
			var publisher DocumentPublisher
			if tc.publisher {
				publisher = gateDocPublisher{}
			}
			r := &Runner{docPublisher: publisher, log: zap.NewNop()}
			require.Equal(t, tc.wantPublish, publishFinalAudioNeeded(req, r))
		})
	}
}
