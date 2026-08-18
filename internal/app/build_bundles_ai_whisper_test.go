// Package app — build_bundles_ai_whisper_test.go pins the fail-SOFT
// Whisper composition contract (Aug 2026): when python3 or the bridge
// script is unavailable, BuildAIBundle MUST NOT abort boot. It returns a
// bundle with WhisperTranscriber=nil so the 5-priority acquisition chain
// simply skips the Whisper fallback (godlike/07 no-fake-availability:
// nil port = capability absent, never a fake transcript).
package app

import (
	"context"
	"os"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestBuildAIBundle_WhisperUnavailableDegradesSoftly(t *testing.T) {
	// chdir to a scratch dir WITHOUT scripts/bridges/whisper_transcriber.py
	// so NewWhisperTranscriberAdapter fails its os.Stat gate (the default
	// ScriptPath is relative). resolveMigrationsDir walks up from the
	// source file via runtime.Caller, so migrations still resolve even
	// though the test cwd is not the project root.
	scratch := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err, "getwd")
	require.NoError(t, os.Chdir(scratch), "chdir to scratch dir")
	t.Cleanup(func() { _ = os.Chdir(orig) })

	cfg := minimalConfig(t.TempDir())
	log := zaptest.NewLogger(t)

	dbs, err := wiring.InitDatabases(context.Background(), cfg, log)
	require.NoError(t, err, "initDatabases")
	t.Cleanup(func() {
		if dbs != nil && dbs.Main != nil {
			_ = dbs.Main.Close()
		}
	})

	repos, err := BuildRepoBundle(context.Background(), cfg, dbs, log)
	require.NoError(t, err, "BuildRepoBundle")

	bundle, err := BuildAIBundle(context.Background(), cfg, dbs, log, repos, nil)
	require.NoError(t, err, "Whisper unavailability must degrade softly, not abort boot")
	require.Nil(t, bundle.WhisperTranscriber,
		"WhisperTranscriber must be nil when the bridge is unavailable (the acquisition chain skips the Whisper fallback)")
}
