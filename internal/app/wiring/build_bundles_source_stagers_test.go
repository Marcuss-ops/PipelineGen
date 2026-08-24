package wiring

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	appacq "github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	infacq "github.com/Marcuss-ops/PipelineGen/internal/platform/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func testAcquisitionConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.Storage.TempDir = filepath.Join(t.TempDir(), "stock_pipeline_staging")
	return cfg
}

func TestWireAcquisitionStagerBuildsCanonicalStager(t *testing.T) {
	stager, err := WireAcquisitionStager(testAcquisitionConfig(t), zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("WireAcquisitionStager: %v", err)
	}
	if _, ok := stager.(*infacq.FilesystemStager); !ok {
		t.Fatalf("stager type = %T, want *FilesystemStager", stager)
	}
}

func TestWireAcquisitionStagerRejectsNilConfig(t *testing.T) {
	stager, err := WireAcquisitionStager(nil, zap.NewNop(), nil)
	if stager != nil || !errors.Is(err, ErrStockPipelineStagerInit) {
		t.Fatalf("stager=%T err=%v; want typed init failure", stager, err)
	}
}

func TestWireAcquisitionStagerFailsClosedWithoutFetch(t *testing.T) {
	stager, err := WireAcquisitionStager(testAcquisitionConfig(t), nil, nil)
	if err != nil {
		t.Fatalf("WireAcquisitionStager: %v", err)
	}
	req := appacq.PrepareRequest{
		Source:         appacq.SourceRef{URL: "https://example.com/source", PolicyVersion: "test"},
		IdempotencyKey: "test-key",
	}
	if _, err := stager.Prepare(context.Background(), req); !errors.Is(err, appacq.ErrAcquisitionPrepareFailed) {
		t.Fatalf("Prepare error = %v; want ErrAcquisitionPrepareFailed", err)
	}
}
