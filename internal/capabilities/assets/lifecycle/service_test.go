package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type lifecyclePublisherStub struct {
	err       error
	nilResult bool
	calls     *[]string
}

type lifecycleFinalizerStub struct {
	err        error
	failAlways bool
	failAt     int
	callCount  int
	calls      []asset.AssetPublishStatus
	records    []*artifacts.MediaRecord
	order      *[]string
}

func (f *lifecycleFinalizerStub) Finalize(_ context.Context, rec *artifacts.MediaRecord, _ artifacts.FinalizeOptions) (*artifacts.FinalizeResult, error) {
	if f.order != nil {
		*f.order = append(*f.order, "finalizer:"+string(rec.PublishStatus))
	}
	f.calls = append(f.calls, rec.PublishStatus)
	f.callCount++
	recordCopy := *rec
	f.records = append(f.records, &recordCopy)
	if f.err != nil && (f.failAlways || (f.failAt > 0 && f.callCount == f.failAt)) {
		return nil, f.err
	}
	return &artifacts.FinalizeResult{OK: true, Status: rec.Status, Record: rec}, nil
}

type lifecycleStoreStub struct {
	findErr   error
	existing  *assetop.AssetRecord
	upsertErr error
	records   map[string]*artifacts.MediaRecord
}

func (s *lifecycleStoreStub) FindExisting(context.Context, assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
	return s.existing, s.findErr
}

func (s *lifecycleStoreStub) ListWithDriveFileID(context.Context, string) ([]*assetop.AssetRecord, error) {
	return nil, nil
}

func (s *lifecycleStoreStub) MarkDriveMissing(context.Context, string) error  { return nil }
func (s *lifecycleStoreStub) DeleteAssetRecord(context.Context, string) error { return nil }

func (s *lifecycleStoreStub) Upsert(ctx context.Context, rec *artifacts.MediaRecord) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	if s.records == nil {
		s.records = make(map[string]*artifacts.MediaRecord)
	}
	s.records[rec.ID] = rec
	return nil
}

func (s *lifecycleStoreStub) UpsertMedia(ctx context.Context, rec *artifacts.MediaRecord) error {
	return s.Upsert(ctx, rec)
}

func (s *lifecycleStoreStub) Get(_ context.Context, id string) (*artifacts.MediaRecord, error) {
	if s.records == nil {
		return nil, nil
	}
	return s.records[id], nil
}

func (s *lifecycleStoreStub) DeleteMedia(context.Context, string) error { return nil }
func (s *lifecycleStoreStub) GetAllWithDriveFileID(context.Context) ([]*artifacts.MediaRecord, error) {
	return nil, nil
}
func (s *lifecycleStoreStub) FindByPHash(context.Context, string) (string, error) { return "", nil }
func (s *lifecycleStoreStub) GetMedia(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
	return s.Get(ctx, id)
}

func lifecycleNoPersistenceConfig() Config {
	return Config{
		DuplicatePolicy: assetop.DuplicatePolicy{},
		UploadPolicy:    assetop.UploadPolicy{},
		PersistPolicy:   assetop.PersistPolicy{},
	}
}

func (p *lifecyclePublisherStub) Publish(context.Context, delivery.PublishRequest) (*delivery.PublishResult, error) {
	if p.calls != nil {
		*p.calls = append(*p.calls, "publisher")
	}
	if p.err != nil {
		return nil, p.err
	}
	if p.nilResult {
		return nil, nil
	}
	return &delivery.PublishResult{FileID: "drive-file", WebViewLink: "https://drive.test/file"}, nil
}

func (p *lifecyclePublisherStub) ResolveFolder(context.Context, delivery.PublishRequest) (string, error) {
	return "drive-folder", nil
}

func TestProcessAsset_FinalizerMissingFailsClosed(t *testing.T) {
	svc := NewService(ServiceDeps{}, Config{
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{}, "hash")
	if result != nil {
		t.Fatalf("result = %#v, want nil when finalizer is unavailable", result)
	}
	if !errors.Is(err, ErrFinalizerUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrFinalizerUnavailable)", err)
	}
}

func TestProcessAsset_RequiredPublisherFailureReturnsError(t *testing.T) {
	cause := errors.New("drive unavailable")
	svc := NewService(ServiceDeps{
		Publisher: &lifecyclePublisherStub{err: cause},
		Finalizer: &lifecycleFinalizerStub{},
	}, Config{
		UploadPolicy:  assetop.UploadPolicy{Enabled: true},
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{
		ID:           "asset-1",
		LocalPath:    "/tmp/asset.mp4",
		RequireDrive: true,
		Destination:  delivery.DestinationYouTubeClip,
	}, "hash")
	if result == nil {
		t.Fatal("result = nil, want partial operational result")
	}
	if result.OK || result.Status != "" {
		t.Fatalf("result = %#v, want non-domain failure without success status", result)
	}
	if !errors.Is(err, ErrDriveUploadFailed) || !errors.Is(err, cause) {
		t.Fatalf("err = %v, want ErrDriveUploadFailed and publisher cause", err)
	}
}

func TestProcessAsset_RequiredPublisherNilResultReturnsError(t *testing.T) {
	svc := NewService(ServiceDeps{
		Publisher: &lifecyclePublisherStub{nilResult: true},
		Finalizer: &lifecycleFinalizerStub{},
	}, Config{
		UploadPolicy:  assetop.UploadPolicy{Enabled: true},
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{
		ID:           "asset-1",
		LocalPath:    "/tmp/asset.mp4",
		RequireDrive: true,
	}, "hash")
	if result == nil {
		t.Fatal("result = nil, want partial operational result")
	}
	if !errors.Is(err, ErrDriveUploadFailed) {
		t.Fatalf("err = %v, want ErrDriveUploadFailed", err)
	}
}

func TestProcessAsset_DedupeStoreMissingReturnsError(t *testing.T) {
	svc := NewService(ServiceDeps{}, Config{
		DuplicatePolicy: assetop.DuplicatePolicy{Enabled: true, CheckByHash: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{ID: "asset-store-missing"}, "hash")
	if result == nil {
		t.Fatal("result = nil, want partial operational result")
	}
	if !errors.Is(err, ErrAssetStoreUnavailable) {
		t.Fatalf("err = %v, want ErrAssetStoreUnavailable", err)
	}
}

func TestProcessAsset_RequiredPublisherMissingReturnsError(t *testing.T) {
	svc := NewService(ServiceDeps{Finalizer: &lifecycleFinalizerStub{}}, Config{
		UploadPolicy:  assetop.UploadPolicy{Enabled: true},
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{
		ID:           "asset-1",
		LocalPath:    "/tmp/asset.mp4",
		RequireDrive: true,
	}, "hash")
	if result == nil {
		t.Fatal("result = nil, want partial operational result")
	}
	if !errors.Is(err, ErrDrivePublisherUnavailable) {
		t.Fatalf("err = %v, want ErrDrivePublisherUnavailable", err)
	}
}

func TestProcessAsset_ProcessedDomainStatusReturnsNilError(t *testing.T) {
	svc := NewService(ServiceDeps{}, lifecycleNoPersistenceConfig())

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{ID: "asset-processed"}, "hash")
	if err != nil {
		t.Fatalf("err = %v, want nil for domain success", err)
	}
	if result == nil || !result.OK || result.Status != "processed" {
		t.Fatalf("result = %#v, want OK=true and status processed", result)
	}
}

func TestProcessAsset_SkippedDuplicateDomainStatusReturnsNilError(t *testing.T) {
	store := &lifecycleStoreStub{existing: &assetop.AssetRecord{
		ID:            "existing-asset",
		DriveLink:     "https://drive.test/existing",
		DriveFileID:   "drive-existing",
		DownloadLink:  "https://drive.test/download",
		LegacyFileMD5: "hash",
	}}
	svc := NewService(ServiceDeps{Store: store}, Config{
		DuplicatePolicy: assetop.DuplicatePolicy{Enabled: true, CheckByHash: true, SkipIfExists: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{ID: "asset-duplicate"}, "hash")
	if err != nil {
		t.Fatalf("err = %v, want nil for duplicate domain result", err)
	}
	if result == nil || !result.OK || result.Status != "skipped_duplicate" {
		t.Fatalf("result = %#v, want OK=true and status skipped_duplicate", result)
	}
	if result.DriveFileID != "drive-existing" {
		t.Fatalf("DriveFileID = %q, want existing duplicate value", result.DriveFileID)
	}
}

func TestProcessAsset_DedupeFailureReturnsOperationalError(t *testing.T) {
	cause := errors.New("asset store unavailable")
	svc := NewService(ServiceDeps{Store: &lifecycleStoreStub{findErr: cause}}, Config{
		DuplicatePolicy: assetop.DuplicatePolicy{Enabled: true, CheckByHash: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{ID: "asset-dedupe-error"}, "hash")
	if result == nil {
		t.Fatal("result = nil, want partial operational result")
	}
	if !errors.Is(err, ErrFinalizationFailed) || !errors.Is(err, cause) {
		t.Fatalf("err = %v, want ErrFinalizationFailed and store cause", err)
	}
}

func TestProcessAsset_CommitFailurePreventsDrivePublish(t *testing.T) {
	commitErr := errors.New("canonical commit failed")
	order := []string{}
	publisher := &lifecyclePublisherStub{calls: &order}
	finalizer := &lifecycleFinalizerStub{err: commitErr, failAlways: true, order: &order}
	svc := NewService(ServiceDeps{Publisher: publisher, Finalizer: finalizer}, Config{
		UploadPolicy:  assetop.UploadPolicy{Enabled: true},
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{
		ID: "asset-commit-failure", LocalPath: "/tmp/asset.mp4", RequireDrive: true,
	}, "hash")
	if result == nil {
		t.Fatal("result = nil, want operational result")
	}
	if !errors.Is(err, ErrFinalizationFailed) || !errors.Is(err, commitErr) {
		t.Fatalf("err = %v, want ErrFinalizationFailed and commit cause", err)
	}
	if len(order) != 1 || order[0] != "finalizer:PUBLISH_PENDING" {
		t.Fatalf("order = %#v, want only pending commit before abort", order)
	}
}

func TestProcessAsset_PublisherIdentityMissingPersistsRecoveryState(t *testing.T) {
	order := []string{}
	finalizer := &lifecycleFinalizerStub{order: &order}
	// The default publisher stub returns an identity; this test uses a
	// dedicated publisher below to model a malformed successful response.
	publisherWithNoIdentity := lifecyclePublisherNoIdentityStub{calls: &order}
	svc := NewService(ServiceDeps{Publisher: &publisherWithNoIdentity, Finalizer: finalizer}, Config{
		UploadPolicy:  assetop.UploadPolicy{Enabled: true},
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{ID: "asset-no-identity", LocalPath: "/tmp/asset.mp4", RequireDrive: true}, "hash")
	if result == nil || !errors.Is(err, ErrDriveUploadFailed) {
		t.Fatalf("result=%#v err=%v, want required delivery error", result, err)
	}
	if len(finalizer.records) != 2 || finalizer.records[1].PublishStatus != asset.AssetPublishFailed {
		t.Fatalf("records = %#v, want pending then failed recovery", finalizer.records)
	}
}

type lifecyclePublisherNoIdentityStub struct {
	calls *[]string
}

func (p *lifecyclePublisherNoIdentityStub) Publish(context.Context, delivery.PublishRequest) (*delivery.PublishResult, error) {
	if p.calls != nil {
		*p.calls = append(*p.calls, "publisher")
	}
	return &delivery.PublishResult{}, nil
}

func (p *lifecyclePublisherNoIdentityStub) ResolveFolder(context.Context, delivery.PublishRequest) (string, error) {
	return "", nil
}

func TestProcessAsset_PublishFailurePersistsRecoveryStateAndOrder(t *testing.T) {
	cause := errors.New("drive unavailable")
	order := []string{}
	finalizer := &lifecycleFinalizerStub{order: &order}
	publisher := &lifecyclePublisherStub{err: cause, calls: &order}
	svc := NewService(ServiceDeps{Publisher: publisher, Finalizer: finalizer}, Config{
		UploadPolicy:  assetop.UploadPolicy{Enabled: true},
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{
		ID: "asset-recovery", LocalPath: "/tmp/asset.mp4", RequireDrive: true,
	}, "hash")
	if result == nil {
		t.Fatal("result = nil, want operational result")
	}
	if !errors.Is(err, ErrDriveUploadFailed) || !errors.Is(err, cause) {
		t.Fatalf("err = %v, want ErrDriveUploadFailed and publisher cause", err)
	}
	if result.OK || result.Status != "" {
		t.Fatalf("result = %#v, want no success result on required delivery failure", result)
	}
	wantOrder := []string{"finalizer:PUBLISH_PENDING", "publisher", "finalizer:PUBLISH_FAILED"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("order = %#v, want %#v", order, wantOrder)
	}
	if len(finalizer.records) != 2 || finalizer.records[1].PublishStatus != asset.AssetPublishFailed || finalizer.records[1].Status != "delivery_pending" {
		t.Fatalf("recovery record = %#v, want PUBLISH_FAILED/delivery_pending", finalizer.records)
	}
}

func TestProcessAsset_TerminalCommitFailurePreservesDriveIdentityForRecovery(t *testing.T) {
	commitErr := errors.New("terminal commit failed")
	finalizer := &lifecycleFinalizerStub{err: commitErr, failAt: 2}
	publisher := &lifecyclePublisherStub{}
	svc := NewService(ServiceDeps{Publisher: publisher, Finalizer: finalizer}, Config{
		UploadPolicy:  assetop.UploadPolicy{Enabled: true},
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{
		ID: "asset-terminal-recovery", LocalPath: "/tmp/asset.mp4", RequireDrive: true,
	}, "hash")
	if result == nil || !errors.Is(err, ErrFinalizationFailed) || !errors.Is(err, commitErr) {
		t.Fatalf("result=%#v err=%v, want terminal commit failure", result, err)
	}
	if len(finalizer.records) != 3 {
		t.Fatalf("records = %d, want pending + failed terminal attempt + recovery", len(finalizer.records))
	}
	recovery := finalizer.records[2]
	if recovery.PublishStatus != asset.AssetPublishFailed || recovery.Status != "delivery_pending" {
		t.Fatalf("recovery = %#v, want PUBLISH_FAILED/delivery_pending", recovery)
	}
	if recovery.DriveFileID != "drive-file" || recovery.DriveLink == "" {
		t.Fatalf("recovery = %#v, want preserved Drive identity", recovery)
	}
}

func TestProcessAsset_FinalizerFailureReturnsOperationalError(t *testing.T) {
	cause := errors.New("asset registry unavailable")
	store := &lifecycleStoreStub{upsertErr: cause}
	finalizer := artifacts.NewFinalizer(store, nil, zap.NewNop())
	svc := NewService(ServiceDeps{Store: store, Finalizer: finalizer}, Config{
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{ID: "asset-finalize-error"}, "hash")
	if result == nil {
		t.Fatal("result = nil, want partial operational result")
	}
	if !errors.Is(err, ErrFinalizationFailed) {
		t.Fatalf("err = %v, want ErrFinalizationFailed", err)
	}
	if !strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("err = %v, want finalizer cause %q", err, cause)
	}
}

func TestReconcile_ReconcilerMissingFailsClosed(t *testing.T) {
	svc := NewService(ServiceDeps{}, Config{
		ReconcilePolicy: assetop.ReconcilePolicy{Enabled: true},
	})

	count, err := svc.Reconcile(context.Background(), "images")
	if count != 0 {
		t.Fatalf("count = %d, want 0 when reconciler is unavailable", count)
	}
	if !errors.Is(err, ErrReconcilerUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrReconcilerUnavailable)", err)
	}
}
