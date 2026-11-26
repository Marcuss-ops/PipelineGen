package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type stubDetailsLookup struct {
	details *asset.Details
	err     error
}

func (s stubDetailsLookup) Get(context.Context, string) (*asset.Details, error) {
	return s.details, s.err
}

type stubWorkerOutputs struct {
	rec *WorkerOutputRecord
	err error
}

func (s stubWorkerOutputs) GetByID(context.Context, string) (*WorkerOutputRecord, error) {
	return s.rec, s.err
}

func (s stubWorkerOutputs) MarkUploading(context.Context, string) error { return nil }
func (s stubWorkerOutputs) MarkUploaded(context.Context, string, string) error {
	return nil
}
func (s stubWorkerOutputs) MarkReady(context.Context, string) error { return nil }

func canonicalDetails() *asset.Details {
	return &asset.Details{
		Asset: &asset.Asset{ID: "asset-1", Filename: "ssot.mp4"},
		Locations: []*asset.Location{
			{LocationKind: asset.LocationKindLocal, URI: "/ssot/asset-1.mp4"},
			{LocationKind: asset.LocationKindDrive, AccessURL: "https://drive.example/ssot", DownloadURL: "https://drive.example/ssot?dl=1"},
		},
	}
}

// The canonical media SSOT wins over a stale worker-output projection. Before
// the reordering the SQLite asset index answered first and could serve an
// outdated path for an asset PostgreSQL had already committed.
func TestResolvePrefersCanonicalMediaSSOTOverStaleIndex(t *testing.T) {
	svc := NewAssetTransferService(
		stubWorkerOutputs{rec: &WorkerOutputRecord{LocalPath: "/stale/asset-1.mp4"}},
		stubDetailsLookup{details: canonicalDetails()},
		nil, nil, zap.NewNop(),
	)

	got, err := svc.resolve(context.Background(), "asset-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned no asset")
	}
	if got.LocalPath != "/ssot/asset-1.mp4" {
		t.Fatalf("expected the canonical path, got %q", got.LocalPath)
	}
	if got.DriveLink != "https://drive.example/ssot" {
		t.Fatalf("expected the canonical drive link, got %q", got.DriveLink)
	}
}

// A canonical lookup failure must be an error, not "asset missing": the old code
// discarded it (err == nil guard) and returned (nil, nil).
func TestResolveSurfacesCanonicalLookupError(t *testing.T) {
	svc := NewAssetTransferService(nil, stubDetailsLookup{err: errors.New("postgres unreachable")}, nil, nil, zap.NewNop())

	got, err := svc.resolve(context.Background(), "asset-1")
	if err == nil {
		t.Fatalf("expected the infrastructure error to surface, got asset %#v", got)
	}
	if !strings.Contains(err.Error(), "canonical media lookup") {
		t.Fatalf("error should name the failing lookup, got %v", err)
	}
}

func TestResolveSurfacesWorkerOutputLookupError(t *testing.T) {
	svc := NewAssetTransferService(stubWorkerOutputs{err: errors.New("sqlite busy")}, stubDetailsLookup{}, nil, nil, zap.NewNop())

	if _, err := svc.resolve(context.Background(), "asset-1"); err == nil {
		t.Fatal("expected the worker-output error to surface")
	}
}

// A typed not-found from the canonical store is absence, not failure: resolution
// must continue to the projections.
func TestResolveTreatsTypedNotFoundAsAbsence(t *testing.T) {
	svc := NewAssetTransferService(
		stubWorkerOutputs{rec: &WorkerOutputRecord{LocalPath: "/worker/asset-1.mp4"}},
		stubDetailsLookup{err: asset.ErrNotFound},
		nil, nil, zap.NewNop(),
	)

	got, err := svc.resolve(context.Background(), "asset-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil || got.LocalPath != "/worker/asset-1.mp4" {
		t.Fatalf("expected the worker-output fallback, got %#v", got)
	}
}

func TestResolveReturnsNilOnlyWhenEverySourceIsEmpty(t *testing.T) {
	svc := NewAssetTransferService(stubWorkerOutputs{}, stubDetailsLookup{}, nil, nil, zap.NewNop())

	got, err := svc.resolve(context.Background(), "asset-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no asset, got %#v", got)
	}
	if _, _, err := svc.Download(context.Background(), "asset-1"); err == nil {
		t.Fatal("Download must fail for an absent asset")
	}
}

// releaseStaging must detach from the caller's context but keep a deadline.
type deadlineProbeStager struct {
	hasDeadline bool
}

func (s *deadlineProbeStager) Prepare(context.Context, acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	return &acquisition.PrepareContext{}, nil
}

func (s *deadlineProbeStager) Release(ctx context.Context, _ string) error {
	_, s.hasDeadline = ctx.Deadline()
	return nil
}

func TestReleaseStagingIsDetachedButBounded(t *testing.T) {
	probe := &deadlineProbeStager{}
	if err := releaseStaging(probe, "token"); err != nil {
		t.Fatalf("releaseStaging: %v", err)
	}
	if !probe.hasDeadline {
		t.Fatal("staging release must carry an explicit deadline")
	}
}
