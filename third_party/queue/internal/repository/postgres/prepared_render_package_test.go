package postgres

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func TestPreparedRenderPackageReferencePersistsInInputManifest(t *testing.T) {
	job := model.Job{
		ID: "job-1",
		PreparedRenderPackage: &model.PreparedRenderPackageRef{
			PackageID: strings.Repeat("a", 64), PlanSHA256: strings.Repeat("b", 64),
			AssetManifestSHA: strings.Repeat("c", 64), TotalFrames: 200, CompilerVersion: "chronon-3.4.1",
			ProfileID: "velox-h264-1080p30-v1",
		},
	}
	row, err := prepareJobRow(job)
	if err != nil {
		t.Fatalf("prepareJobRow: %v", err)
	}
	manifest, err := decodeInputManifest(row.manifest)
	if err != nil {
		t.Fatalf("decodeInputManifest: %v", err)
	}
	if manifest.PreparedRenderPackage == nil || *manifest.PreparedRenderPackage != *job.PreparedRenderPackage {
		t.Fatalf("manifest ref = %+v, want %+v", manifest.PreparedRenderPackage, job.PreparedRenderPackage)
	}
}
