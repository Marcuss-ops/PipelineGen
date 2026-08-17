// Package app — adapters_voiceover_split_panic_test.go (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Regression-test 4 groups of constructor panic-invariants for the 5-file
// split of internal/app/adapters_voiceover_use_case.go. Each group
// targets one capability cluster (AUDIO synthesis, DRIVE external I/O,
// REPO/RESOLVER, FINALIZATION sidecar) and pins:
//
//  1. Nil-deps panic message exact-substring (so future drift surfaces
//     as a test failure, NOT a silent skip).
//  2. Happy path returns a non-nil adapter whose type satisfies the
//     canonical narrow port (var _ compile-time pin is implicit at
//     the adapter file via `var _ voiceover.<Port> = (*Adapter)(nil)`).
//
// Per AGENTS.md Pattern 0 (port abstraction layer, June 2026): each
// constructor is a fail-fast composition-root gate; the panic must
// surface at construction time so a wiring error is detected at boot
// (not at the first request that exercises the adapter).
package app

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────
// Cluster 1 — AUDIO synthesis (tts.go).
// Pins: newUseCaseTTSAdapter (nil proc → panic); newUseCaseAudioAdapter
// (nil-safe log: returns adapter with nil log).
// ─────────────────────────────────────────────────────────────────────

func TestSplit_TTSAdapter_PanicInvariants(t *testing.T) {
	t.Run("TTS_NilProc_Panics", func(t *testing.T) {
		require.PanicsWithValue(t,
			"app.adapters_voiceover_use_case: newUseCaseTTSAdapter: proc is required (ttsGenerator)",
			func() { _ = newUseCaseTTSAdapter(nil) },
			"newUseCaseTTSAdapter must panic with the canonical message when proc is nil")
	})
	t.Run("TTS_HappyConstructs", func(t *testing.T) {
		// audioasset.Processor takes *sql.DB; nil-DB is acceptable for
		// the constructor-panic invariant test since the adapter
		// ctor only reads fields, never calls methods on proc.
		proc := &audioasset.Processor{}
		adapter := newUseCaseTTSAdapter(proc)
		require.NotNil(t, adapter, "TTS adapter must be non-nil on valid input")
	})
	t.Run("Audio_NilLogAcceptable", func(t *testing.T) {
		adapter := newUseCaseAudioAdapter(nil, nil)
		require.NotNil(t, adapter, "Audio adapter must be non-nil even with nil log (nil-safe)")
	})
}

// ─────────────────────────────────────────────────────────────────────
// Cluster 2 — DRIVE external I/O (publisher.go).
// Pins: newUseCasePublisherAdapter (nil publisher → panic).
// Azione #9 follow-up (July 2026): newVoiceoverDriveAdapter + DriveUploaderPort
// removed. drive.Admin now satisfies VoiceoverCleanupDriver structurally;
// composition.go passes it directly, no wrapper needed.
// ─────────────────────────────────────────────────────────────────────

func TestSplit_PublisherAdapter_PanicInvariants(t *testing.T) {
	t.Run("Publisher_NilPublisher_Panics", func(t *testing.T) {
		require.PanicsWithValue(t,
			"app.adapters_voiceover_use_case: newUseCasePublisherAdapter: publisher is required (delivery.Publisher)",
			func() { _ = newUseCasePublisherAdapter(nil, nil) },
			"newUseCasePublisherAdapter must panic with the canonical message when publisher is nil")
	})
}

// ─────────────────────────────────────────────────────────────────────
// Cluster 3 — REPO/RESOLVER (repo.go).
// Pins: newUseCaseRepoAdapter (nil repo OR nil db → panic with TWO
// distinct messages); newUseCaseDestResolverAdapter (nil resolver →
// panic); newUseCaseDefaultFolderResolverAdapter (nil-safe: returns
// adapter with empty FolderID).
// ─────────────────────────────────────────────────────────────────────

func TestSplit_RepoAdapter_PanicInvariants(t *testing.T) {
	t.Run("Repo_BothNil_PanicsOnRepoFirst", func(t *testing.T) {
		// Canonical ordering contract: when both repo and db are nil,
		// the repo-nil panic fires first (the function short-circuits).
		// The diagnostic message MUST point at the FIRST missing dep,
		// not the SECOND one — this gives operators the actionable
		// signal to fix the upstream wiring error first.
		require.PanicsWithValue(t,
			"app.adapters_voiceover_use_case: newUseCaseRepoAdapter: repo is required (*sqassets.VoiceoversRepository)",
			func() { _ = newUseCaseRepoAdapter(nil, nil) },
			"newUseCaseRepoAdapter must panic with the canonical message when repo is nil (and short-circuit before db-nil guard)")
	})
	t.Run("Repo_NilDB_PanicsOnDB", func(t *testing.T) {
		// Verify the SECOND panic guard fires when only db is nil
		// (repo supplied, db missing) — guarantees the construction
		// order is repo-then-db both for panic-message diagnostic
		// and for fail-fast composition intent.
		require.PanicsWithValue(t,
			"app.adapters_voiceover_use_case: newUseCaseRepoAdapter: db is required (*sql.DB, used by BeginTx in P1-2)",
			func() { _ = newUseCaseRepoAdapter(&sqassets.VoiceoversRepository{}, nil) },
			"newUseCaseRepoAdapter must panic with the canonical db-is-required message when repo is supplied but db is nil")
	})
	t.Run("Repo_HappyConstructs", func(t *testing.T) {
		// &sql.DB{} zero-valued handle is acceptable here: the
		// ctor only stores db in the struct (no ctor-time dereference);
		// BeginTx is the first method that actually touches db. The
		// test pins happy-path construction; any future refactor
		// that adds a ctor-time db dereference would surface as a
		// runtime panic in this test, NOT a hidden production
		// regression.
		repo := &sqassets.VoiceoversRepository{}
		adapter := newUseCaseRepoAdapter(repo, &sql.DB{})
		require.NotNil(t, adapter, "Repo adapter must be non-nil with valid (repo, db) input")
	})
	t.Run("DestResolver_NilResolver_Constructs", func(t *testing.T) {
		// The adapter is now nil-tolerant: a caller-explicit destination
		// (KindExplicit / KindAuto + FolderID) resolves via
		// ResolveVoiceoverDestination's direct() path WITHOUT consulting
		// the asset.Resolver, so a nil resolver must still construct
		// (group-based routing fails with a typed error at resolve time).
		adapter := newUseCaseDestResolverAdapter(nil)
		require.NotNil(t, adapter, "DestResolver adapter must be non-nil even with nil resolver (explicit destination path works without it)")
		resolved, err := adapter.Resolve(context.Background(), &voiceover.DestinationRequest{
			Kind:     string(voiceover.KindExplicit),
			FolderID: "explicit-folder",
		})
		require.NoError(t, err, "explicit destination must resolve without the asset.Resolver")
		require.NotNil(t, resolved)
		require.Equal(t, "explicit-folder", resolved.FolderID)
	})
	t.Run("DefaultFolderResolver_EmptyFolderID_NilSafe", func(t *testing.T) {
		// Empty driveFolderID is the production case when the
		// deployment lacks a configured voiceover_root_folder.
		// Ctor must NOT panic; the adapter's Resolve returns
		// ("", "", false) to signal missing config.
		adapter := newUseCaseDefaultFolderResolverAdapter("", "")
		require.NotNil(t, adapter, "DefaultFolderResolver must be non-nil even with empty folderID")
	})
}

// ─────────────────────────────────────────────────────────────────────
// Cluster 4 — FINALIZATION sidecars (projection.go).
// Pins: newVoiceoverProjectionAdapter (nil svc → panic);
// newVoiceoverPostCommitVerifierAdapter (nil db → panic).
//
// Note: this cluster's file imports `database/sql` for the *sql.Tx
// parameter type — see PR-VO-ADAPTERS-TYPED-PORT forward-pointer.
// ─────────────────────────────────────────────────────────────────────

func TestSplit_ProjectionAdapter_PanicInvariants(t *testing.T) {
	t.Run("Projection_NilSvc_Panics", func(t *testing.T) {
		require.PanicsWithValue(t,
			"app.adapters_voiceover_use_case: newVoiceoverProjectionAdapter: svc is required (*lifecycle.Service)",
			func() { _ = newVoiceoverProjectionAdapter(nil) },
			"newVoiceoverProjectionAdapter must panic with the canonical message when svc is nil")
	})
	t.Run("PostCommitVerifier_NilDB_Panics", func(t *testing.T) {
		require.PanicsWithValue(t,
			"app.adapters_voiceover_use_case: newVoiceoverPostCommitVerifierAdapter: db is required (*sql.DB)",
			func() { _ = newVoiceoverPostCommitVerifierAdapter(nil) },
			"newVoiceoverPostCommitVerifierAdapter must panic with the canonical message when db is nil")
	})
}
