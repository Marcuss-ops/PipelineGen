package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// The materialize fan-out boundary values (default 10, clamp 1..16,
// PIPELINEGEN_TEXTTRACKS_FANOUT) moved to the config SSOT in A2 (October
// 2026): config.MultilingualConfig.TextTracksFanout + FanoutConcurrency,
// pinned by internal/platform/config/multilingual_fanout_test.go. The wiring
// consumes mlCfg.FanoutConcurrency() (build_bundles_texttracks.go) and owns
// no second parser of the same env (godlike/06 one-canonical-owner-per-fact).

// TestResolveCueTranslationConcurrency pins the single-owner rule: the
// per-cue subtitle fan-out width comes from scripts.translation_concurrency
// (the same knob that bounds the scene×language translation phase), and falls
// back to the certified default when the operator leaves it unset.

func TestResolveCueTranslationConcurrency(t *testing.T) {
	if got := resolveCueTranslationConcurrency(nil); got != texttracks.DefaultCueTranslationConcurrency {
		t.Fatalf("nil config = %d, want the certified default %d", got, texttracks.DefaultCueTranslationConcurrency)
	}

	unset := &config.Config{}
	if got := resolveCueTranslationConcurrency(unset); got != texttracks.DefaultCueTranslationConcurrency {
		t.Fatalf("unset knob = %d, want the certified default %d", got, texttracks.DefaultCueTranslationConcurrency)
	}

	configured := &config.Config{Scripts: config.ScriptsConfig{TranslationConcurrency: 3}}
	if got := resolveCueTranslationConcurrency(configured); got != 3 {
		t.Fatalf("configured knob = %d, want 3 (scripts.translation_concurrency)", got)
	}

	negative := &config.Config{Scripts: config.ScriptsConfig{TranslationConcurrency: -1}}
	if got := resolveCueTranslationConcurrency(negative); got != texttracks.DefaultCueTranslationConcurrency {
		t.Fatalf("negative knob = %d, want the certified default %d", got, texttracks.DefaultCueTranslationConcurrency)
	}
}
