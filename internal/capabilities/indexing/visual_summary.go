// Package indexing — visual_summary.go owns the VisualSummary
// orchestrator + the audit-stable defaults + the typed config
// errors + the narrowed repository port.
//
// godlike/06 SSOT: this file owns the SERVICE PORT (one canonical
// type that wires the sampler + VLM client + repository + temp-dir
// cleanup). No two services implement the same shape elsewhere.
//
// godlike/07 NO-FAKE-AVAILABILITY: a failed VLM pass surfaces a
// typed error; a missing row leaves Qdrant keys ABSENT (NOT empty
// string / NOT empty slice); empty inputs at every stage are
// rejected before any HTTP / ffmpeg / DB call.
//
// Split rationale (cx0030 build / cx0031 embed / cx0032 render):
//
//   - visual_summary.go : Orchestrator. Service struct + RunJob +
//     VLMJobConfig + Default* const block +
//     typed errors + the Upsert/Get port.
//   - frame_sampler.go  : [Build]   — FrameSampler port +
//     FFMPEGFrameSampler concrete
//     (FFmpeg frame extraction).
//   - vlm_client.go     : [Embed]   — VLMClient port +
//     HTTPVLMClient concrete +
//     VLMInferenceResponse envelope
//     (Python sidecar HTTP transport).
//   - vlm_aggregator.go : [Render]  — AggregateVLMResponses +
//     sortedKeys helper (pure
//     deterministic aggregation).
//
// The orchestrator wires the three layers via three port fields
// (sampler FrameSampler, vlm VLMClient, repo VisualSummary
// RepositoryWriter) so each layer can be swapped independently in
// tests or future production paths.
//
// Architecture (post-FASE-9):
//
//   - Python sidecar at port 8000 (canonical /vlm/visual-tag endpoint,
//     consumed by scripts/bridges/semantic_tagger/vlm.py) does the
//     per-frame VLM inference and returns a JSON envelope
//     (visual_objects, text_on_screen, raw_description, ...).
//
//   - Go side uses the canonical media execution port, backed by
//     rustexec, to extract 1 PNG frame every interval_seconds from
//     the clip's local_path.
//
//   - For each extracted frame, Go issues an HTTP POST to the Python
//     VLM endpoint with the frame's absolute path; the response is
//     aggregated into one VisualSummary row (deterministic dedup of
//     actions + entities, capped at detail.MaxVisibleItems; longest
//     RawDescription becomes the caption, truncated at
//     detail.MaxVisualSummaryChars).
//
//   - The aggregated row is upserted via the canonical
//     VisualSummaryRepository (internal/platform/sqlite/assets/
//     visual_summary_repository.go). SourceHash is computed via the
//     canonical detail.ComputeSourceHash, so the supersede gate
//     (ReindexVerifier/CLI) can short-circuit identical re-runs.
//
//   - The CLI (cmd/admin/reindex_visual_summary.go) reads the row
//     after upsert and surfaces the six payload keys to AssetData so
//     the next reindex --apply pass pushes them into Qdrant.
package indexing

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Defaults (audit-stable; godlike/06 SSOT) ──────────────────────

const (
	// DefaultVLMFrameIntervalSeconds is the canonical "1 frame every
	// N seconds" sampling cadence when the operator does NOT pin
	// --interval. Matches the capacity-sweep operator workflow which
	// exercised 5-second intervals as the production-shaped benchmark.
	DefaultVLMFrameIntervalSeconds = 5.0

	// DefaultVLMModelName / DefaultVLMModelVersion are the audit-stable
	// pinned VLM identifiers when the operator does NOT supply
	// --model / --model-version. The canonical choice is
	// llava-1.6-7b at the 2026-07-13 checkpoint.
	DefaultVLMModelName    = "llava-1.6-7b"
	DefaultVLMModelVersion = "2026-07-13"

	// DefaultVLMPreprocessingVersion is the canonical
	// "vlm-sampler/<semver>" identifier of the FFmpeg + frame-sampler
	// pipeline. Bumping it forces Qdrant re-indexing (the
	// godlike/06 SSOT "version the projection with preprocessing
	// versions" rule).
	DefaultVLMPreprocessingVersion = "vlm-sampler/v1.0.0"

	// DefaultVLMHTTPEndpoint is the canonical Python VLM sidecar
	// URL (scripts/bridges/semantic_tagger/vlm.py bridge). The
	// actual endpoint serving /vlm/visual-tag lives at port 8000
	// (distinct from the embedding_server at port 8001 which hosts
	// SigLIP / E5 / CLAP).
	DefaultVLMHTTPEndpoint = "http://127.0.0.1:8000"

	// DefaultVLMHTTPTimeoutSeconds is the per-request HTTP
	// timeout for the /vlm/visual-tag call. Generous because the
	// VLM inference itself can take 2-8s per frame on GPU-CPU mix.
	DefaultVLMHTTPTimeoutSeconds = 120
)

// ── Typed errors (godlike/07 fail-closed) ─────────────────────────

var (
	// ErrVLMJobConfigAssetIDRequired: caller passed empty AssetID.
	// Distinct from ErrVLMJobConfigLocalPathRequired so callers can
	// errors.Is-branch on which field is missing.
	ErrVLMJobConfigAssetIDRequired = errors.New(
		"vlm_job_config: AssetID must not be empty")

	// ErrVLMJobConfigLocalPathRequired: caller passed empty LocalPath.
	// FFmpeg.Probe on "" returns an error, but rejecting early
	// prevents the wasted ffmpeg probe call.
	ErrVLMJobConfigLocalPathRequired = errors.New(
		"vlm_job_config: LocalPath must not be empty")

	// ErrVLMJobIntervalSecondsInvalid: caller passed interval <= 0.
	// Distinct from the "use defaults" code path that fires when
	// the caller passed zero (which silently uses
	// DefaultVLMFrameIntervalSeconds). This sentinel only fires
	// when the caller EXPLICITLY passed a negative or non-positive
	// number — the contract rejects negative intervals.
	ErrVLMJobIntervalSecondsInvalid = errors.New(
		"vlm_job_config: interval_seconds must be > 0")
)

// ── Port types ────────────────────────────────────────────────────

// VLMJobConfig is the canonical request shape for one
// frame-sampler + VLM invocation pass on ONE asset's local_path.
// All fields except AssetID and LocalPath have audit-stable defaults
// (the Default* consts above).
type VLMJobConfig struct {
	// AssetID is the canonical media_assets.id — the
	// primary key of the VisualSummary row.
	AssetID string

	// LocalPath is the absolute filesystem path to the source
	// media (mp4 / mov / wav / png / ...).
	// Used by FFmpeg.Probe + FFmpeg.ExtractFrame.
	LocalPath string

	// IntervalSeconds is the per-frame cadence. Must be > 0;
	// defaults to DefaultVLMFrameIntervalSeconds when zero.
	IntervalSeconds float64

	// ModelName / ModelVersion are the VLM identifiers to surface
	// into the row's source_hash (the deterministic fingerprint
	// surface). Defaults to DefaultVLMModelName /
	// DefaultVLMModelVersion when blank.
	ModelName    string
	ModelVersion string

	// PreprocessingVersion is the canonical "vlm-sampler/<semver>"
	// identifier. Defaults to DefaultVLMPreprocessingVersion.
	PreprocessingVersion string

	// VLMEndpoint is the Python sidecar URL. Defaults to
	// DefaultVLMHTTPEndpoint when blank.
	VLMEndpoint string

	// TimeoutSeconds is the per-request HTTP timeout. Defaults to
	// DefaultVLMHTTPTimeoutSeconds when zero.
	TimeoutSeconds int

	// SkipVLMCall is a dry-run / test-mode escape hatch: run the
	// frame-sampler + aggregator pipeline WITHOUT calling the Python
	// VLM endpoint. Used by `--dry-run` mode of the CLI driver.
	SkipVLMCall bool
}

// VisualSummaryRepositoryWriter is the narrowed Upsert/Get port the
// service consumes. Distinct from the full
// assets.VisualSummaryRepository (which also carries Delete +
// List*); the service only writes + reads back for the supersede
// gate. Smaller surface = easier mocking in unit tests.
type VisualSummaryRepositoryWriter interface {
	Upsert(ctx context.Context, summary detail.VisualSummary) error
	Get(ctx context.Context, assetID string) (*detail.VisualSummary, error)
}

// ── VisualSummaryService (orchestrator) ───────────────────────────

// VisualSummaryService orchestrates the frame-sampler + VLM pass +
// upsert. It is the production seam between the reindex CLI and the
// VisualSummaryRepository.
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure surfaces a typed
// error; the supersede gate silently skips the upsert when the
// prospective SourceHash matches the existing row's SourceHash
// (no DB write, no Qdrant side effect, the previous row remains
// canonical).
type VisualSummaryService struct {
	sampler FrameSampler
	vlm     VLMClient
	repo    VisualSummaryRepositoryWriter
	tempDir string
	log     *zap.Logger
}

// NewVisualSummaryService wires the orchestrator. nil log →
// zap.NewNop() (the canonical log-not-required pattern).

// RunJob executes one VLM pass over one asset's local_path.
//
// Flow (godlike/07 fail-closed):
//  1. Apply audit-stable defaults for unset optional fields.
//  2. Read existing row via repo.Get — supersede-gate pre-compute.
//  3. Extract frames via the FrameSampler port (frame_sampler.go).
//  4. For each frame, call vlm.Infer (or skip if cfg.SkipVLMCall).
//  5. Aggregate responses (vlm_aggregator.go).
//  6. Build detail.VisualSummary + compute SourceHash via
//     detail.ComputeSourceHash (canonical owner).
//  7. Supersede gate: existing row's SourceHash matches prospective
//     SourceHash → return existing row, skip upsert.
//  8. Upsert via repo.Upsert.
//
// All-upsert path is enforced by godlike/07: a missing AssetID
// or LocalPath surfaces ErrVLMJobConfigAssetIDRequired /
// ErrVLMJobConfigLocalPathRequired BEFORE any ffmpeg / HTTP / DB
// call.
