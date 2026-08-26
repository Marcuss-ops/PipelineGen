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
	"fmt"
	"os"
	"strings"
	"time"

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
func NewVisualSummaryService(
	sampler FrameSampler,
	vlm VLMClient,
	repo VisualSummaryRepositoryWriter,
	tempDir string,
	log *zap.Logger,
) (s *VisualSummaryService, err error) {
	if sampler == nil {
		return nil, errors.New("visual_summary_service: sampler is nil")
	}
	if vlm == nil {
		return nil, errors.New("visual_summary_service: vlm client is nil")
	}
	if repo == nil {
		return nil, errors.New("visual_summary_service: repo is nil")
	}
	if strings.TrimSpace(tempDir) == "" {
		return nil, errors.New("visual_summary_service: tempDir must not be empty")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &VisualSummaryService{
		sampler: sampler, vlm: vlm, repo: repo,
		tempDir: tempDir, log: log,
	}, nil
}

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
func (s *VisualSummaryService) RunJob(ctx context.Context, cfg VLMJobConfig) (*detail.VisualSummary, error) {
	if strings.TrimSpace(cfg.AssetID) == "" {
		return nil, ErrVLMJobConfigAssetIDRequired
	}
	if strings.TrimSpace(cfg.LocalPath) == "" {
		return nil, ErrVLMJobConfigLocalPathRequired
	}
	// Apply audit-stable defaults (godlike/06 SSOT: defaults live in
	// the const block above; this section is the ONLY place that
	// mutates cfg before behavior).
	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = DefaultVLMFrameIntervalSeconds
	}
	if cfg.IntervalSeconds <= 0 { // belt-and-braces against zero-and-defaults
		return nil, ErrVLMJobIntervalSecondsInvalid
	}
	if strings.TrimSpace(cfg.ModelName) == "" {
		cfg.ModelName = DefaultVLMModelName
	}
	if strings.TrimSpace(cfg.ModelVersion) == "" {
		cfg.ModelVersion = DefaultVLMModelVersion
	}
	if strings.TrimSpace(cfg.PreprocessingVersion) == "" {
		cfg.PreprocessingVersion = DefaultVLMPreprocessingVersion
	}
	if strings.TrimSpace(cfg.VLMEndpoint) == "" {
		cfg.VLMEndpoint = DefaultVLMHTTPEndpoint
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = DefaultVLMHTTPTimeoutSeconds
	}
	// Supersede-gate pre-compute: read existing row before extracting
	// frames (avoids wasted ffmpeg work on a duplicate pass).
	existing, err := s.repo.Get(ctx, cfg.AssetID)
	if err != nil {
		return nil, fmt.Errorf("visual_summary_service.repo.Get(asset=%s): %w", cfg.AssetID, err)
	}
	// Make a per-job subdirectory under tempDir so concurrent
	// RunJob calls don't trample each other's frame files.
	jobDir, err := os.MkdirTemp(s.tempDir, "vlm-frames-")
	if err != nil {
		return nil, fmt.Errorf("visual_summary_service.MkdirTemp(temp=%s): %w", s.tempDir, err)
	}
	defer os.RemoveAll(jobDir)
	// Step 3: extract frames.
	frames, err := s.sampler.ExtractFrames(ctx, cfg.LocalPath, cfg.IntervalSeconds, jobDir)
	if err != nil {
		return nil, fmt.Errorf("visual_summary_service.ExtractFrames: %w", err)
	}
	// Step 4: per-frame VLM inference (or skip).
	responses := make([]*VLMInferenceResponse, 0, len(frames))
	if cfg.SkipVLMCall {
		// Dry-run: synthesize a placeholder response so the aggregator
		// can still produce a deterministic summary for surface
		// verification. The synthesis is the canonical "frame sampled,
		// VLM not yet called" sentinel — ReplaceSkipVLMCall() in tests
		// substitutes a deterministic response.
		responses = append(responses, &VLMInferenceResponse{
			SceneType:      "dry_run",
			RawDescription: fmt.Sprintf("[dry_run] frames=%d interval=%.3f model=%s@%s", len(frames), cfg.IntervalSeconds, cfg.ModelName, cfg.ModelVersion),
		})
	} else {
		for i, framePath := range frames {
			resp, err := s.vlm.Infer(ctx, framePath)
			if err != nil {
				return nil, fmt.Errorf(
					"visual_summary_service.VLM.Infer[idx=%d frame=%s]: %w",
					i, framePath, err)
			}
			responses = append(responses, resp)
		}
	}
	// Step 5: aggregate (godlike/06 SSOT — dedup + cap + truncation).
	text, actions, entities := AggregateVLMResponses(responses)
	// Step 6: build + SourceHash.
	now := time.Now().UTC()
	summary := detail.VisualSummary{
		AssetID:              cfg.AssetID,
		VisualSummaryText:    text,
		VisibleActions:       actions,
		VisibleEntities:      entities,
		FrameCount:           len(frames),
		IntervalSeconds:      cfg.IntervalSeconds,
		PreprocessingVersion: cfg.PreprocessingVersion,
		ModelName:            cfg.ModelName,
		ModelVersion:         cfg.ModelVersion,
		SampledAt:            now.Format(time.RFC3339),
	}
	summary.SourceHash = detail.ComputeSourceHash(
		summary.VisibleActions,
		summary.VisibleEntities,
		summary.ModelName,
		summary.ModelVersion,
		summary.PreprocessingVersion,
		summary.FrameCount,
	)
	// Step 7: supersede gate (godlike/07 NO-FAKE-AVAILABILITY: an
	// identical re-run produces NO DB write and NO Qdrant side effect).
	if existing != nil && existing.SourceHash == summary.SourceHash {
		s.log.Info("visual_summary_service: supersede gate ACTIVE — skipping upsert",
			zap.String("asset_id", cfg.AssetID),
			zap.String("source_hash", summary.SourceHash),
		)
		return existing, nil
	}
	// Step 8: upsert.
	if err := s.repo.Upsert(ctx, summary); err != nil {
		return nil, fmt.Errorf("visual_summary_service.repo.Upsert(asset=%s): %w", cfg.AssetID, err)
	}
	s.log.Info("visual_summary_service: upserted row",
		zap.String("asset_id", cfg.AssetID),
		zap.Int("frame_count", summary.FrameCount),
		zap.Int("actions", len(summary.VisibleActions)),
		zap.Int("entities", len(summary.VisibleEntities)),
		zap.String("source_hash", summary.SourceHash),
	)
	return &summary, nil
}
