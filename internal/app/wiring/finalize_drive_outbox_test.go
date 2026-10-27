// finalize_drive_outbox_test.go — pins the deferred finalize Drive delivery
// consumer: the upload goes through the SAME ArtifactPreparation as the
// synchronous path, and the Drive identity is reconciled against the canonical
// asset with the byte identity the delivery proved.
package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	infraartifacts "github.com/Marcuss-ops/PipelineGen/internal/platform/artifactstaging"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
)

type stubPreparation struct {
	gotPath string
	gotID   string
	result  finalization.PublishedArtifact
	err     error
}

func (s *stubPreparation) Prepare(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	s.gotPath = artifact.LocalPath
	s.gotID = artifact.ArtifactID
	if s.err != nil {
		return finalization.PublishedArtifact{}, s.err
	}
	if s.result.ArtifactID == "" {
		s.result = finalization.PublishedArtifact{
			ArtifactID: artifact.ArtifactID,
			Kind:       artifact.Kind,
			Filename:   artifact.Filename,
			MIMEType:   artifact.MIMEType,
			SizeBytes:  artifact.SizeBytes,
			SHA256:     artifact.SHA256,
			Location: finalization.AssetLocation{
				Provider: "drive", FileID: "drive-file-9", WebViewLink: "https://drive/file/9",
				DownloadLink: "https://drive/uc?id=9", Action: finalization.PublishCreated,
			},
		}
	}
	return s.result, nil
}

var _ finalization.ArtifactPreparationService = (*stubPreparation)(nil)

type stubAssetMutator struct {
	reconciled []persistence.DriveLocationPatch
	patched    []persistence.AssetPatch
}

func (m *stubAssetMutator) PatchAsset(_ context.Context, patch persistence.AssetPatch) error {
	m.patched = append(m.patched, patch)
	return nil
}
func (m *stubAssetMutator) PatchAssetTx(context.Context, persistence.Transaction, persistence.AssetPatch) error {
	return nil
}
func (m *stubAssetMutator) ReconcileDriveLocations(_ context.Context, changes []persistence.DriveLocationPatch) error {
	m.reconciled = append(m.reconciled, changes...)
	return nil
}
func (m *stubAssetMutator) ReconcileDriveLocationsTx(context.Context, persistence.Transaction, []persistence.DriveLocationPatch) error {
	return nil
}

var _ persistence.AssetMutator = (*stubAssetMutator)(nil)

func stagedDeliveryFixture(t *testing.T) (*cas.Store, string, []byte) {
	t.Helper()
	content := []byte(`{"title":"golden"}`)
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	root := filepath.Join(t.TempDir(), "cas")
	stager, err := infraartifacts.NewLocalStore(infraartifacts.Config{Workspace: filepath.Join(root, ".staging")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cas.NewStore(cas.Config{Root: root, Stager: stager})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), strings.NewReader(string(content))); err != nil {
		t.Fatal(err)
	}
	return store, sha, content
}

func deliveryEvent(t *testing.T, req finalization.FinalizeDriveDeliveryRequest) outboxevents.Event {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return outboxevents.Event{ID: 7, EventType: finalization.EventFinalizeDriveDeliveryRequested, PayloadJSON: string(payload), EventKey: "finalize-drive:job-1:job-1:script_json"}
}

func TestFinalizeDriveDeliveryHandler_PublishesAndReconciles(t *testing.T) {
	store, sha, content := stagedDeliveryFixture(t)
	preparation := &stubPreparation{}
	mutator := &stubAssetMutator{}
	handler, err := newFinalizeDriveDeliveryHandler(preparation, mutator, store, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	req := finalization.NewFinalizeDriveDeliveryRequest(finalization.FinalizeDriveDeliveryRequest{
		JobID: "job-1", AssetID: "job-1:script_json", ArtifactID: "job-1:script_json",
		Kind: finalization.KindScript, Filename: "script.json", MIMEType: "application/json",
		SHA256: sha, SizeBytes: int64(len(content)), IdempotencyKey: "job-1:script_json",
		Requirement: finalization.ArtifactRequirementRequired, StorageKey: sha,
	})
	if err := handler.Handle(context.Background(), deliveryEvent(t, req)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// The publication runs through the canonical preparation against the
	// certified object-store copy (Prepare re-hashes those exact bytes).
	objectPath, err := store.LocalPath(sha)
	if err != nil {
		t.Fatal(err)
	}
	if preparation.gotPath != objectPath {
		t.Fatalf("preparation path = %q, want the object-store copy %q", preparation.gotPath, objectPath)
	}
	if len(mutator.reconciled) != 1 {
		t.Fatalf("reconcile calls = %d, want 1", len(mutator.reconciled))
	}
	patch := mutator.reconciled[0]
	if patch.AssetID != "job-1:script_json" || patch.DriveFileID != "drive-file-9" || patch.FileSizeBytes != int64(len(content)) {
		t.Fatalf("reconcile patch = %+v, want the delivered Drive identity with the proved byte size", patch)
	}
	if patch.MimeType != "application/json" {
		t.Fatalf("reconcile mime = %q, want application/json", patch.MimeType)
	}
	if len(mutator.patched) != 1 || mutator.patched[0].MetadataPatchJSON == nil {
		t.Fatalf("metadata patch = %+v, want one delivery_status/drive identity patch", mutator.patched)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(*mutator.patched[0].MetadataPatchJSON), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["delivery_status"] != "completed" || meta["drive_file_id"] != "drive-file-9" {
		t.Fatalf("metadata patch = %v, want completed + drive identity", meta)
	}
}

func TestFinalizeDriveDeliveryHandler_FailsClosedOnInvalidPayload(t *testing.T) {
	store, sha, content := stagedDeliveryFixture(t)
	preparation := &stubPreparation{}
	mutator := &stubAssetMutator{}
	handler, err := newFinalizeDriveDeliveryHandler(preparation, mutator, store, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	req := finalization.NewFinalizeDriveDeliveryRequest(finalization.FinalizeDriveDeliveryRequest{
		JobID: "job-1", AssetID: "job-1:script_json", ArtifactID: "job-1:script_json",
		Kind: finalization.KindScript, Filename: "script.json",
		SHA256: sha, SizeBytes: int64(len(content)), IdempotencyKey: "job-1:script_json",
		Requirement: finalization.ArtifactRequirementRequired,
	})
	if err := handler.Handle(context.Background(), deliveryEvent(t, req)); err == nil {
		t.Fatal("an intent with no storage key must fail closed")
	}
	if preparation.gotID != "" || len(mutator.reconciled) != 0 {
		t.Fatalf("invalid intent must not publish or reconcile (got %q, %d patches)", preparation.gotID, len(mutator.reconciled))
	}
}

func TestFinalizeDriveDeliveryHandler_MissingObjectFails(t *testing.T) {
	store, sha, content := stagedDeliveryFixture(t)
	preparation := &stubPreparation{}
	mutator := &stubAssetMutator{}
	handler, err := newFinalizeDriveDeliveryHandler(preparation, mutator, store, nil, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	req := finalization.NewFinalizeDriveDeliveryRequest(finalization.FinalizeDriveDeliveryRequest{
		JobID: "job-1", AssetID: "job-1:script_json", ArtifactID: "job-1:script_json",
		Kind: finalization.KindScript, Filename: "script.json", MIMEType: "application/json",
		SHA256: strings.Repeat("b", 64), SizeBytes: int64(len(content)), IdempotencyKey: "job-1:script_json",
		Requirement: finalization.ArtifactRequirementRequired, StorageKey: sha,
	})
	// StorageKey resolves, but the manifest digest disagrees with the delivered
	// object: the canonical preparation re-verifies and rejects the intent.
	if err := handler.Handle(context.Background(), deliveryEvent(t, req)); err == nil {
		t.Fatal("digest mismatch must fail the delivery, never publish unverified bytes")
	}
}

func TestRegisterFinalizeDriveDeliveryHandler_RequiresRegistry(t *testing.T) {
	store, _, _ := stagedDeliveryFixture(t)
	if err := registerFinalizeDriveDeliveryHandler(nil, &stubPreparation{}, &stubAssetMutator{}, store, nil, zap.NewNop()); err == nil {
		t.Fatal("nil registry must fail closed at composition time")
	}
}

// TestFinalizeDriveOutboxRegistry_SplitJobsPlane pins the plane selection the
// first L10 verification run (2026-10-26) broke: the finalize delivery intent
// is enqueued into the jobs-plane outbox (jobs.db), and the pool dispatches
// through the registry that DRAINS that plane. Registering the consumer on
// the media registry dead-letters every intent with "no handler registered".
func TestFinalizeDriveOutboxRegistry_SplitJobsPlane(t *testing.T) {
	eventsRegistry := outboxevents.NewHandlerRegistry()
	jobsRegistry := outboxevents.NewHandlerRegistry()

	if got := finalizeDriveOutboxRegistry(eventsRegistry, jobsRegistry, true); got != jobsRegistry {
		t.Fatal("split jobs plane must select the jobs-plane outbox registry")
	}
	if got := finalizeDriveOutboxRegistry(eventsRegistry, jobsRegistry, false); got != eventsRegistry {
		t.Fatal("non-split plane must select the media outbox registry")
	}
	if got := finalizeDriveOutboxRegistry(eventsRegistry, nil, true); got != eventsRegistry {
		t.Fatal("missing jobs registry must fall back to the media registry")
	}

	// Registration must make the handler retrievable from the SELECTED
	// registry — the exact lookup the pool performs at dispatch time.
	store, _, _ := stagedDeliveryFixture(t)
	selected := finalizeDriveOutboxRegistry(eventsRegistry, jobsRegistry, true)
	if err := registerFinalizeDriveDeliveryHandler(selected, &stubPreparation{}, &stubAssetMutator{}, store, nil, zap.NewNop()); err != nil {
		t.Fatalf("register on selected registry: %v", err)
	}
	if _, ok := selected.Get(finalization.EventFinalizeDriveDeliveryRequested); !ok {
		t.Fatal("delivery consumer must be retrievable from the jobs-plane registry at dispatch time")
	}
	if _, ok := eventsRegistry.Get(finalization.EventFinalizeDriveDeliveryRequested); ok {
		t.Fatal("the media registry must not hold the jobs-plane delivery consumer")
	}
}

// Silence the unused import guard for os when the fixture evolves.
var _ = os.Remove
