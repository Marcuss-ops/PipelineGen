package assetquery

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

func TestDetailsLocalLocation_PrefersPrimary(t *testing.T) {
	d := &Details{
		Locations: []*assets.Location{
			{LocationKind: assets.LocationKindLocal, IsPrimary: false},
			{LocationKind: assets.LocationKindLocal, IsPrimary: true},
		},
	}
	got := d.LocalLocation()
	if got == nil || !got.IsPrimary {
		t.Fatalf("expected primary local, got %+v", got)
	}
}

func TestDetailsLocalLocation_FallbackToNonPrimary(t *testing.T) {
	d := &Details{
		Locations: []*assets.Location{
			{LocationKind: assets.LocationKindDrive, IsPrimary: true},
			{LocationKind: assets.LocationKindLocal},
		},
	}
	got := d.LocalLocation()
	if got == nil || got.LocationKind != assets.LocationKindLocal {
		t.Fatalf("expected local, got %+v", got)
	}
}

func TestDetailsLocalLocation_Absent(t *testing.T) {
	d := &Details{
		Locations: []*assets.Location{
			{LocationKind: assets.LocationKindDrive},
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
		Locations: []*assets.Location{
			nil,
			{LocationKind: assets.LocationKindLocal, IsPrimary: true},
		},
	}
	got := d.LocalLocation()
	if got == nil || !got.IsPrimary {
		t.Fatalf("expected primary local with nil-skipping, got %+v", got)
	}
}

func TestDetailsDriveLocation_PrefersPrimary(t *testing.T) {
	d := &Details{
		Locations: []*assets.Location{
			{LocationKind: assets.LocationKindLocal, IsPrimary: true},
			{LocationKind: assets.LocationKindDrive, IsPrimary: true},
		},
	}
	got := d.DriveLocation()
	if got == nil || got.LocationKind != assets.LocationKindDrive || !got.IsPrimary {
		t.Fatalf("expected primary drive, got %+v", got)
	}
}

func TestDetailsDriveLocation_Absent(t *testing.T) {
	d := &Details{
		Locations: []*assets.Location{
			{LocationKind: assets.LocationKindLocal, IsPrimary: true},
		},
	}
	if got := d.DriveLocation(); got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestDetailsProcessingStep_Found(t *testing.T) {
	d := &Details{
		Processing: []assets.ProcessingRecord{
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
		Processing: []assets.ProcessingRecord{
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
