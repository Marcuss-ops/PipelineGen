package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
)

// ErrFinalizerNotConfigured is returned when CompleteWithArtifacts is
// called but the broker was not wired with a JobFinalizer.
var ErrFinalizerNotConfigured = errors.New("broker: JobFinalizer not configured — CompleteWithArtifacts requires the finalization spine")

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

	if b.finalizer == nil {
		return nil, ErrFinalizerNotConfigured
	}

	// Deserialise artifacts from the command. The wire-format was renamed
	// PublishedArtifacts -> StagedArtifacts in P0-COMPL-5-WIRE-NAMING
	// (July 2026); the Carry value is still json.RawMessage so the unmarshal
	// shape is byte-stable across the rename. The typed StagedArtifactReference
	// surface lives on the Sender-side wire envelope at
	// internal/domain/remote/staged_artifact_reference.go (godlike/06 SSOT).
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
			artifacts = make([]finalization.PublishedArtifact, 0, len(staged))
			for _, ref := range staged {
				if ref == nil {
					return nil, fmt.Errorf("broker: nil staged artifact reference")
				}
				kind, err := publishedKind(ref.Destination)
				if err != nil {
					return nil, err
				}
				requirement := finalization.ArtifactRequirementOptional
				if ref.Required {
					requirement = finalization.ArtifactRequirementRequired
				}
				if strings.TrimSpace(ref.Path) == "" {
					return nil, fmt.Errorf("broker: staged artifact %q has no local path for canonical publication", ref.ArtifactID)
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
				// Script/document destinations require a logical project path;
				// worker manifests do not need to duplicate it for every file.
				// Use the job identity as the stable fallback, while preserving
				// the explicit voiceover group when supplied.
				if verified.ProjectID == "" {
					verified.ProjectID = cmd.JobID
				}
				if verified.Language == "" {
					verified.Language = "it"
				}
				published, err := b.preparation.Prepare(ctx, verified)
				if err != nil {
					return nil, fmt.Errorf("broker: publish staged artifact %q: %w", ref.ArtifactID, err)
				}
				artifacts = append(artifacts, published)
			}
		} else if err := json.Unmarshal(cmd.StagedArtifacts, &artifacts); err != nil {
			return nil, fmt.Errorf("broker: deserialise published artifacts: %w", err)
		}
		// Older in-process runners emitted the published envelope before
		// the typed requirement/location cutover. Normalize only that
		// legacy shape at this compatibility boundary; new staged refs
		// above always carry both values explicitly.
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

	finResult, err := b.finalizer.CompleteWithArtifacts(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("broker: finalizer.CompleteWithArtifacts: %w", err)
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
