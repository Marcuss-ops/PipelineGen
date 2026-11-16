// overlay_plan_rust_fixture_test.go — the checked-in overlay plan goldens under
// testdata/overlay_plan/map_plan are compiler output, not hand-frozen data.
//
// Regenerate them with the real BrainRust overlay compiler:
//
//	UPDATE_OVERLAY_PLAN_FIXTURES=1 go test ./internal/capabilities/scripts \
//	  -run TestRegenerateOverlayPlanFixturesFromRustCompiler -count=1
//
// The parity test runs the same requests through the real compiler whenever the
// semantic-engine binary is available, so a stale golden fails instead of
// silently certifying a planner response that production no longer produces.
package scriptgeneration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

const updateOverlayPlanFixturesEnv = "UPDATE_OVERLAY_PLAN_FIXTURES"

// rustOverlayPlanFixtureCompiler is the test seam used only to produce and
// verify goldens. Production uses rustexec.SemanticEngineAdapter; this helper
// keeps the scripts package independent of the adapter package (which imports
// scripts in production).
type rustOverlayPlanFixtureCompiler struct {
	binary string
}

func newRustOverlayPlanFixtureCompiler(t *testing.T) *rustOverlayPlanFixtureCompiler {
	t.Helper()
	binary := strings.TrimSpace(os.Getenv("VELOX_RUST_SEMANTIC_ENGINE_PATH"))
	if binary == "" {
		binary = filepath.Join("..", "..", "..", "bin", "semantic-engine")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() {
		if os.Getenv(updateOverlayPlanFixturesEnv) == "1" {
			t.Fatalf("overlay plan fixture regeneration requires the semantic-engine binary at %q or VELOX_RUST_SEMANTIC_ENGINE_PATH", binary)
		}
		return nil
	}
	return &rustOverlayPlanFixtureCompiler{binary: binary}
}

func (c *rustOverlayPlanFixtureCompiler) CompileOverlayPlanV2(ctx context.Context, payload json.RawMessage) (*capmodel.OverlayPlan, error) {
	if c == nil || c.binary == "" {
		return nil, fmt.Errorf("overlay plan fixture compiler: semantic-engine binary is not configured")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return nil, fmt.Errorf("overlay plan fixture compiler: compact request: %w", err)
	}
	request := append(compact.Bytes(), '\n')
	cmd := exec.CommandContext(ctx, c.binary)
	cmd.Stdin = bytes.NewReader(request)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("overlay plan fixture compiler: run %s: %w: %s", c.binary, err, strings.TrimSpace(stderr.String()))
	}
	var response struct {
		OK            bool   `json:"ok"`
		Error         string `json:"error"`
		SchemaVersion string `json:"schema_version"`
		Plan          *struct {
			SchemaVersion   string                 `json:"schema_version"`
			PlanID          string                 `json:"plan_id"`
			VideoID         string                 `json:"video_id"`
			ProjectID       string                 `json:"project_id"`
			Language        string                 `json:"language"`
			Width           int                    `json:"width"`
			Height          int                    `json:"height"`
			FPSNum          int                    `json:"fps_num"`
			FPSDen          int                    `json:"fps_den"`
			DurationMS      int64                  `json:"duration_ms"`
			OutputProfileID string                 `json:"output_profile_id"`
			Background      json.RawMessage        `json:"background"`
			Items           []capmodel.OverlayItem `json:"items"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("overlay plan fixture compiler: decode response: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("overlay plan fixture compiler: worker: %s", strings.TrimSpace(response.Error))
	}
	if response.Plan == nil {
		return nil, fmt.Errorf("overlay plan fixture compiler: worker returned no plan")
	}
	if response.SchemaVersion != "editorial_plan.v2" || response.Plan.SchemaVersion != "editorial_plan.v2" {
		return nil, fmt.Errorf("overlay plan fixture compiler: unexpected schema %q/%q", response.SchemaVersion, response.Plan.SchemaVersion)
	}
	out := &capmodel.OverlayPlan{
		SchemaVersion:   capmodel.SchemaVersionPlan,
		PlanID:          response.Plan.PlanID,
		VideoID:         response.Plan.VideoID,
		ProjectID:       response.Plan.ProjectID,
		Language:        response.Plan.Language,
		Width:           response.Plan.Width,
		Height:          response.Plan.Height,
		FPSNum:          response.Plan.FPSNum,
		FPSDen:          response.Plan.FPSDen,
		DurationMS:      response.Plan.DurationMS,
		OutputProfileID: response.Plan.OutputProfileID,
		Items:           response.Plan.Items,
	}
	if background := strings.TrimSpace(string(response.Plan.Background)); background != "" && background != "null" {
		var decoded capmodel.OverlayBackground
		if err := json.Unmarshal(response.Plan.Background, &decoded); err != nil {
			return nil, fmt.Errorf("overlay plan fixture compiler: decode background: %w", err)
		}
		out.Background = &decoded
	}
	return out, nil
}

func writeOverlayPlanFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0o644))
}

// TestRegenerateOverlayPlanFixturesFromRustCompiler is the golden generator.
// It writes both the producer request and the sealed plan emitted by the real
// Rust compiler, so the checked-in pair is exactly what production sends and
// adopts.
func TestRegenerateOverlayPlanFixturesFromRustCompiler(t *testing.T) {
	if os.Getenv(updateOverlayPlanFixturesEnv) != "1" {
		t.Skipf("set %s=1 to regenerate the overlay plan fixtures from the real Rust compiler", updateOverlayPlanFixturesEnv)
	}
	compiler := newRustOverlayPlanFixtureCompiler(t)
	for _, fixture := range mapPlanV2Fixtures(t) {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			request := buildMapPlanV2RequestForFixture(t, fixture)
			writeOverlayPlanFixture(t, filepath.Join(mapPlanV2FixtureDir, fixture.name+".request.json"), request)

			plan, err := compileOverlayPlanWithMotionOffset(context.Background(), fixture.result, fixture.language,
				fixture.canvas, fixture.planID, fixture.videoID, fixture.projectID, fixture.plates, compiler)
			require.NoError(t, err)
			require.NotNil(t, plan)
			writeOverlayPlanFixture(t, filepath.Join(mapPlanV2FixtureDir, fixture.name+".plan.json"), plan)
		})
	}
}

// TestOverlayPlanFixturesMatchRealRustCompiler is the drift gate: every
// checked-in plan must still be the sealed output of the real compiler.
func TestOverlayPlanFixturesMatchRealRustCompiler(t *testing.T) {
	compiler := newRustOverlayPlanFixtureCompiler(t)
	if compiler == nil {
		t.Skip("semantic-engine binary not installed; set VELOX_RUST_SEMANTIC_ENGINE_PATH to verify the overlay plan goldens")
	}
	for _, fixture := range mapPlanV2Fixtures(t) {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			request := buildMapPlanV2RequestForFixture(t, fixture)
			raw, err := json.Marshal(request)
			require.NoError(t, err)
			goldenRequest, goldenPlan := loadMapPlanV2Fixture(t, fixture.name)
			require.JSONEq(t, string(goldenRequest), string(raw), "request golden drifted from the producer")
			require.NotNil(t, goldenPlan, "plan golden is missing")

			live, err := compileOverlayPlanWithMotionOffset(context.Background(), fixture.result, fixture.language,
				fixture.canvas, fixture.planID, fixture.videoID, fixture.projectID, fixture.plates, compiler)
			require.NoError(t, err)
			require.NotNil(t, live)
			liveRaw, err := json.Marshal(live)
			require.NoError(t, err)
			goldenRaw, err := json.Marshal(goldenPlan)
			require.NoError(t, err)
			require.JSONEq(t, string(goldenRaw), string(liveRaw), "plan golden drifted from the real Rust compiler")
		})
	}
}
