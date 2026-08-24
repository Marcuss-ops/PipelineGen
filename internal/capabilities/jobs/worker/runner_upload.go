package worker

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// OutputArtifact is the typed per-output declaration a job handler
// may return in handlerResult["output_files"]. Used by the legacy
// uploadOutputsLegacy path for backward compat with handlers that
// pre-date the ArtifactManifest contract.
type OutputArtifact struct {
	AssetID  string `json:"asset_id,omitempty"`
	Path     string `json:"path"`
	Required bool   `json:"required,omitempty"`
}

// ErrArtifactClientRequired surfaces a worker misconfiguration:
// the runner's assetClient is nil AND a non-empty handlerResult
// was produced (so the worker is expected to upload). Pre-P0 #4
// the runner silently returned nil + nil, masking the
// misconfiguration. Post-P0 #4 the runner fail-closes with this
// typed error so the operator dashboard can surface a single
// "asset-client-not-wired" alarm category and the worker
// supervisor can fail-fast the job instead of leaving it
// terminal-reported as SUCCEEDED with zero artifacts.
//
// godlike/07 typed-error contract: the sentinel is errors.Is
// reachable from any caller (no wrap layer) so tests + ops
// tooling can probe via `errors.Is(err, ErrArtifactClientRequired)`.
var ErrArtifactClientRequired = errors.New("worker Runner: assetClient is required when handlerResult is non-empty (P0 #4 fail-closed — silent skip on a real artifact-producing job is a misconfiguration, not a no-op)")

// ErrLegacyUploadPathRemoved is the sentinel returned by the now-disabled
// pre-Blocco-2.2 JSON path-scan upload path. P0 Commit 12 (July 2026)
// killed the output_path / pdf_path / markdown_path / output_files
// scan entirely: handlers that do not emit an ArtifactManifest under
// __artifact_manifest fail-closed at the runner.
var ErrLegacyUploadPathRemoved = errors.New("runner: legacy output_files/output_path/pdf_path/markdown_path upload path removed (P0 Commit 12); emit ArtifactManifest under __artifact_manifest instead")

// sha256File computes the SHA-256 digest of the file at path and returns
// the hex-encoded string. Thin wrapper around job.ComputeSHA256 so the
// worker package doesn't need to import crypto/sha256 directly.
func sha256File(path string) (string, error) {
	return job.ComputeSHA256(filesystem.NewOS(), path)
}

// uploadManifest tries to decode an ArtifactManifest from handlerResult.
// If successful, validates required artifacts, computes SHA-256 digests,
// uploads each file via assetClient, and returns the sender-safe
// RemoteArtifactManifest (no local filesystem paths).
//
// P0 Commit 5 (C5): the canonical emit-side conversion is now
// (*ArtifactManifest).ToRemote, which enforces the V1 schema-version
// gate (rejecting any other schema_version before emit) and the
// required-missing rejection (pre-emit check that all Required
// artefacts have an entry in `uploaded`). The dual type vocabulary
// (Local ArtifactManifest + Remote RemoteArtifactManifest) is locked
// at this conversion boundary so the Sender NEVER sees LocalPath.
//
// If no manifest is found (Decode returns nil, nil), falls back to the
// legacy uploadOutputs path and returns nil, nil so the caller sends the
// raw handlerResult.
//
// Returns an error on: malformed manifest, validation failure, missing
// required artefact on disk, SHA-256 computation failure, upload failure,
// or post-upload ToRemote gate rejection (SchemaVersion!=V1, required
// missing).
//
// P0 #4 (July 2026) — fail-closed split: the historical
// `r.assetClient == nil || len(handlerResult) == 0` short-circuit
// conflated two semantically distinct conditions and silently
// dropped both. The runner now distinguishes:
//
//   - handlerResult empty      ⇒ silent-skip (no upload needed,
//     preserves the legacy contract).
//   - handlerResult non-empty + assetClient nil ⇒ typed error
//     ErrArtifactClientRequired (godlike/07 no-fake-availability:
//     the worker is misconfigured to upload but the broker asked
//     for a non-empty artifact manifest, so the runner must surface
//     the misconfiguration rather than silently dropping).
//
// godlike/06 SSOT: ErrArtifactClientRequired is the worker-package
// owner of the "worker AssetClient is unwired" fact. Placed next
// to ErrLegacyUploadPathRemoved + ErrHandlerNotRegistered in this
// file (worker-internal surface; the AssetClient interface lives
// in tools.go of the same package, so the sentinel does not cross
// the domain/application boundary).
func (r *Runner) uploadManifest(ctx context.Context, jobID string, handlerResult map[string]any) (*job.RemoteArtifactManifest, error) {
	// Order matters: empty handlerResult short-circuits BEFORE the
	// assetClient check so the legacy silent-skip is preserved when
	// both conditions hold (handler did not produce any output; the
	// worker may or may not have an asset client — irrelevant).
	if len(handlerResult) == 0 {
		return nil, nil
	}
	if r.assetClient == nil {
		return nil, fmt.Errorf("runner.uploadManifest: jobID=%q: %w", jobID, ErrArtifactClientRequired)
	}

	manifest, decodeErr := job.Decode(handlerResult)
	if decodeErr != nil {
		return nil, fmt.Errorf("artifact manifest decode: %w", decodeErr)
	}

	if manifest == nil {
		// No manifest key → legacy path.
		if err := r.uploadOutputsLegacy(ctx, jobID, handlerResult); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Manifest path: validate, compute digests, upload.
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("artifact manifest: %w", err)
	}

	uploaded := make(map[string]job.RemoteAssetIDAdapter, len(manifest.Artifacts))

	// Required artefacts: fail closed on any issue.
	for _, a := range manifest.RequiredArtifacts() {
		if _, statErr := os.Stat(a.Path); os.IsNotExist(statErr) {
			return nil, fmt.Errorf("required artifact %q (%s): file not found on disk: %s", a.ID, a.Kind, a.Path)
		}

		sha, shaErr := sha256File(a.Path)
		if shaErr != nil {
			return nil, fmt.Errorf("required artifact %q (%s): %w", a.ID, a.Kind, shaErr)
		}

		// P2.6 forward-pointer (DRIVE-CUTOVER-P0-1): r.assetClient is
		// the **Worker-side sender** abstraction over
		// `jobbrokerclient.Client.UploadFile` (the Creator-side
		// receiver is the remote ArtifactUploader port described in
		// AGENTS.md Pattern 10 — paired but distinct concerns). This
		// is NOT a canonical `delivery.Publisher.Publish` call (which
		// targets Drive media assets via the DestinationRegistry).
		// TRACKED in architecture/current.yaml#PR-DRIVE-008-CUTOVER
		// forward-pointer; deferred to a P0.5 wave that lands the
		// Creator-side ArtifactUploader port abstraction. Today's
		// sites stay unchanged.
		// TODO(P0.5): ArtifactUploader port — NOT delivery.Publisher P0.4 target (grandfathered)
		if uploadErr := r.assetClient.UploadFile(ctx, a.ID, a.Path); uploadErr != nil {
			return nil, fmt.Errorf("upload required artifact %q (%s): %w", a.ID, a.Kind, uploadErr)
		}

		uploaded[a.ID] = job.RemoteAsset{RemoteAssetID: a.ID, SHA256: sha}
	}

	// Non-required artefacts: best-effort upload.
	for _, a := range manifest.Artifacts {
		if a.Required {
			continue // already handled above
		}
		if _, statErr := os.Stat(a.Path); os.IsNotExist(statErr) {
			continue
		}
		sha, shaErr := sha256File(a.Path)
		if shaErr != nil {
			r.log.Warn("non-required artifact SHA-256 failed — skipping",
				zap.String("artifact_id", a.ID), zap.String("kind", a.Kind), zap.Error(shaErr))
			continue
		}
		// P2.6 forward-pointer (DRIVE-CUTOVER-P0-1): see companion
		// comment at the required-artifact loop above.
		// TODO(P0.5): ArtifactUploader port — NOT delivery.Publisher P0.4 target (grandfathered)
		if uploadErr := r.assetClient.UploadFile(ctx, a.ID, a.Path); uploadErr != nil {
			r.log.Warn("non-required artifact upload failed — skipping",
				zap.String("artifact_id", a.ID), zap.String("kind", a.Kind), zap.Error(uploadErr))
			continue
		}
		uploaded[a.ID] = job.RemoteAsset{RemoteAssetID: a.ID, SHA256: sha}
	}

	// Build sender-safe manifest (no local paths) via the C5 canonical
	// ToRemote adapter. ToRemote enforces the V1 gate + required-missing
	// pre-emit check; the runner returns the typed error unwrapped so
	// the caller can errors.Is(err, ErrRemoteSchemaVersionUnsupported).
	return manifest.ToRemote(uploaded)
}

// uploadOutputsLegacy is the disabled pre-Blocco-2.2 JSON path-scan.
// P0 Commit 12 (July 2026) killed it entirely: handlers that emit
// required files MUST go through the canonical ArtifactManifest sidecar
// at __artifact_manifest and the round-trip job.Decode path.
//
// Fail-closed at composition-time: a future caller that bypasses the
// manifest emission and attempts the legacy path gets the typed
// error, surfacing the regression immediately in monitoring rather
// than running on a stale upload cycle that silently scans the wrong
// keys.
func (r *Runner) uploadOutputsLegacy(ctx context.Context, jobID string, handlerResult map[string]any) error {
	if r.log != nil {
		r.log.Error("legacy upload path invoked — handler does not emit ArtifactManifest",
			zap.String("job_id", jobID))
	}
	return fmt.Errorf("job=%s: %w", jobID, ErrLegacyUploadPathRemoved)
}
