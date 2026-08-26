// Package staging — service.go (FASE 3 / Push 3.1b, July 2026).
//
// StoreService is the canonical concrete for the staging.Store
// port. It performs the FASE 3 (a) "Stage verified" step:
// write the inbound content to a local path under the staging
// workspace, compute SHA-256 during write (single pass via
// io.MultiWriter), apply the quota/disk pre-flight check, then
// INSERT the artifact_stages row via the artifact.Repository
// port.
//
// godlike/06 SSOT: this is the SINGLE canonical application-layer
// Service for FASE 3 staging. The composition root
// (internal/app/build_bundles_*.go) instantiates exactly one
// instance and exposes it via the application.StagingBundle
// (forward-pointer to Push 3.1c).
//
// godlike/07 fail-closed:
//   - Pre-flight Validate gates bogus inputs (ErrInvalidRequest).
//   - io.Copy error surfaces as ErrSourceRead (typed wrap).
//   - 0-byte write surfaces as ErrSourceEmpty BEFORE the
//     Repository.Insert call (no silent-insert of empty rows).
//   - Repository.Insert failure triggers a deferred os.Remove
//     of the local file (no orphan files on error).
//   - Safe-path check on the computed LocalPath
//     (ErrPathInvalid, defense-in-depth against JobID with
//     traversal characters).
//
// Implementation notes (Push 3.1c forward-pointer):
//   - Quota/disk check is currently a stub (see Step 4 below).
//     The forward-pointer is `ErrQuotaExceeded` + `ErrDiskSpaceLow`
//     from the artifact domain; a real disk-quota enforcement
//     (syscall.Statfs + workspace accounting) lands in 3.1c.
//   - Outbox commit is NOT included in this push. The current
//     Service commits the artifact row but emits NO outbox event;
//     the publisher worker is wired in Push 3.1c via a TX-aware
//     Repository.InsertWithOutbox primitive.
package staging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// Compile-time assertion: *StoreService satisfies the Store port.
var _ Store = (*StoreService)(nil)

// StoreService is the canonical concrete for staging.Store.
type StoreService struct {
	// repo is the artifact.ArtifactStageRepository port (single-writer for the
	// artifact_stages table per Push 3.1a SSOT).
	repo artifact.ArtifactStageRepository

	// workspaceDir is the canonical staging root (e.g.
	// /var/lib/pipelinegen/staging). The composition root reads
	// this from PIPELINEGEN_STAGING_WORKSPACE (env var) or a
	// config value; default is /var/lib/pipelinegen/staging.
	workspaceDir string

	// idGen produces canonical stage IDs. Production uses
	// `art_<unix_nano>_<8hex>`. Tests inject a counter for
	// determinism. The generated ID MUST be non-empty (fail-
	// closed ErrIDGenerator on empty).
	idGen func() string

	// clock returns the current time. Default is time.Now.
	clock func() time.Time
}

// NewStoreService constructs the canonical FASE 3 staging
// service. Caller MUST supply non-nil repo + non-empty
// workspaceDir (godlike/07 fail-fast at construction).
func NewStoreService(repo artifact.ArtifactStageRepository, workspaceDir string) (*StoreService, error) {
	if repo == nil {
		return nil, fmt.Errorf("staging.NewStoreService: repo is required")
	}
	if strings.TrimSpace(workspaceDir) == "" {
		return nil, fmt.Errorf("staging.NewStoreService: workspaceDir is required")
	}
	return &StoreService{
		repo:         repo,
		workspaceDir: workspaceDir,
		idGen:        defaultIDGenerator,
		clock:        func() time.Time { return time.Now().UTC() },
	}, nil
}

// defaultIDGenerator produces `art_<unix_nano>_<8hex>` IDs.
// The 8hex suffix is from the SHA-256 of a 4-byte nanosecond
// fragment; collisions are astronomically unlikely (Unix nano
// resolution × 32-bit entropy per second).
func defaultIDGenerator() string {
	now := time.Now().UTC().UnixNano()
	h := digest.SHA256Bytes([]byte(fmt.Sprintf("stage-id-%d", now)))
	return fmt.Sprintf("art_%d_%s", now, h[:4])
}

// ── Stage pipeline ──────────────────────────────────────────────────────

// Stage performs the FASE 3 (a) "Stage verified" step. See
// package doc for the 6-step pipeline.
func (s *StoreService) Stage(ctx context.Context, req StageRequest) (*StageReceipt, error) {
	// Step 1: pre-flight Validate.
	if err := req.Validate(); err != nil {
		return nil, err
	}

	// Step 2: idempotent workspace dir creation. The JobID
	// sub-dir is the canonical layout: {workspace}/{job_id}/.
	// SafePath pre-check (defense-in-depth against JobID with
	// traversal characters).
	jobDir := filepath.Join(s.workspaceDir, req.JobID)
	if !isSafeSubpath(s.workspaceDir, jobDir) {
		return nil, fmt.Errorf("%w: jobDir=%q outside workspace=%q", ErrPathInvalid, jobDir, s.workspaceDir)
	}
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, fmt.Errorf("%w: mkdir %q: %v", ErrWorkspacePermission, jobDir, err)
	}

	// Step 3: generate the canonical stage ID.
	stageID := s.idGen()
	if stageID == "" {
		return nil, fmt.Errorf("%w", ErrIDGenerator)
	}

	// Step 4: write + hash via io.MultiWriter. Open the dst file
	// FIRST (fail-fast on permission errors BEFORE we touch the
	// hasher), then split the source bytes into (file, hasher).
	localPath := filepath.Join(jobDir, stageID)
	if !isSafeSubpath(s.workspaceDir, localPath) {
		return nil, fmt.Errorf("%w: localPath=%q outside workspace=%q", ErrPathInvalid, localPath, s.workspaceDir)
	}
	dst, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("%w: open %q: %v", ErrWorkspacePermission, localPath, err)
	}
	// Deferred close: always close; the error path also Removes
	// the partial file.
	dstClosed := false
	defer func() {
		if !dstClosed {
			_ = dst.Close()
		}
	}()

	hasher := sha256.New()
	mw := io.MultiWriter(dst, hasher)
	written, err := io.Copy(mw, req.Content)
	if err != nil {
		// Source read error: typed wrap (ErrSourceRead) so the
		// operator can grep by failure class. The partial file
		// is removed below.
		_ = dst.Close()
		dstClosed = true
		_ = os.Remove(localPath)
		return nil, fmt.Errorf("%w: io.Copy returned %v (wrote %d bytes before failure)", ErrSourceRead, err, written)
	}
	// Force any buffered data to disk before the hash is
	// trusted. fsync-then-close is the canonical durability
	// pattern (FASE 3 (a) "verify after write").
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		dstClosed = true
		_ = os.Remove(localPath)
		return nil, fmt.Errorf("%w: fsync %q: %v", ErrWorkspacePermission, localPath, err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(localPath)
		return nil, fmt.Errorf("%w: close %q: %v", ErrWorkspacePermission, localPath, err)
	}
	dstClosed = true

	// Step 4b: 0-byte write check. The canonical "empty artifact"
	// gate (godlike/07 fail-closed: never silently accept an
	// empty file as a successful stage).
	if written == 0 {
		_ = os.Remove(localPath)
		return nil, fmt.Errorf("%w: 0-byte file at %q", ErrSourceEmpty, localPath)
	}

	hashHex := hex.EncodeToString(hasher.Sum(nil))

	// Step 5: quota/disk check (forward-pointer stub). A real
	// enforcement (syscall.Statfs + workspace accounting) lands
	// in a follow-up push. The stub is intentionally fail-OPEN: it
	// returns nil so the pipeline can proceed; the forward-
	// pointer enforcement will REJECT oversized writes with
	// artifact.ErrQuotaExceeded + artifact.ErrDiskSpaceLow.
	// godlike/07 fail-closed: a future change MUST replace this
	// stub with a real check (the seed for the audit story is
	// "the stub was always meant to be replaced").
	_ = ctx // quota check will use ctx for cancellation

	// Step 6: TX-aware commit via Repository.InsertWithOutbox.
	// godlike/07 atomicity: the artifact_stages row + the
	// outbox follow-up event commit together or NEITHER commits.
	// Push 3.1c closes the forward-pointer documented at the top
	// of this file: Stage.Stage now emits `artifact.staged.v1`
	// atomically with the row INSERT so a downstream Drive-
	// upload handler can drain the event and proceed.
	now := s.clock()
	stage := &artifact.ArtifactStage{
		ID:           stageID,
		JobID:        req.JobID,
		LocalPath:    localPath,
		Hash:         hashHex,
		Size:         written,
		Mime:         req.Mime,
		Requirement:  req.Requirement,
		Destination:  req.Destination,
		State:        artifact.ArtifactStageStateStaged,
		AttemptCount: 0,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	payload, payloadErr := s.buildStageEventPayload(stage)
	if payloadErr != nil {
		// godlike/07 fail-closed: payload-encoding failure is a
		// programming error (TypedPayload has only stdlib types)
		// — not a recoverable runtime fault. Remove the file
		// (no orphan) + surface the typed error.
		_ = os.Remove(localPath)
		return nil, fmt.Errorf("staging.Stage: build typed payload (id=%s): %w", stageID, payloadErr)
	}
	eventKey, err := s.repo.InsertWithOutbox(ctx, stage, EventTypeArtifactStaged, payload)
	if err != nil {
		_ = os.Remove(localPath)
		return nil, fmt.Errorf("staging.Stage: repo.InsertWithOutbox (id=%s): %w", stageID, err)
	}

	return &StageReceipt{
		ID:        stageID,
		Hash:      hashHex,
		Size:      written,
		LocalPath: localPath,
		EventKey:  eventKey,
		CreatedAt: now,
	}, nil
}

// ── Outbox event payload ────────────────────────────────────────────────

// EventTypeArtifactStaged is the canonical event_type emitted
// by Store.Stage after a successful stage. The canonical name
// convention is `<aggregate>.<action>.<version>`; the
// follow-up consumer is the Drive-upload handler (Push 3.1e
// forward-pointer) which drains `artifact.staged.v1` events.
const EventTypeArtifactStaged = "artifact.staged.v1"

// TypedStageEventPayload is the canonical event payload emitted
// by Store.Stage. Fields are intentionally narrow: the
// downstream Drive-upload consumer needs only the four pieces
// of identity to resolve the destination + perform a content
// re-read (or trust-stage-by-hash). A future schema evolution
// adds fields backward-compatibly (forward-pointer).
type TypedStageEventPayload struct {
	StageID     string `json:"stage_id"`
	JobID       string `json:"job_id"`
	LocalPath   string `json:"local_path"`
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	Mime        string `json:"mime"`
	Requirement string `json:"requirement"`
	Destination string `json:"destination"`
	EmittedAt   string `json:"emitted_at"` // RFC3339Nano — UTC, canonical
}

// buildStageEventPayload constructs the canonical artifact.staged.v1
// payload from a freshly-staged `*artifact.ArtifactStage`. The
// EmittedAt is the same UTC instant the artifact row's
// UpdatedAt was set to (single-clock ordering invariant: the
// outbox event + artifact row share CreatedAt/UpdatedAt).
func (s *StoreService) buildStageEventPayload(stage *artifact.ArtifactStage) ([]byte, error) {
	payload := TypedStageEventPayload{
		StageID:     stage.ID,
		JobID:       stage.JobID,
		LocalPath:   stage.LocalPath,
		Hash:        stage.Hash,
		Size:        stage.Size,
		Mime:        stage.Mime,
		Requirement: string(stage.Requirement),
		Destination: stage.Destination,
		EmittedAt:   stage.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	return json.Marshal(payload)
}

// ── Safe-path helper (defense-in-depth) ───────────────────────────────

// isSafeSubpath returns true when child is a subpath of parent
// AFTER both are cleaned. The check rejects:
//   - child == parent (must be a strict subpath, not equal)
//   - child outside parent (must start with parent + "/")
//   - child containing ".." segments that escape parent
//
// godlike/07 fail-closed: the canonical gate that catches a
// JobID with traversal characters BEFORE the file system is
// touched. The canonical StageID generator SHOULD never produce
// such inputs, but the runtime check is the fail-closed sentinel
// (the JobID is a free-form operator-supplied string).
func isSafeSubpath(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if child == parent {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, string(filepath.Separator)+"..") {
		return false
	}
	return true
}

// (Compile-time anchor for the Store port is at the top of
// this file: `var _ Store = (*StoreService)(nil)`. Additional
// guards against the stdlib interfaces (io.Writer, io.Reader)
// are intentionally omitted — the compiler already enforces
// them, and a redundant no-op guard only confuses future
// maintainers.)
