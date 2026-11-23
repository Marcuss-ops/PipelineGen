package publish

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

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

type publishFakeRunner struct {
	runInput     *core.RunInput
	cfg          core.OrchestratorConfig
	state        *core.RunState
	artifactPrep finalization.ArtifactPreparationService
}

func (f *publishFakeRunner) Cfg() core.OrchestratorConfig           { return f.cfg }
func (f *publishFakeRunner) RunInput() *core.RunInput               { return f.runInput }
func (f *publishFakeRunner) JobID() string                          { return "publish-test-job" }
func (f *publishFakeRunner) PolicyVersion() string                  { return f.cfg.PolicyVersion }
func (f *publishFakeRunner) Planner() core.ClipPlanner              { return nil }
func (f *publishFakeRunner) SourceStager() acquisition.SourceStager { return nil }
func (f *publishFakeRunner) Cutter() core.VideoCutter               { return nil }
func (f *publishFakeRunner) Builder() core.ManifestBuilder          { return nil }
func (f *publishFakeRunner) Writer() core.TransactionalAssetWriter  { return nil }
func (f *publishFakeRunner) Projection() core.ProjectionPort        { return nil }
func (f *publishFakeRunner) SourceDurationProbe() core.SourceDurationProbe {
	return nil
}
func (f *publishFakeRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return f.artifactPrep
}
func (f *publishFakeRunner) JobFinalizer() finalization.JobFinalizer           { return nil }
func (f *publishFakeRunner) RunFingerprint() string                            { return "run-fingerprint-123" }
func (f *publishFakeRunner) DestinationReconciler() core.DestinationReconciler { return nil }
func (f *publishFakeRunner) Log() *zap.Logger                                  { return zap.NewNop() }
func (f *publishFakeRunner) LocalFS() core.LocalFSPort                         { return newRealishFakeLocalFS() }
func (f *publishFakeRunner) State() *core.RunState                             { return f.state }
func (f *publishFakeRunner) BatchRepository() core.StockBatchRepository        { return nil }

var _ core.StepRunner = (*publishFakeRunner)(nil)

// ── PR-STOCK-TIMESTAMP-CLIPS Front 3 (July 2026) — shared timestamp leaf ──
//
// The pre-PR bug: StockPublishStep stamped the same PathLeafName on
// every chunk in an explicit-clips run, so all 5-second children from
// the same timestamp block landed in different folders instead of one
// shared timestamp folder. The fix gates the explicit-clips leaf on
// the timestamp parent label, not the child clip title.

// TestPerClipLeafName_SlugFromTitle locks the canonical slug
// derivation for explicit-clips runs. The slug convention matches
// pkg/stockparser (SafeFolderName → ToLower → space-to-hyphen) so
// the parser and the publisher produce byte-equivalent slugs for
// the same Title input.
//
// godlike/07 NO-FAKE-AVAILABILITY: the slug is NEVER "untitled"
// (the pathutil.SafeFolderName all-whitespace fallback); such
// inputs fall through to the start-end literal.
func publishedDrivePathFor(artifactID string) string {
	return "https://drive.google.com/file/d/" + artifactID + "/view"
}

func makePR004LegacyRunner(t *testing.T, runInput *core.RunInput, policyVersion string) *publishFakeRunner {
	t.Helper()
	tmpDir := t.TempDir()
	chunk := filepath.Join(tmpDir, "chunk.mp4")
	if err := os.WriteFile(chunk, []byte("chunk"), 0o644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	prep := &recordingArtifactPreparation{}
	return &publishFakeRunner{
		runInput: runInput,
		cfg:      core.OrchestratorConfig{PolicyVersion: policyVersion},
		state: &core.RunState{
			Plan:          []core.ClipPlan{{SourceID: "https://youtu.be/abc", StartSec: 10, EndSec: 20}},
			ComposedPaths: []string{chunk},
		},
		artifactPrep: prep,
	}
}
