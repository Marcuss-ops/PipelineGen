package assetquery

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
)

func TestDetailsLocalLocation_PrefersPrimary(t *testing.T) {
	d := &Details{
		Locations: []*asset.Location{
			{LocationKind: asset.LocationKindLocal, IsPrimary: false},
			{LocationKind: asset.LocationKindLocal, IsPrimary: true},
		},
	}
	got := d.LocalLocation()
	if got == nil || !got.IsPrimary {
		t.Fatalf("expected primary local, got %+v", got)
	}
}

func TestDetailsLocalLocation_FallbackToNonPrimary(t *testing.T) {
	d := &Details{
		Locations: []*asset.Location{
			{LocationKind: asset.LocationKindDrive, IsPrimary: true},
			{LocationKind: asset.LocationKindLocal},
		},
	}
	got := d.LocalLocation()
	if got == nil || got.LocationKind != asset.LocationKindLocal {
		t.Fatalf("expected local, got %+v", got)
	}
}

func TestDetailsLocalLocation_Absent(t *testing.T) {
	d := &Details{
		Locations: []*asset.Location{
			{LocationKind: asset.LocationKindDrive},
		},
	}
	if got := d.LocalLocation(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestDetailsLocalLocation_NilDetails(t *testing.T) {
	var d *Details
	if got := d.LocalLocation(); got != nil {
		t.Fatalf("expected nil for nil Details, got %+v", got)
	}
}

func TestDetailsLocalLocation_SkipsNilEntries(t *testing.T) {
	d := &Details{
		Locations: []*asset.Location{
			nil,
			{LocationKind: asset.LocationKindLocal, IsPrimary: true},
		},
	}
	got := d.LocalLocation()
	if got == nil || !got.IsPrimary {
		t.Fatalf("expected primary local with nil-skipping, got %+v", got)
	}
}

func TestDetailsDriveLocation_PrefersPrimary(t *testing.T) {
	d := &Details{
		Locations: []*asset.Location{
			{LocationKind: asset.LocationKindLocal, IsPrimary: true},
			{LocationKind: asset.LocationKindDrive, IsPrimary: true},
		},
	}
	got := d.DriveLocation()
	if got == nil || got.LocationKind != asset.LocationKindDrive || !got.IsPrimary {
		t.Fatalf("expected primary drive, got %+v", got)
	}
}

func TestDetailsDriveLocation_Absent(t *testing.T) {
	d := &Details{
		Locations: []*asset.Location{
			{LocationKind: asset.LocationKindLocal, IsPrimary: true},
		},
	}
	if got := d.DriveLocation(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestDetailsProcessingStep_Found(t *testing.T) {
	d := &Details{
		Processing: []asset.ProcessingRecord{
			{Step: string(asset.StageDownload), Status: asset.StatusCompleted},
			{Step: string(asset.StageIndexing), Status: asset.StatusRunning},
		},
	}
	rec := d.ProcessingStep(string(asset.StageIndexing))
	if rec == nil || rec.Status != asset.StatusRunning {
		t.Fatalf("expected running indexing, got %+v", rec)
	}
}

func TestDetailsProcessingStep_NotFound(t *testing.T) {
	d := &Details{
		Processing: []asset.ProcessingRecord{
			{Step: string(asset.StageDownload), Status: asset.StatusCompleted},
		},
	}
	if rec := d.ProcessingStep("missing"); rec != nil {
		t.Fatalf("expected nil, got %+v", rec)
	}
}

func TestDetailsProcessingStep_NilDetails(t *testing.T) {
	var d *Details
	if rec := d.ProcessingStep(string(asset.StageDownload)); rec != nil {
		t.Fatalf("expected nil for nil Details, got %+v", rec)
	}
}
