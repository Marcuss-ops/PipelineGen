// Package diagnostics — system_prober_stubs.go: the stubFailSet helper
// used by both the canonical ProbeAll's "nil receiver" branch and
// the application-layer stubSystemProber fallback (Step 4 follow-up,
// July 2026).
//
// godlike/06 SSOT: stubFailSet is the SINGLE canonical "every probe
// failed" set. The application-layer stubSystemProber (in
// internal/capabilities/assets/providers/artlist/diagnostics.go)
// hand-rolls a similar but distinct version with Error="stub prober"
// (vs the canonical "stub" string) — both honor godlike/07 fail-closed
// semantics (every probe reported as failed, never fake-availability)
// but live in different packages to keep the cross-layer boundary
// clean.
//
// godlike/07 NO-FAKE-AVAILABILITY §22: this helper never reports a
// passing probe. Every field of the returned ProbeSet is a failed
// ProbeResult with Error starting with "<label>: stub" and Detail
// explaining the "stub" or "nil receiver" reason.
package diagnostics

import (
	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
)

// stubFailSet returns the canonical "every probe failed" set used by
// the fallback stubSystemProber in the application layer (in
// NewDiagnosticsService). Mirrors ProbeAll's stub shape verbatim so
// the wire-shape contract is identical when AdminSystemProber is nil.
//
// Reason is forwarded into Detail so operators can grep for
// "stub_prober_activated" or "nil_receiver" etc.
func stubFailSet(reason string) artlist.ProbeSet {
	f := func(label string) artlist.ProbeResult {
		return artlist.ProbeResult{
			OK:        false,
			Error:     label + ": stub",
			Detail:    "stubSystemProber: " + reason + " (composition root forgot to wire SystemProber; godlike/07 fail-closed)",
			ElapsedMs: 0,
		}
	}
	return artlist.ProbeSet{
		Scraper:           f("scraper"),
		Browser:           f("browser"),
		Session:           f("session"),
		Downloader:        f("downloader"),
		FFmpegBinary:      f("ffmpeg_binary"),
		DriveFolder:       f("drive_folder"),
		SQLiteWritable:    f("sqlite_writable"),
		OutboxDispatcher:  f("outbox_dispatcher"),
		QdrantReachable:   f("qdrant_reachable"),
		EmbeddingProvider: f("embedding_provider"),
	}
}
