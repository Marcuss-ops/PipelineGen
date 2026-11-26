package wiring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring/creatorruntime"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	opsapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/operations"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	sqlitejobs "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/jobs"
	sqliteops "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/operations"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlport"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// registryCrossStepState carries dependencies produced by the internal-module
// phase and consumed by later registration phases. It is intentionally a
// short-lived value, not part of RegistryWiring: RegistryWiring is the final
// graph result returned to the server, while this value represents the
// dependency edges used during graph construction.
type registryCrossStepState struct {
	SearchFanOut       search.SearchFanOut
	SearchBackends     *search.BackendRegistry
	SearchAggregator   *search.Aggregator
	IdempotencyHandler gin.HandlerFunc
}

// sourcingHashAdapter was DELETED here on 2026-09-20, together with its
// sourcing.HashPort port (internal/capabilities/assets/sourcing/ports.go).
// The adapter was never constructed anywhere in the tree and HashPort had zero
// consumers — no field, parameter or return value was typed by it. The
// canonical MD5 surface is clips.ClipHashPort, implemented by the clips wiring
// adapter and consumed by clips.BulkUploadWorker.hasher. This preserves one
// owner for the checksum port.

// searxngWebSearchProviderAdapter wraps the existing Ollama WebSearcher
// (backed by SearXNG) as a WebSearchProvider for the multi-provider
// registry. It satisfies scriptports.WebSearchProvider.
type searxngWebSearchProviderAdapter struct{ searcher *ollamaclient.WebSearcher }

func (a *searxngWebSearchProviderAdapter) Name() string { return "searxng" }

func (a *searxngWebSearchProviderAdapter) Search(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	if a == nil || a.searcher == nil {
		return nil, nil
	}
	results, err := a.searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	out := make([]scriptports.WebSearchHit, 0, len(results))
	for _, result := range results {
		out = append(out, scriptports.WebSearchHit{Title: result.Title, URL: result.URL, Content: result.Content})
	}
	return out, nil
}

// Compile-time assertion.
var _ scriptports.WebSearchProvider = (*searxngWebSearchProviderAdapter)(nil)

// Package app — build_stage_drive_bundle.go
//
// Canonical typed sentinel for the Stage→Drive forward-pointer components.
//
// The StageDriveBundle struct and BuildStageDriveBundle func were designed
// as a canonical composition surface for the COMPLETION-CUTOVER-P0 wave.
// They were removed (July 2026) per YAGNI: zero external callers existed
// in production code; the forward-pointer components (ArtifactPreparation +
// WithArtifactsService) have independent composition paths via their own
// build_bundles_* files.
//
// The sentinel below is retained for backward compatibility with
// lifecycle_capability_disabled_test.go (sentinel-identity probe).
// ErrStageDriveInsufficientForCompletion is the typed sentinel for the
// forward-pointer components (P0-COMPL-4-PUBLISH-DEDUPE + P0-COMPL-5-
// SINGLE-BACKBONE). The companion StageDriveBundle struct and typed-nil-
// safe accessors were removed per YAGNI (zero production callers).
//
// P0-COMPL-4-PUBLISH-DEDUPE shipped (commit ca73476d, 2026-07-03).
// P0-COMPL-5-SINGLE-BACKBONE deadline: 2026-08-15.
var ErrStageDriveInsufficientForCompletion = errors.New(
	"stage-drive bundle: forward-pointer components (ArtifactPreparation + WithArtifactsService) require P0-COMPL-5-SINGLE-BACKBONE (deadline 2026-08-15) to ship",
)

// POSTGRES-MEDIA-CUTOVER (2026-09-12): the `media.reindex` job binding is
// REMOVED. It was the last reachable path from the job broker into the retired
// clipindexer batch reindex (clipindexer.Service.RegisterJobHandler →
// jobmedia.TypeReindex → HandleJob → IndexClip). The canonical media index
// plane is the PostgreSQL outbox: AssetCommitter emits asset.index.requested,
// PostgresIndexWorker drains it (embed → pgvector). Reindex is requested
// through media_reindex-free surfaces (operator reindex API + backfill CLI),
// both of which enqueue the same canonical event.

// Package app — images job handler late-binding (extracted from
// composition.go NewComposition per PG-028 capability split, July 2026).
// wireImagesJobBinding registers the image generation handler into
// jobs.Service. Extracted from NewComposition per PG-028.
func wireImagesJobBinding(domains *DomainBundle, jobs *JobsBundle) error {
	if domains.ImageService != nil && jobs.Service != nil {
		if err := domains.ImageService.RegisterHandler(jobs.Service); err != nil {
			return fmt.Errorf("images.image_generate_google: %w", err)
		}
	}
	return nil
}

// appendImagesCriticalValidator populates the critical-handler validators
// slice with the images.image_generate_google binding.
// Extracted from NewComposition per PG-028.
func appendImagesCriticalValidator(domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
	if domains.ImageService != nil && jobs.Service != nil {
		img := domains.ImageService
		*validators = append(*validators,
			CriticalHandler{
				Name: "images.image_generate_google",
				Bind: func(svc *appjobs.Service) error {
					return img.RegisterHandler(svc)
				},
			},
		)
	}
}

// Package app — voiceover use case adapters orchestrator
// (PR-VO-ADAPTERS-SPLIT, July 2026).
//
// Bridges production concretes under internal/platform/* to the
// 9 canonical narrow ports declared in
// internal/capabilities/voiceover/ports.go. Per AGENTS.md Pattern 0
// (port abstraction layer, June 2026) each adapter is a thin
// bridge; production wiring lives here, NOT inside the voiceover
// package, so voiceover stays free of *infrastructure and *lifecycle
// imports.
//
// File split (godlike/06 one-canonical-owner-per-fact):
//
//   - adapters_voiceover_use_case.go   → this file (orchestrator landmark: package doc only)
//   - adapters_voiceover_tts.go        → TTSProvider + AudioPostProcessor (AUDIO synthesis cluster)
//   - adapters_voiceover_publisher.go  → VoiceoverPublisher (DRIVE cluster)
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
// See internal/capabilities/voiceover/ports.go for the canonical 9-port
// surface layout (TTSProvider, AudioPostProcessor, VoiceoverPublisher,
// VoiceoverRepository, DestinationResolver, VoiceoverDefaultFolderResolver,
// LifecycleProjectionUpserter, VoiceoverPostCommitVerifier).
//
// Production wiring sites: internal/app/build_bundles_voiceover.go
// (BuildVoiceoverBundle + VoiceoverUseCaseDeps construction).

type sqliteTxManager struct {
	db *sql.DB
}

func (m *sqliteTxManager) BeginTx(ctx context.Context) (sqlport.Tx, error) {
	if m == nil || m.db == nil {
		return nil, fmt.Errorf("script submission: tx manager not wired")
	}
	return m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
}

func buildScriptSubmissionService(root *ComposeRoot, log *zap.Logger) (*opsapp.Service, error) {
	if root == nil || root.DB == nil || root.Jobs == nil || root.Jobs.Repo == nil || root.Outbox == nil || root.Outbox.EventsRepo == nil {
		return nil, fmt.Errorf("script submission: required runtime dependencies are nil")
	}
	// operations + jobs + outbox_events form one atomic submission unit and
	// therefore must use the same execution-plane database connection.
	if root.Jobs.DB == nil || root.Jobs.DB.DB == nil {
		return nil, fmt.Errorf("script submission: jobs database is not configured")
	}
	opsRepo := sqliteops.NewSQLiteRepository(root.Jobs.DB.DB)
	txMgr := &sqliteTxManager{db: root.Jobs.DB.DB}
	svc := opsapp.NewService(opsRepo, root.Jobs.Repo, root.Jobs.Repo, root.Outbox.EventsRepo, txMgr, log)
	svc.SetKeyedLocker(newOpsKeyedLockerAdapter())
	return svc, nil
}

func newOpsKeyedLockerAdapter() opsapp.KeyedLocker {
	return opsapp.NewKeyedLocker()
}

// Compile-time assertion: *sqlitejobs.SQLiteStore implements
// BOTH the submission service's JobEnqueuer port AND the
// JobGetter port. Drift in either surface is a build failure,
// not a runtime panic (godlike/06 Pattern 0).
var (
	_ opsapp.TxManager     = (*sqliteTxManager)(nil)
	_ opsapp.JobEnqueuer   = (*sqlitejobs.SQLiteStore)(nil)
	_ opsapp.JobGetter     = (*sqlitejobs.SQLiteStore)(nil)
	_ opsapp.OutboxEmitter = (*outboxevents.Repository)(nil)
)

// CleanupFunc is the type returned by initialization functions for teardown.
type CleanupFunc = creatorruntime.CleanupFunc

// databases is the composition-root view of `storage.DatabaseSet`.
// Exists only to keep the consumer-facing API of composition.go stable
// (every Build*Bundle() takes `*Databases`); the inner state delegates
// to the canonical DatabaseSet opened by `storage.OpenSet` (rule: no
// `sql.Open` outside `internal/platform/sqlite/**`).
//
// `main` and `logs` fields are kept for back-compat with the dozens of
// `dbs.Main.<X>` references in `composition.go` / `shutdown.go` /
// `registry.go` / `dependencies.go`. They are populated from the
// DatabaseSet at construction time; the canonical source of truth is
// `dbs.Set.Primary` / `dbs.Set.Observability`.
//
// Jobs is a separate execution-plane database when the canonical split is
// enabled; Main remains the media control-plane database and Logs remains the
// observability axis.
//
// Compatibility field population rules (godlike/06 SSOT):
//   - dbs.Set: always non-nil after a successful OpenSet.
//   - dbs.Main: dbs.Set.Primary.
//   - dbs.Logs: dbs.Set.Observability.
//   - dbs.DualPool: a true reader/writer pool over the canonical primary file.
//   - dbs.Jobs: non-nil only when the explicit jobs split is enabled.
