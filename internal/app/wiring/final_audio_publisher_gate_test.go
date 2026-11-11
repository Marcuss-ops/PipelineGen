package wiring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDriveUploadGate_IsSharedAcrossPublisherSites is a source-level freeze:
// the process-wide Drive ceiling is only real if ONE gate is built and handed to
// both publisher sites. A refactor that reverts either site to a self-owned gate
// silently multiplies `max_concurrent_drive_uploads` by the number of publishers
// and reopens the cross-job starvation this file documents.
func TestDriveUploadGate_IsSharedAcrossPublisherSites(t *testing.T) {
	chdirToProjectRoot(t)

	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("internal/app/wiring", rel))
		require.NoError(t, err, "read %s", rel)
		return string(raw)
	}

	composition := read("composition.go")
	require.Contains(t, composition, "vowiring.NewDriveUploadGate(cfg.Voiceover)",
		"composition root must build the ONE shared Drive-upload gate")
	require.Contains(t, composition, "DriveUploadGate:",
		"composition root must cache the shared gate on ComposeRoot")

	// The per-item voiceover pipeline is local-only (waste-cut 2026-10-24):
	// it must not construct a publisher at all, so it can neither bypass nor
	// duplicate the process-wide Drive ceiling. Delivery surfaces keep the
	// shared gate through their own publisher construction.
	voiceoverBundle := read("build_bundles_voiceover.go")
	require.NotContains(t, voiceoverBundle, "NewRateLimitedPublisher(",
		"voiceover bundle must NOT build a self-owned gate")
	require.NotContains(t, voiceoverBundle, "NewRateLimitedPublisherWithGate(",
		"local-only per-item voiceover pipeline must not construct a publisher")

	// The final-audio publisher (audio_publish) must reuse the same gate.
	finalAudio := read("script_generation_runtime.go")
	require.Contains(t, finalAudio, "finalAudioDriveGate = root.DriveUploadGate",
		"final-audio publisher must take the shared gate from ComposeRoot")
	require.Contains(t, finalAudio, "vowiring.NewFinalAudioPublisher(",
		"final-audio publisher must be built by the voiceover wiring module")
}
