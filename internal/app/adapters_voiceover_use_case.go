// Package app — voiceover use case adapters orchestrator
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Bridges production concretes under internal/infrastructure/* to the
// 9 canonical narrow ports declared in
// internal/application/voiceover/ports.go. Per AGENTS.md Pattern 0
// (port abstraction layer, June 2026) each adapter is a thin
// bridge; production wiring lives here, NOT inside the voiceover
// package, so voiceover stays free of *infrastructure and *lifecycle
// imports.
//
// File split (godlike/06 one-canonical-owner-per-fact):
//
//   - adapters_voiceover_use_case.go   → this file (orchestrator landmark: package doc only)
//   - adapters_voiceover_tts.go        → TTSProvider + AudioPostProcessor (AUDIO synthesis cluster)
//   - adapters_voiceover_publisher.go  → VoiceoverPublisher + DriveUploaderPort (DRIVE cluster)
//   - adapters_voiceover_repo.go       → VoiceoverRepository + DestinationResolver +
//     VoiceoverDefaultFolderResolver (REPO/RESOLVER cluster;
//     sole canonical owner of heavy *sql.DB / *sql.Tx /
//     BeginTx / ExecContext use per godlike/06 SSOT)
//   - adapters_voiceover_projection.go → LifecycleProjectionUpserter + VoiceoverPostCommitVerifier
//     (FINALIZATION sidecars; imports `database/sql` for the
//     *sql.Tx parameter type required by the canonical port
//     signatures — forward-pointer PR-VO-ADAPTERS-TYPED-PORT
//     abstracts this once a typed envelope lands)
//
// Compile-time pin convention (godlike/06 SSOT): each adapter struct
// on the right side declares `var _ voiceover.<Port> = (*<AdapterStruct>)(nil)`
// in its capability file so drift between the adapter signature and
// the port contract surfaces as a compile error at the file where the
// adapter lives, NOT at the use case Execute call site.
//
// See internal/application/voiceover/ports.go for the canonical 9-port
// surface layout (TTSProvider, AudioPostProcessor, VoiceoverPublisher,
// VoiceoverRepository, DestinationResolver, VoiceoverDefaultFolderResolver,
// LifecycleProjectionUpserter, VoiceoverPostCommitVerifier, DriveUploaderPort).
//
// Production wiring sites: internal/app/build_bundles_voiceover.go
// (BuildVoiceoverBundle + VoiceoverUseCaseDeps construction).
package app
