// Package texttracks — fanout.go: canonical post-publish helper
// that enqueues `asset.text.materialize` jobs from any pipeline's
// success seam.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026): the
// TextTrackMaterializer package is shared by YouTube / Artlist /
// Stock / Script / Video pipelines. Before this helper, the
// package's job handler was registered at composition time but
// no pipeline actively emitted `asset.text.materialize` jobs.
// This file introduces the SOLE canonical post-publish enqueue
// helper that pipeline finalizers MUST delegate to
// (godlike/06 SSOT: one canonical owner per fact).
//
// godlike/06 SSOT: this file is the SOLE canonical owner of:
//   - the post-publish → materialize-job dispatch logic
//     (EnqueueMaterializeOne below), AND
//   - the "what does a fanout producer need to dispatch?"
//     narrow port (`MaterializeEnqueuer` interface).
//
// Pipeline finalizers MUST NOT inline the EnqueueRequest
// construction themselves; they call this helper. Renaming or
// restructuring the dispatch surface must happen here and ONLY
// here.
//
// godlike/07 typed-error contract: pipeline consumers match the
// typed sentinel errors via errors.As/Is — the helper returns
// the canonical *ErrInvalidMaterializeRequest for empty-arg
// failures (pre-broker-side) and wraps broker errors in
// "texttracks.fanout.enqueue: %w" form so the error chain is
// observable.
package texttracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"go.uber.org/zap"
)

func mustMarshalMaterializePayload(payload MaterializeJobPayload) []byte {
	b, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("texttracks: marshal materialize payload: %v", err))
	}
	return b
}

// MaterializeEnqueuer is the narrow port surface a fanout
// producer needs from the broker. Defining this interface
// keeps the production wiring explicit (Pipeline X depends on
// the enqueue surface, NOT the full *jobs.Service) AND keeps
// hermetic tests trivial — a stub satisfying the surface is
// one short method.
//
// godlike/06 SSOT: the canonical production implementation is
// *appjobs.Service (the broker's typed Enqueue entry point).
// The compile-time assertion below pins this — a future
// signature drift in appjobs.Service.Enqueue surfaces as a
// build failure here, not as a runtime nil-method-call panic.
type MaterializeEnqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// Compile-time assertion: *jobs.Service (the application-layer
// broker facade) satisfies the MaterializeEnqueuer surface.
// AGENTS.md Pattern 0 build-time lock for signature drift:
// future drift in jobs.Service.Enqueue surfaces as a build
// failure here, not as a runtime nil-method-call panic.
var _ MaterializeEnqueuer = (*jobs.Service)(nil)

// errMissingJobs is the panic message surfaced at construction
// when the broker is nil. Centralized so the message stays
// stable across multiple call sites + the NewService fanout
// wiring site in build_bundles_texttracks.go.
var errMissingJobs = errors.New("texttracks.NewMaterializeFanOut: jobs is required")

// MaterializeFanOut is the canonical post-publish helper.
// Pipelines call EnqueueMaterializeOne immediately after a
// media_assets row + the source track have been durably
// written.
//
// godlike/07 fail-closed: nil enqueuer panics at construction
// (composition-time wiring gap). nil log falls back to
// zap.NewNop() (test ergonomics — log-on-log panics during
// hermetic tests).
type MaterializeFanOut struct {
	jobs                  MaterializeEnqueuer
	log                   *zap.Logger
	defaultSourceLanguage string
}

func (f *MaterializeFanOut) SetDefaultSourceLanguage(language string) {
	if f != nil {
		f.defaultSourceLanguage = language
	}
}

func (f *MaterializeFanOut) DefaultSourceLanguage() string {
	if f == nil {
		return ""
	}
	return f.defaultSourceLanguage
}

// NewMaterializeFanOut constructs the helper. The enqueuer is
// mandatory (the helper has no other dispatch path); the
// logger is nil-safe (defaults to zap.NewNop()).
func NewMaterializeFanOut(jobs MaterializeEnqueuer, log *zap.Logger) *MaterializeFanOut {
	if jobs == nil {
		panic(errMissingJobs.Error())
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &MaterializeFanOut{jobs: jobs, log: log}
}

// EnqueueMaterializeOne enqueues an `asset.text.materialize` job
// for the (asset_id, source_language, source_text_hash) tuple
// with one or more text_kinds (e.g. [TextTrackTranscript] for
// clips with audio-to-text tracks; [TextTrackDescription,
// TextTrackSummary] for assets with metadata-derived text rows).
//
// Idempotency (two-level):
//  1. Broker-side: ActiveKey =
//     `asset.text.materialize:<asset_id>:<source_text_hash>`.
//     jobs.Service.Enqueue's UNIQUE-constraint rescue
//     collapses repeated post-publish enqueues for the same
//     (asset, source) to a single broker job (godlike/07
//     fails-closed; no double materialize run for the same
//     source).
//  2. Materializer-internal: MaterializationKey.matches
//     compares (source_version, model_version) on the
//     existing READY track row per target language
//     (per-language skip; policy.go:40).
//
// godlike/06 SSOT: this helper does NOT compute the source's
// hash (auto-computation would conflict with the source-
// tracker's own hash). The caller passes the precomputed hash
// from the source track row's TextHash field. This is the
// canonical contract — the materializer's Hash contract
// check (materializer.go: source.TextHash != callerHash →
// ErrInvalid) is the regression lock.
//
// Return contract:
//   - *ErrInvalidMaterializeRequest on empty-arg pre-broker
//     failures (fail-closed at the helper boundary).
//   - Wrapped broker error otherwise — the prefix
//     "texttracks.fanout.enqueue" lets callers grep the
//     error chain for the fanout surface specifically.
func (f *MaterializeFanOut) EnqueueMaterializeOne(
	ctx context.Context,
	assetID string,
	sourceLanguage string,
	sourceTextHash string,
	kinds []asset.TextTrackKind,
) error {
	if assetID == "" {
		return &ErrInvalidMaterializeRequest{
			Field:  "asset_id",
			Reason: "asset_id is required",
		}
	}
	if sourceLanguage == "" {
		return &ErrInvalidMaterializeRequest{
			Field:  "source_language",
			Reason: "source_language is required",
		}
	}
	if sourceTextHash == "" {
		return &ErrInvalidMaterializeRequest{
			Field:  "source_text_hash",
			Reason: "source_text_hash is required (caller pre-computes SHA-256 of the source text)",
		}
	}
	if len(kinds) == 0 {
		return &ErrInvalidMaterializeRequest{
			Field:  "text_kinds",
			Reason: "text_kinds must contain at least one kind",
		}
	}

	payload := MaterializeJobPayload{
		AssetID:        assetID,
		SourceLanguage: sourceLanguage,
		SourceTextHash: sourceTextHash,
		TextKinds:      make([]string, 0, len(kinds)),
	}
	for _, k := range kinds {
		payload.TextKinds = append(payload.TextKinds, string(k))
	}

	// The application job service owns the single payload marshal. Passing the
	// typed structure avoids the double-marshal/base64 failure mode.
	activeKey := fmt.Sprintf(
		"asset.text.materialize:%s:%s",
		assetID, sourceTextHash,
	)

	f.log.Info("texttracks.fanout.enqueue",
		zap.String("asset_id", assetID),
		zap.String("source_language", sourceLanguage),
		zap.String("source_text_hash", sourceTextHash),
		zap.Strings("text_kinds", payload.TextKinds),
		zap.String("active_key", activeKey),
	)

	_, err := f.jobs.Enqueue(ctx, &job.EnqueueRequest{
		Type:      asset.TypeTextMaterialize,
		Payload:   payload,
		ActiveKey: activeKey,
	})
	if err != nil {
		f.log.Warn("texttracks.fanout.enqueue.failed",
			zap.String("asset_id", assetID),
			zap.Error(err),
		)
		return fmt.Errorf("texttracks.fanout.enqueue: %w", err)
	}
	return nil
}

// EnqueueAcquireOne schedules source-transcript acquisition followed by the
// configured multilingual materialization. It is used when an artifact has
// been persisted without a source text hash yet: the worker acquires the
// original transcript through the canonical subtitle/Whisper chain, saves it,
// and then translates it into every configured target language.
func (f *MaterializeFanOut) EnqueueAcquireOne(
	ctx context.Context,
	assetID string,
	sourceLanguage string,
	kinds []asset.TextTrackKind,
) error {
	if assetID == "" {
		return &ErrInvalidMaterializeRequest{Field: "asset_id", Reason: "asset_id is required"}
	}
	if sourceLanguage == "" {
		return &ErrInvalidMaterializeRequest{Field: "source_language", Reason: "source_language is required"}
	}
	if len(kinds) == 0 {
		return &ErrInvalidMaterializeRequest{Field: "text_kinds", Reason: "text_kinds must contain at least one kind"}
	}

	payload := MaterializeJobPayload{
		AssetID:        assetID,
		SourceLanguage: sourceLanguage,
		TextKinds:      make([]string, 0, len(kinds)),
	}
	for _, kind := range kinds {
		payload.TextKinds = append(payload.TextKinds, string(kind))
	}

	_, err := f.jobs.Enqueue(ctx, &job.EnqueueRequest{
		Type:      asset.TypeTextMaterialize,
		Payload:   mustMarshalMaterializePayload(payload),
		Priority:  5,
		ActiveKey: fmt.Sprintf("asset.text.acquire:%s:%s", assetID, sourceLanguage),
	})
	if err != nil {
		return fmt.Errorf("texttracks.fanout.enqueue_acquire: %w", err)
	}
	return nil
}
