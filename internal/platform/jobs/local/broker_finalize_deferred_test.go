// Package local — broker_finalize_deferred_test.go: the deferred Drive
// publication seam (L10). The completion must record a DURABLE delivery
// intent without a Drive location while the synchronous path stays byte-
// identical for out-of-scope artifacts.
package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	"go.uber.org/zap"
)

// captureFinalizer records the exact FinalizationRequest the broker hands to
// the completion spine (artifacts + outbox events).
type captureFinalizer struct {
	req finalization.FinalizationRequest
}

func (f *captureFinalizer) CompleteWithArtifacts(_ context.Context, req finalization.FinalizationRequest) (*finalization.FinalizationResult, error) {
	f.req = req
	return &finalization.FinalizationResult{JobID: req.Result.JobID, Status: "SUCCEEDED"}, nil
}

var _ finalization.JobFinalizer = (*captureFinalizer)(nil)

func testObjectStore(t *testing.T) *cas.Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "cas")
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(root, ".staging")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cas.NewStore(cas.Config{Root: root, Stager: stager})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func stagedScriptArtifact(t *testing.T, meta map[string]any) (remote.StagedArtifacts, string) {
	t.Helper()
	content := []byte(`{"title":"golden"}`)
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	return remote.StagedArtifacts{{
		ArtifactID:       "job-defer-1:script_json",
		Destination:      "script",
		Path:             path,
		Filename:         "script.json",
		MIMEType:         "application/json",
		SizeBytes:        int64(len(content)),
		SHA256:           sha,
		Required:         true,
		DriveGroup:       "job-defer-1",
		ArtifactMetadata: meta,
	}}, sha
}

func deferTestBroker(t *testing.T, jobType string, store *cas.Store, finalizer finalization.JobFinalizer) *Broker {
	t.Helper()
	return &Broker{
		jobs: &stubJobStore{job: &job.Job{
			ID: "job-defer-1", Type: jobType, WorkerID: "w-1", LeaseID: "lease-1", Revision: 1,
		}},
		finalizer:          finalizer,
		preparation:        assetfinalizer.NewArtifactPreparation(&stubPublisherPort{location: finalization.AssetLocation{Provider: "drive", FileID: "drive-file-1", Action: finalization.PublishCreated}}, nil),
		deferredDriveStore: store,
		log:                zap.NewNop(),
	}
}

// TestBroker_DeferredDrivePublication_EmitsIntentWithoutLocation pins the L10
// contract: a script.generate deliverable completes with a durable delivery
// intent in the completion outbox fan-out, an artifact committed WITHOUT a
// Drive location (deliberately local), and the certified bytes staged in the
// content-addressed object store under the payload's storage key.
func TestBroker_DeferredDrivePublication_EmitsIntentWithoutLocation(t *testing.T) {
	staged, sha := stagedScriptArtifact(t, nil)
	store := testObjectStore(t)
	finalizer := &captureFinalizer{}
	b := deferTestBroker(t, "script.generate", store, finalizer)

	if _, err := b.CompleteWithArtifacts(context.Background(), appjobs.CompleteWithArtifactsCommand{
		WorkerID: "w-1", LeaseID: "lease-1", JobID: "job-defer-1", ExpectedRevision: 1,
		StagedArtifacts: mustJSON(t, staged),
	}); err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}

	if len(finalizer.req.Events) != 1 {
		t.Fatalf("outbox events = %d, want exactly one delivery intent", len(finalizer.req.Events))
	}
	evt := finalizer.req.Events[0]
	if evt.EventType != finalization.EventFinalizeDriveDeliveryRequested {
		t.Fatalf("event type = %q, want %q", evt.EventType, finalization.EventFinalizeDriveDeliveryRequested)
	}
	if evt.AggregateID != "job-defer-1" {
		t.Fatalf("aggregate = %q, want job-defer-1", evt.AggregateID)
	}
	if want := "finalize-drive:job-defer-1:job-defer-1:script_json:" + sha; evt.EventKey != want {
		t.Fatalf("event key = %q, want %q", evt.EventKey, want)
	}
	var intent finalization.FinalizeDriveDeliveryRequest
	if err := json.Unmarshal(evt.Payload, &intent); err != nil {
		t.Fatalf("decode intent: %v", err)
	}
	if err := intent.Validate(); err != nil {
		t.Fatalf("intent must be complete: %v", err)
	}
	if intent.StorageKey != sha || intent.SHA256 != sha {
		t.Fatalf("storage key/hash = %q/%q, want %q", intent.StorageKey, intent.SHA256, sha)
	}
	exists, err := store.Exists(context.Background(), sha)
	if err != nil || !exists {
		t.Fatalf("object store must hold the certified bytes (exists=%v err=%v)", exists, err)
	}

	if len(finalizer.req.Artifacts) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(finalizer.req.Artifacts))
	}
	artifact := finalizer.req.Artifacts[0]
	if artifact.Location.Provider != "" || artifact.Location.FileID != "" {
		t.Fatalf("deferred artifact location = %+v, want deliberately local (empty)", artifact.Location)
	}
	if artifact.ArtifactMetadata["delivery_status"] != "pending" {
		t.Fatalf("delivery_status = %v, want pending", artifact.ArtifactMetadata["delivery_status"])
	}
	if artifact.ArtifactMetadata[finalization.DrivePublicationMetadataKey] != finalization.DrivePublicationDeferred {
		t.Fatalf("drive_publication marker missing from the deferred artifact metadata")
	}
}

// TestBroker_DeferredDrivePublication_OutOfScopeStaysSynchronous pins the
// scope rule: every other job type keeps today's synchronous publication and
// emits no intent.
func TestBroker_DeferredDrivePublication_OutOfScopeStaysSynchronous(t *testing.T) {
	staged, _ := stagedScriptArtifact(t, nil)
	store := testObjectStore(t)
	finalizer := &captureFinalizer{}
	b := deferTestBroker(t, "image.generate.google", store, finalizer)

	if _, err := b.CompleteWithArtifacts(context.Background(), appjobs.CompleteWithArtifactsCommand{
		WorkerID: "w-1", LeaseID: "lease-1", JobID: "job-defer-1", ExpectedRevision: 1,
		StagedArtifacts: mustJSON(t, staged),
	}); err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}
	if len(finalizer.req.Events) != 0 {
		t.Fatalf("outbox events = %d, want 0 for an out-of-scope job", len(finalizer.req.Events))
	}
	if got := finalizer.req.Artifacts[0].Location.FileID; got != "drive-file-1" {
		t.Fatalf("artifact location file id = %q, want the synchronous drive-file-1", got)
	}
}

// TestBroker_DeferredDrivePublication_OverrideForcesSynchronous pins the
// producer escape hatch: drive_publication=sync keeps a script.generate
// deliverable on the synchronous path.
func TestBroker_DeferredDrivePublication_OverrideForcesSynchronous(t *testing.T) {
	staged, _ := stagedScriptArtifact(t, map[string]any{finalization.DrivePublicationMetadataKey: finalization.DrivePublicationSync})
	store := testObjectStore(t)
	finalizer := &captureFinalizer{}
	b := deferTestBroker(t, "script.generate", store, finalizer)

	if _, err := b.CompleteWithArtifacts(context.Background(), appjobs.CompleteWithArtifactsCommand{
		WorkerID: "w-1", LeaseID: "lease-1", JobID: "job-defer-1", ExpectedRevision: 1,
		StagedArtifacts: mustJSON(t, staged),
	}); err != nil {
		t.Fatalf("CompleteWithArtifacts: %v", err)
	}
	if len(finalizer.req.Events) != 0 {
		t.Fatalf("outbox events = %d, want 0 under the sync override", len(finalizer.req.Events))
	}
	if got := finalizer.req.Artifacts[0].Location.FileID; got != "drive-file-1" {
		t.Fatalf("artifact location file id = %q, want the synchronous drive-file-1", got)
	}
}

func TestFinalizeDrivePublicationDeferrable(t *testing.T) {
	script := finalization.VerifiedArtifact{Kind: finalization.KindScript}
	voice := finalization.VerifiedArtifact{Kind: finalization.KindVoiceover}
	image := finalization.VerifiedArtifact{Kind: finalization.KindImage}
	cases := []struct {
		name     string
		jobType  string
		artifact finalization.VerifiedArtifact
		want     bool
	}{
		{"script deliverable defers", "script.generate", script, true},
		{"audio deliverable defers", "script.generate", voice, true},
		{"image stays synchronous", "script.generate", image, false},
		{"other job types stay synchronous", "books.process", script, false},
		{"sync override pins the synchronous path", "script.generate",
			finalization.VerifiedArtifact{Kind: finalization.KindScript,
				ArtifactMetadata: map[string]any{finalization.DrivePublicationMetadataKey: "sync"}}, false},
		{"deferred override forces the async path", "books.process",
			finalization.VerifiedArtifact{Kind: finalization.KindDocument,
				ArtifactMetadata: map[string]any{finalization.DrivePublicationMetadataKey: "deferred"}}, true},
	}
	for _, tc := range cases {
		if got := finalizeDrivePublicationDeferrable(tc.jobType, tc.artifact); got != tc.want {
			t.Errorf("%s: deferrable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
