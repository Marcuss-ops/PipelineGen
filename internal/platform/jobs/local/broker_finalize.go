package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"golang.org/x/sync/errgroup"
)

// ErrFinalizerNotConfigured is returned when CompleteWithArtifacts is
// called but the broker was not wired with a JobFinalizer.
var ErrFinalizerNotConfigured = errors.New("broker: JobFinalizer not configured — CompleteWithArtifacts requires the finalization spine")

// finalizePublishConcurrency bounds the per-artifact Drive publication
// fan-out during CompleteWithArtifacts. Artifact publication is sequential
// Drive I/O (~2.5–3 s per artifact on the canonical profiling job), so a
// bounded parallel pool collapses a 23-artifact finalize from ~70 s toward
// ~18–25 s while staying well below Drive API quota ceilings. The bound is
// deliberate: 20+ concurrent uploads would trade sequential latency for
// rate-limit 429s and nondeterministic failures. 4 workers is the measured
// sweet spot from the profiling baselines (3–4 workers); the per-artifact
// Drive idempotency key + ConflictSkip already dedupe identical retries, so
// concurrency never re-uploads the same content. The terminal
// CompleteWithArtifacts single SQLite TX is NOT part of the pool — it runs
// strictly AFTER every publication completes, preserving the atomic
// contract (godlike/07: no partial success).
const finalizePublishConcurrency = 4

// CompleteWithArtifacts finalises a job atomically with its published
// artifacts through the JobFinalizer spine. The command's artifacts and
// events are deserialised into finalization types and passed to the
// finalizer's CompleteWithArtifacts.
//
// The lease is constructed from the broker's knowledge of the job row
// (workerID, leaseID, attempt) combined with the command's expiration
// hint.
func (b *Broker) CompleteWithArtifacts(ctx context.Context, cmd appjobs.CompleteWithArtifactsCommand) ([]string, error) {
	if err := b.ensureJobSession(ctx, cmd.WorkerID, cmd.WorkerSessionID, cmd.JobID, cmd.LeaseID, cmd.ExpectedRevision); err != nil {
		return nil, err
	}
	b.flushPendingProgress(ctx, cmd.JobID)

	// The finalize spine serves two transports: the in-process worker's
	// post_writer_finalize stage (run bound to ctx → operations land in
	// the RunReport) and the worker-broker HTTP RPC (no run bound →
	// MeasureOperation pass-through). Tag the stage so the shared
	// artifact prepare/hash/publish helpers and the completion TX below
	// attribute their operations to post_writer_finalize instead of the
	// neutral publish default.
	ctx = kernobs.WithStage(ctx, kernobs.StageName("post_writer_finalize"))

	if b.finalizer == nil {
		return nil, ErrFinalizerNotConfigured
	}

	// Deserialise artifacts from the command. The wire-format was renamed
	// PublishedArtifacts -> StagedArtifacts in P0-COMPL-5-WIRE-NAMING
	// (July 2026); the Carry value is still json.RawMessage so the unmarshal
	// shape is byte-stable across the rename. The typed StagedArtifactReference
	// surface lives on the Sender-side wire envelope at
	// internal/capabilities/remote/staged_artifact_reference.go (godlike/06 SSOT).
	var artifacts []finalization.PublishedArtifact
	if len(cmd.StagedArtifacts) > 0 {
		var staged remote.StagedArtifacts
		if err := json.Unmarshal(cmd.StagedArtifacts, &staged); err != nil {
			return nil, fmt.Errorf("broker: deserialise staged artifacts: %w", err)
		}
		isStaged := len(staged) > 0 && staged[0] != nil && staged[0].Destination != ""
		if isStaged {
			if b.preparation == nil {
				return nil, fmt.Errorf("broker: staged artifacts require canonical ArtifactPreparation")
			}
			// Phase 1 — synchronous pre-validation. Every staged reference is
			// checked (nil, destination mapping, on-disk path) and converted to
			// its canonical VerifiedArtifact envelope BEFORE any Drive I/O
			// starts, so a malformed manifest aborts deterministically with
			// zero wasted uploads (godlike/07 fail-closed: validation errors
			// surface in input order, never racing with publishers).
			verified := make([]finalization.VerifiedArtifact, 0, len(staged))
			for _, ref := range staged {
				v, err := verifiedFromStagedRef(ctx, ref, cmd.JobID, b.folderResolver)
				if err != nil {
					return nil, err
				}
				verified = append(verified, v)
			}
			// Phase 2 — bounded-parallel Drive publication (finalizePublishConcurrency
			// workers). Each worker validates + hashes + uploads ONE artifact; the
			// per-artifact IdempotencyKey + ConflictSkip make concurrent retries
			// safe (identical content never re-uploads). Results are written into
			// their input index so the published slice keeps the manifest order
			// (deterministic asset/row ordering for the finalizer). errgroup
			// cancels the group ctx on the FIRST failure and Wait blocks until
			// every in-flight worker has returned — no goroutine leak, no silent
			// partial-success (the failed artifact fails the whole job, exactly
			// like the pre-parallel contract).
			artifacts = make([]finalization.PublishedArtifact, len(verified))
			g, pubCtx := errgroup.WithContext(ctx)
			g.SetLimit(finalizePublishConcurrency)
			for i := range verified {
				i, va := i, verified[i]
				g.Go(func() error {
					published, err := b.preparation.Prepare(pubCtx, va)
					if err != nil {
						return fmt.Errorf("broker: publish staged artifact %q: %w", va.ArtifactID, err)
					}
					artifacts[i] = published
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				return nil, err
			}
		} else if err := json.Unmarshal(cmd.StagedArtifacts, &artifacts); err != nil {
			return nil, fmt.Errorf("broker: deserialise published artifacts: %w", err)
		} // Older in-process runners emitted the published envelope before
		// the typed requirement/location cutover. Normalize only that
		// legacy shape at this compatibility boundary; new staged refs
		// above always carry both values explicitly.
		//
		// REMOVAL GATE: delete this normalize loop only after a FULL
		// rollout of runners built post-cutover has been observed emitting
		// zero legacy-shape payloads (isStaged == false while
		// len(cmd.StagedArtifacts) > 0) over a complete benchmark cycle.
		// Until then, dropping normalization turns every in-flight old
		// binary's finalize into a validation failure.
		for i := range artifacts {
			if !artifacts[i].Requirement.Valid() {
				artifacts[i].Requirement = finalization.ArtifactRequirementRequired
			}
			if artifacts[i].Location.Provider == "" {
				artifacts[i].Location = finalization.AssetLocation{
					Provider: "local", FileID: artifacts[i].ArtifactID,
					Action: finalization.PublishCreated,
				}
			}
		}
	}

	// Deserialise outbox events from the command.
	var events []finalization.OutboxEvent
	if len(cmd.OutboxEvents) > 0 {
		if err := json.Unmarshal(cmd.OutboxEvents, &events); err != nil {
			return nil, fmt.Errorf("broker: deserialise outbox events: %w", err)
		}
	}

	// Get the job row to compute the attempt counter and lease expiry.
	j, err := b.jobs.Get(ctx, cmd.JobID)
	if err != nil {
		return nil, fmt.Errorf("broker: get job for finalization: %w", err)
	}
	if j == nil {
		return nil, fmt.Errorf("broker: job %q not found for finalization", cmd.JobID)
	}

	// LeaseExpiry is a *time.Time in the Job struct; default to 30s
	// from now if nil (matches the Claim default).
	leaseExpiresAt := time.Now().UTC().Add(30 * time.Second)
	if j.LeaseExpiry != nil {
		leaseExpiresAt = *j.LeaseExpiry
	}

	req := finalization.FinalizationRequest{
		Lease: finalization.Lease{
			LeaseID:   cmd.LeaseID,
			JobID:     cmd.JobID,
			WorkerID:  cmd.WorkerID,
			Attempt:   j.RetryCount + 1,
			ExpiresAt: leaseExpiresAt,
		},
		Result: finalization.ResultManifest{
			JobID:   cmd.JobID,
			Attempt: j.RetryCount + 1,
			Data:    cmd.ResultData,
		},
		Artifacts: artifacts,
		Events:    events,
	}

	// The single-TX atomic terminal (SUCCEEDED flip + asset records +
	// outbox fanout) is measured as finalize.completion_tx so SQLite
	// contention or finalizer retries are never misreported as
	// unattributed post_writer_finalize time.
	var finResult *finalization.FinalizationResult
	if opErr := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageName("post_writer_finalize"),
		Component: kernobs.ComponentName("finalize"),
		Operation: kernobs.OperationName("completion_tx"),
		Items:     int64(len(artifacts)),
	}, func(opCtx context.Context) error {
		var err error
		finResult, err = b.finalizer.CompleteWithArtifacts(opCtx, req)
		return err
	}); opErr != nil {
		return nil, fmt.Errorf("broker: finalizer.CompleteWithArtifacts: %w", opErr)
	}

	// AZIONE 5 (July 2026): extract canonical AssetIDs from the finalizer
	// result and return them so the HTTP handler can populate the
	// CompleteArtifactsResponse.AssetIDs wire field.
	assetIDs := make([]string, 0, len(finResult.ArtifactRefs))
	for _, ref := range finResult.ArtifactRefs {
		assetIDs = append(assetIDs, ref.AssetID)
	}

	return assetIDs, nil
}

// verifiedFromStagedRef projects one StagedArtifactReference into the
// canonical finalization.VerifiedArtifact envelope consumed by the publish
// spine. It is the synchronous pre-validation + conversion half of the
// staged-artifact pipeline (the publish half runs bounded-parallel): nil
// references, unmapped destinations and missing on-disk paths fail closed
// here, in input order, before any Drive I/O starts.
func verifiedFromStagedRef(ctx context.Context, ref *remote.StagedArtifactReference, jobID string, folderResolver finalization.ArtifactFolderResolver) (finalization.VerifiedArtifact, error) {
	if ref == nil {
		return finalization.VerifiedArtifact{}, fmt.Errorf("broker: nil staged artifact reference")
	}
	kind, err := publishedKind(ref.Destination)
	if err != nil {
		return finalization.VerifiedArtifact{}, err
	}
	requirement := finalization.ArtifactRequirementOptional
	if ref.Required {
		requirement = finalization.ArtifactRequirementRequired
	}
	if strings.TrimSpace(ref.Path) == "" {
		return finalization.VerifiedArtifact{}, fmt.Errorf("broker: staged artifact %q has no local path for canonical publication", ref.ArtifactID)
	}
	verified := finalization.VerifiedArtifact{
		ArtifactID: ref.ArtifactID, Kind: kind, Filename: ref.Filename,
		LocalPath: ref.Path, MIMEType: ref.MIMEType, SizeBytes: ref.SizeBytes,
		SHA256: ref.SHA256, SourceVersion: 1, Requirement: requirement,
		IdempotencyKey: ref.ArtifactID, Source: string(kind),
		ProjectID: ref.DriveGroup, Language: ref.DriveLanguage,
		ArtifactMetadata: ref.ArtifactMetadata,
	}
	if source, ok := ref.ArtifactMetadata["source"].(string); ok && source != "" {
		verified.Source = source
	}
	if raw, ok := ref.ArtifactMetadata["drive_subpath"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				verified.DriveSubpath = append(verified.DriveSubpath, s)
			}
		}
	}
	// RenderingGen overlays publish BELOW the parent video's
	// already-resolved Drive folder (/video/.../overlay/): resolve
	// the video's folder and pin it as the overlay's destination.
	// Nil resolver / no video_id → legacy path-builder behaviour.
	if folderID, ok, err := resolveOverlayParentFolder(ctx, ref.ArtifactMetadata, folderResolver); err != nil {
		return finalization.VerifiedArtifact{}, fmt.Errorf("broker: resolve overlay parent folder: %w", err)
	} else if ok {
		verified.ResolvedFolderID = folderID
		verified.RootFolderResolved = true
	}
	// Script/document destinations require a logical project path;
	// worker manifests do not need to duplicate it for every file.
	// Use the job identity as the stable fallback, while preserving
	// the explicit voiceover group when supplied.
	if verified.ProjectID == "" {
		verified.ProjectID = jobID
	}
	if verified.Language == "" {
		verified.Language = "it"
	}
	return verified, nil
}

// resolveOverlayParentFolder resolves the parent video's already-resolved
// Drive folder for a sidecar artifact (RenderingGen overlay). It is a no-op
// (returns ok=false) when the resolver is not wired or the metadata carries no
// parent video_id. A resolver returning an empty folder is treated as
// "not resolved" (ok=false) so the caller keeps the legacy path-builder route.
func resolveOverlayParentFolder(ctx context.Context, meta map[string]any, resolver finalization.ArtifactFolderResolver) (folderID string, resolved bool, err error) {
	if resolver == nil || meta == nil {
		return "", false, nil
	}
	videoID, _ := meta["video_id"].(string)
	if strings.TrimSpace(videoID) == "" {
		return "", false, nil
	}
	folderID, err = resolver.ResolveArtifactFolder(ctx, videoID)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(folderID) == "" {
		return "", false, nil
	}
	return folderID, true, nil
}

func publishedKind(destination string) (finalization.ArtifactKind, error) {
	switch destination {
	case "script":
		return finalization.KindScript, nil
	case "voiceover":
		return finalization.KindVoiceover, nil
	case "image":
		return finalization.KindImage, nil
	case "youtube_clip":
		return finalization.KindVideo, nil
	case "document", "book":
		return finalization.KindDocument, nil
	case "sound_effect":
		return finalization.KindSoundEffect, nil
	default:
		return "", fmt.Errorf("broker: unsupported staged artifact destination %q", destination)
	}
}

// WithFinalizer threads the canonical JobFinalizer into the broker
// after construction. nil is tolerated — the broker falls back to
// ErrFinalizerNotConfigured at CompleteWithArtifacts time (the typed
// sentinel surfaces the wiring gap to the operator).
//
// Returns the receiver for builder-style chaining at the composition site.
func (b *Broker) WithFinalizer(f finalization.JobFinalizer) *Broker {
	b.finalizer = f
	return b
}

// Coalescer returns the broker's progress coalescer for use by the
// lifecycle StartupStep wiring (PR-Progress / ADR-0002 §D6.4). The
// returned pointer is the same instance the broker holds in its
// Deps - tick-loop startup calls .Start(ctx) on it; tick-loop
// shutdown calls .Stop() on it.
//
// Returns nil when coalescing is disabled (Deps.Coalescer was nil at
// construction time) — the lifecycle.go startup step gates on
// non-nil before launching the ticker goroutine (matches the
// "disable coalescing" escape hatch promised by ADR §D6.4).
//
// Cheap (O(1) field read); safe to call concurrently with Take/Flush
// because the coalescer pointer is immutable post-construction (set
// in New(d Deps)).
func (b *Broker) Coalescer() *ProgressCoalescer {
	return b.coalescer
}
