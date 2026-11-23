package extract

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/plan"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// fakeLocalFS is a configurable LocalFSPort fake for tests.
// Every method is a function field; unconfigured methods return
// a typed "not configured" error.
type fakeLocalFS struct {
	StatFn       func(name string) (fs.FileInfo, error)
	OpenFn       func(name string) (io.ReadCloser, error)
	CreateFn     func(name string) (io.WriteCloser, error)
	MkdirTempFn  func(dir, pattern string) (string, error)
	RemoveFn     func(name string) error
	RemoveAllFn  func(path string) error
	MkdirAllFn   func(path string, perm fs.FileMode) error
	CreateTempFn func(dir, pattern string) (string, io.WriteCloser, error)
	TempDirFn    func() string
}

func (f *fakeLocalFS) Stat(name string) (fs.FileInfo, error) {
	if f.StatFn != nil {
		return f.StatFn(name)
	}
	return nil, fmt.Errorf("fakeLocalFS: Stat(%q) not configured", name)
}

func (f *fakeLocalFS) Open(name string) (io.ReadCloser, error) {
	if f.OpenFn != nil {
		return f.OpenFn(name)
	}
	return nil, fmt.Errorf("fakeLocalFS: Open(%q) not configured", name)
}

func (f *fakeLocalFS) Create(name string) (io.WriteCloser, error) {
	if f.CreateFn != nil {
		return f.CreateFn(name)
	}
	return nil, fmt.Errorf("fakeLocalFS: Create(%q) not configured", name)
}

func (f *fakeLocalFS) MkdirTemp(dir, pattern string) (string, error) {
	if f.MkdirTempFn != nil {
		return f.MkdirTempFn(dir, pattern)
	}
	return "", fmt.Errorf("fakeLocalFS: MkdirTemp(%q, %q) not configured", dir, pattern)
}

func (f *fakeLocalFS) Remove(name string) error {
	if f.RemoveFn != nil {
		return f.RemoveFn(name)
	}
	return fmt.Errorf("fakeLocalFS: Remove(%q) not configured", name)
}

func (f *fakeLocalFS) RemoveAll(path string) error {
	if f.RemoveAllFn != nil {
		return f.RemoveAllFn(path)
	}
	return fmt.Errorf("fakeLocalFS: RemoveAll(%q) not configured", path)
}

func (f *fakeLocalFS) MkdirAll(path string, perm fs.FileMode) error {
	if f.MkdirAllFn != nil {
		return f.MkdirAllFn(path, perm)
	}
	return fmt.Errorf("fakeLocalFS: MkdirAll(%q, %o) not configured", path, perm)
}

func (f *fakeLocalFS) CreateTemp(dir, pattern string) (string, io.WriteCloser, error) {
	if f.CreateTempFn != nil {
		return f.CreateTempFn(dir, pattern)
	}
	return "", nil, fmt.Errorf("fakeLocalFS: CreateTemp(%q, %q) not configured", dir, pattern)
}

func (f *fakeLocalFS) TempDir() string {
	if f.TempDirFn != nil {
		return f.TempDirFn()
	}
	return "/tmp/fake-local-fs"
}

// Compile-time assertion: *fakeLocalFS satisfies LocalFSPort.
var _ core.LocalFSPort = (*fakeLocalFS)(nil)

// newRealishFakeLocalFS returns a *fakeLocalFS that delegates every
// LocalFSPort method to the real OS filesystem. Tests that need the
// cutter to write real files (e.g. fakeSucceedingCutter uses os.WriteFile)
// must wire this so executeCuts can create workspace directories, metadata
// steps can create temp files, and publish steps can stat output paths.
//
// The base fakeLocalFS{} (with no function fields set) remains strict
// fail-closed (godlike/07): unconfigured methods return typed errors.
// Tests that need real I/O explicitly opt in via this constructor.
func newRealishFakeLocalFS() *fakeLocalFS {
	return &fakeLocalFS{
		StatFn: func(name string) (fs.FileInfo, error) {
			return os.Stat(name)
		},
		OpenFn: func(name string) (io.ReadCloser, error) {
			return os.Open(name)
		},
		CreateFn: func(name string) (io.WriteCloser, error) {
			return os.Create(name)
		},
		MkdirTempFn: func(dir, pattern string) (string, error) {
			return os.MkdirTemp(dir, pattern)
		},
		RemoveFn: func(name string) error {
			return os.Remove(name)
		},
		RemoveAllFn: func(path string) error {
			return os.RemoveAll(path)
		},
		MkdirAllFn: func(path string, perm fs.FileMode) error {
			return os.MkdirAll(path, os.FileMode(perm))
		},
		CreateTempFn: func(dir, pattern string) (string, io.WriteCloser, error) {
			f, err := os.CreateTemp(dir, pattern)
			if err != nil {
				return "", nil, err
			}
			return f.Name(), f, nil
		},
		TempDirFn: func() string {
			return os.TempDir()
		},
	}
}

type mockCutter struct {
	lastReq core.CutRequest
	res     core.CutBatchResult
	err     error
	called  int
}

func (m *mockCutter) Cut(ctx context.Context, req core.CutRequest) (core.CutBatchResult, error) {
	m.called++
	m.lastReq = req
	return m.res, m.err
}

type fakeStepRunner struct {
	runInput *core.RunInput
	cfg      core.OrchestratorConfig
	state    *core.RunState
	planner  core.ClipPlanner
	cutter   core.VideoCutter
}

func (f *fakeStepRunner) Cfg() core.OrchestratorConfig                                 { return f.cfg }
func (f *fakeStepRunner) RunInput() *core.RunInput                                     { return f.runInput }
func (f *fakeStepRunner) JobID() string                                                { return "test-job" }
func (f *fakeStepRunner) PolicyVersion() string                                        { return f.cfg.PolicyVersion }
func (f *fakeStepRunner) Planner() core.ClipPlanner                                    { return f.planner }
func (f *fakeStepRunner) SourceStager() acquisition.SourceStager                       { return nil }
func (f *fakeStepRunner) Cutter() core.VideoCutter                                     { return f.cutter }
func (f *fakeStepRunner) Builder() core.ManifestBuilder                                { return nil }
func (f *fakeStepRunner) Writer() core.TransactionalAssetWriter                        { return nil }
func (f *fakeStepRunner) Projection() core.ProjectionPort                              { return nil }
func (f *fakeStepRunner) SourceDurationProbe() core.SourceDurationProbe                { return nil }
func (f *fakeStepRunner) ArtifactPreparation() finalization.ArtifactPreparationService { return nil }
func (f *fakeStepRunner) JobFinalizer() finalization.JobFinalizer                      { return nil }
func (f *fakeStepRunner) RunFingerprint() string                                       { return "test-fingerprint" }
func (f *fakeStepRunner) DestinationReconciler() core.DestinationReconciler            { return nil }
func (f *fakeStepRunner) Log() *zap.Logger                                             { return zap.NewNop() }
func (f *fakeStepRunner) LocalFS() core.LocalFSPort                                    { return newRealishFakeLocalFS() }
func (f *fakeStepRunner) State() *core.RunState                                        { return f.state }
func (f *fakeStepRunner) BatchRepository() core.StockBatchRepository                   { return nil }

// newFakeRunner builds a fakeStepRunner with the given clips,
// clip duration, and policy version. The planner is the
// deterministic planner (never called when clips are present,
// but required for interface satisfaction).
func newFakeRunner(clips []core.ClipSpec, clipDur int, policyVer string) *fakeStepRunner {
	if policyVer == "" {
		policyVer = "test-policy-v1"
	}
	return &fakeStepRunner{
		runInput: &core.RunInput{
			Clips:        clips,
			ClipDuration: clipDur,
			TotalMinutes: 1,
		},
		cfg: core.OrchestratorConfig{
			PolicyVersion: policyVer,
		},
		state:   &core.RunState{},
		planner: plan.NewDeterministicPlanner(),
	}
}

type recordingArtifactPreparation struct {
	artifacts []finalization.VerifiedArtifact
}

func (r *recordingArtifactPreparation) Prepare(_ context.Context, artifact finalization.VerifiedArtifact) (finalization.PublishedArtifact, error) {
	r.artifacts = append(r.artifacts, artifact)
	return finalization.PublishedArtifact{
		ArtifactID: artifact.ArtifactID,
		Location: finalization.AssetLocation{
			Provider:     "drive",
			FileID:       artifact.ArtifactID + "-file",
			WebViewLink:  "https://drive.google.com/file/d/" + artifact.ArtifactID + "/view",
			DownloadLink: "https://drive.google.com/uc?id=" + artifact.ArtifactID,
			FolderID:     "folder-123",
			FolderPath:   "stock/test",
		},
	}, nil
}

func fakeSHA(i int) string {
	const pad = "0123456789abcdef"
	out := make([]byte, 64)
	for k := 0; k < 64; k++ {
		out[k] = pad[(k+i)%len(pad)]
	}
	return string(out)
}

func validLease(jobID string) finalization.Lease {
	return finalization.Lease{
		LeaseID:  "lease-1",
		JobID:    jobID,
		WorkerID: "worker-stock-sync",
		Attempt:  1,
	}
}
