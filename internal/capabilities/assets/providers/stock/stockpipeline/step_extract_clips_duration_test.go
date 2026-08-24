package stockpipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
)

type durationProbeFixture struct {
	duration float64
	err      error
}

func (p durationProbeFixture) ProbeDurationSec(context.Context, string) (float64, error) {
	return p.duration, p.err
}

func TestValidateAndProbeSourceDuration_StrictUnknownFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		probe     SourceDurationProbe
		wantCause string
	}{
		{
			name:      "probe error",
			probe:     durationProbeFixture{err: errors.New("ffprobe unavailable")},
			wantCause: "ffprobe unavailable",
		},
		{
			name:  "zero duration",
			probe: durationProbeFixture{},
		},
		{
			name:  "negative duration",
			probe: durationProbeFixture{duration: -1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeStepRunner{cfg: OrchestratorConfig{StrictDurationValidation: true}}
			staged := &assets.StagedAsset{SourceID: "source-unknown", LocalPath: "/tmp/source.mp4"}
			_, _, err := validateAndProbeSourceDuration(
				context.Background(),
				durationProbeRunner{fakeStepRunner: runner, probe: tc.probe},
				staged.SourceID, staged.LocalPath, staged,
				[]ClipPlan{{SourceID: staged.SourceID, EndSec: 10}},
			)
			if err == nil || !errors.Is(err, ErrStockClipsUnknownDuration) {
				t.Fatalf("err = %v, want ErrStockClipsUnknownDuration", err)
			}
			if tc.wantCause != "" && !containsErrorText(err, tc.wantCause) {
				t.Fatalf("err = %v, want probe cause %q", err, tc.wantCause)
			}
		})
	}
}

func TestValidateAndProbeSourceDuration_NonStrictFixtureSkipsUnknown(t *testing.T) {
	runner := &fakeStepRunner{cfg: OrchestratorConfig{}}
	staged := &assets.StagedAsset{SourceID: "source-fixture", LocalPath: "/tmp/source.mp4"}
	duration, _, err := validateAndProbeSourceDuration(
		context.Background(), runner, staged.SourceID, staged.LocalPath, staged,
		[]ClipPlan{{SourceID: staged.SourceID, EndSec: 10}},
	)
	if err != nil {
		t.Fatalf("err = %v, want nil in non-strict fixture mode", err)
	}
	if duration != 0 {
		t.Fatalf("duration = %v, want 0", duration)
	}
}

// durationProbeRunner overrides only the probe accessor while reusing the
// canonical fake StepRunner for the rest of the validation seam.
type durationProbeRunner struct {
	*fakeStepRunner
	probe SourceDurationProbe
}

func (r durationProbeRunner) SourceDurationProbe() SourceDurationProbe { return r.probe }

func containsErrorText(err error, text string) bool {
	return err != nil && text != "" && strings.Contains(err.Error(), text)
}
