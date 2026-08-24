// Package internal — helpers.go (PR-GODOBJ-5-COMPLETION-COLLAPSE,
// August 2026).
//
// Shared helpers consumed by both CompleteJobService and
// CompleteWithArtifactsService. Lifted here per godlike/06 SSOT
// one-canonical-owner-per-fact: the C7 implementation of
// codecIDForPayload was duplicated in complete_with_artifacts_service.go
// (line ~580) — a redundant copy that future maintainers could
// drift on. This file is the single canonical owner.
//
// Migration status (godlike/07 EXPAND-phase): the existing per-file
// implementations keep working via package-internal calls (the
// service files now delegate to this package; the duplicate
// declarations are removed). For CUTOVER + final deletion of the
// legacy finalizer.JobFinalizer (PR-GODOBJ-5-FINALIZER-CONTRACT,
// deadline 2026-Q4), the helpers here become the only source of
// truth — see architecture/current.yaml#PR-GODOBJ-5-COMPLETION-COLLAPSE.
package jobs

// CodecIDForPayload pins the canonical codec discriminator for the
// result payload. The canonical ResultCodec enum is owned by the
// C2 compiled-registry surface; this helper returns the stable
// ID for json payloads today (the only codec installed per the
// C1/C2 spec).
//
// Lifted from complete_job_service.go::codecIDForPayload (P0
// Commit 7, July 2026) and the duplicate in
// complete_with_artifacts_service.go (P1 wave Azione 6, July 2026).
// Both service files now delegate to this helper — godlike/06
// SSOT verified by the byte-equivalent round-trip test at
// internal/primitives_test.go::TestCodecIDForPayload_ByteStable.
func CodecIDForPayload(payload []byte) string {
	if len(payload) == 0 {
		return "empty"
	}
	return "json.v1"
}
