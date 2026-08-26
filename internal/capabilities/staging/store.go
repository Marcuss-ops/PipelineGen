// Package staging — store.go (FASE 3 / Push 3.1b, July 2026).
//
// Application-layer port for the FASE 3 Spina Dorsale staging
// step. Push 3.1a-fix established the canonical persistence
// layer (artifact.Repository + the artifact_stages SQLite table);
// Push 3.1b adds the application-layer Service that performs the
// actual staging work (write file + hash + insert) on top of
// that repository.
//
// godlike/06 SSOT: this port + the typed input/output structs
// are the SOLE canonical surface for FASE 3 staging. The
// concrete lives in this package (StoreService) and is
// instantiated at the composition root (internal/app/build_bundles_*.go,
// forward-pointer to Push 3.1c).
//
// godlike/07 fail-closed: every failure mode is a typed sentinel;
// the pipeline rejects bogus inputs (Validate), rejected
// inserts (Repository sentinel chain), and quota/disk overruns
// (forward-pointer to disk-space enforcement) WITHOUT falling
// back to silent defaults. A staging call that returns nil error
// is a SUCCESS: the row is durable, the file is on disk, the
// hash is committed.
//
// Pattern 0 (AGENTS.md): the port is application-layer so the
// consumer (Push 3.1c publisher worker, Push 3.1d finalizer)
// can declare compile-time dependencies without importing
// infrastructure.
package staging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Sentinel errors (godlike/07 typed-error contract) ────────────────
//
// Each sentinel names the rule it enforces so a log scan can
// grep by rule-id without parsing human-readable suffix. The
// artifact.* sentinels (ErrQuotaExceeded, ErrArtifactStageEmpty,
// ErrArtifactStageIDCollision, ErrArtifactStageHashMismatch,
// ErrInvalidArtifactStageID) live in the domain package and are
// surfaced via the typed-error chain when the Repository rejects
// an Insert or when a forward-pointer check (e.g. quota) trips.
var (
	// ErrSourceEmpty is returned by Stage when the inbound reader
	// produces a 0-byte file (the canonical "empty source" failure
	// mode). Surfaces BEFORE the Repository Insert call so the
	// artifact.ArtifactStageEmpty sentinel is not the only signal
	// (this sentinel names the SOURCE layer; the domain sentinel
	// names the REPOSITORY layer; both can wrap in the chain).
	ErrSourceEmpty = errors.New("staging: source reader produced 0 bytes (empty file rejected)")

	// ErrSourceRead is returned when the inbound reader's Read
	// returns a non-EOF, non-nil error. The wrap carries the
	// underlying error so operators can grep logs by the network
	// or filesystem failure class.
	ErrSourceRead = errors.New("staging: source reader returned a non-EOF error during io.Copy")

	// ErrInvalidRequest is returned by Validate when the
	// StageRequest is structurally invalid (empty JobID, empty
	// Mime, invalid Requirement, etc.). Pre-fail-fast on the
	// validate gate; never surfaces from a successful staging call.
	ErrInvalidRequest = errors.New("staging: StageRequest invalid (pre-flight validation rejected)")

	// ErrWorkspacePermission is returned when the workspace dir
	// is not writable (os.MkdirAll / os.Create fails with EACCES
	// or EPERM). The forward-pointer composition root reads the
	// workspace dir from PIPELINEGEN_STAGING_WORKSPACE env var
	// (default /var/lib/pipelinegen/staging); if the operator
	// configures an unwritable dir, the error surfaces here.
	ErrWorkspacePermission = errors.New("staging: workspace directory is not writable (check PIPELINEGEN_STAGING_WORKSPACE)")

	// ErrPathInvalid is returned when the computed LocalPath
	// fails the safe-path check (e.g. contains ".." segments or
	// resolves outside the workspace dir). This is defense-in-
	// depth against a JobID that contains path-traversal
	// characters; the canonical StageID generator SHOULD never
	// produce such inputs but the runtime check is the canonical
	// fail-closed gate (godlike/07 NO-FAKE-AVAILABILITY).
	ErrPathInvalid = errors.New("staging: computed LocalPath fails the safe-path check (JobID may contain traversal characters)")

	// ErrIDGenerator is returned when the injected idGen returns
	// an empty or otherwise invalid stage ID. Forward-pointer:
	// production uses `art_<unix_nano>_<8hex>`; tests can inject
	// a counter for determinism. An empty ID is a programming
	// error and MUST trip a fail-closed sentinel.
	ErrIDGenerator = errors.New("staging: ID generator returned an empty string (canonical format is art_<unix_nano>_<8hex>)")

	// ErrInvalidReceipt is returned by StageReceipt.Validated()
	// when the receipt is structurally malformed (Size<=0 or
	// empty LocalPath). Distinct from ErrSourceEmpty (source-side
	// empty) and ErrPathInvalid (pre-write path-traversal
	// rejection) so log-greppers do not conflate the three
	// failure classes.
	ErrInvalidReceipt = errors.New("staging: StageReceipt is structurally invalid (post-Stage validation rejected)")
)

// mimeFormat is the canonical IANA media-type regex. The
// canonical form is `type/subtype` with optional parameters
// (e.g. `audio/mpeg; codecs=mp3`). The pre-flight Validate
// rejects anything that does not match this pattern.
var mimeFormat = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9!#$&^_\-+.]{0,126}/[a-zA-Z0-9][a-zA-Z0-9!#$&^_\-+.]{0,126}(;.*)?$`)

// ── StageRequest — the input shape ────────────────────────────────────

// StageRequest is the input to Store.Stage. The Content field
// supplies the inbound bytes (any io.Reader — file, network,
// in-memory buffer). JobID identifies the parent job the
// artifact belongs to (canonical `job_<...>` pattern, NOT
// enforced at the application layer; the SQL layer treats it
// as opaque FK-by-convention text). Mime is the canonical IANA
// media type. Requirement is the per-artifact required-vs-
// optional policy (FASE 3 (b)). Destination is the canonical
// delivery.DestinationKey (e.g. `drive:voiceover/test`).
type StageRequest struct {
	Content     io.Reader
	JobID       string
	Mime        string
	Requirement artifact.Requirement
	Destination string
}

// Validate enforces StageRequest invariants BEFORE the Stage
// pipeline runs. Returns the FIRST violation's typed error so
// callers can errors.Is-probe the specific failure class.
func (r StageRequest) Validate() error {
	if strings.TrimSpace(r.JobID) == "" {
		return fmt.Errorf("%w: JobID must be non-empty", ErrInvalidRequest)
	}
	if !mimeFormat.MatchString(r.Mime) {
		return fmt.Errorf("%w: Mime %q does not match canonical type/subtype format", ErrInvalidRequest, r.Mime)
	}
	if !r.Requirement.IsValid() {
		return fmt.Errorf("%w: Requirement %q is not in the canonical set", ErrInvalidRequest, r.Requirement)
	}
	if r.Content == nil {
		return fmt.Errorf("%w: Content is nil (callers MUST supply a non-nil io.Reader)", ErrInvalidRequest)
	}
	return nil
}

// ── StageReceipt — the canonical post-Stage envelope ──────────────────

// StageReceipt is the output of Store.Stage — a typed envelope
// that identifies the staged file on disk + registry. The
// caller holds this receipt until the publisher worker pool
// drains the outbox event + records the PublishedLocation.
//
// Field semantics:
//   - ID: canonical registry entry id (filesystem name + SHA
//     prefix). Stable across calls; safe to log + correlate in
//     dashboards.
//   - Hash: lower-case hex SHA-256 of the staged content.
//     Anchor for idempotent release (same content → same Hash).
//   - Size: file size in bytes from post-write stat.
//   - LocalPath: absolute path to the staged file on local
//     disk. Empty IF a future remote-only stager is wired
//     (forward-pointer; today's stager is FS-backed).
//   - CreatedAt: UTC timestamp at the Stage commit.
type StageReceipt struct {
	ID        string
	Hash      string
	Size      int64
	LocalPath string
	// EventKey is the canonical event_key of the outbox event
	// atomically co-emitted with the artifact_stages row via
	// Repository.InsertWithOutbox (Push 3.1c). Empty on plain
	// legacy Insert paths (forward-pointer); populated whenever
	// the Service uses InsertWithOutbox. Form:
	// `stage:<JobID>:<ID>`.
	EventKey  string
	CreatedAt time.Time
}

// Validated returns nil when the receipt is well-formed. The
// concrete MAY consult this in tests to assert that the
// construction path is canonical. Goes hand-in-hand with
// StageRequest.Validate() so callers can guard both sides.
func (r StageReceipt) Validated() error {
	if r.ID == "" {
		return fmt.Errorf("%w: receipt.ID is empty", ErrIDGenerator)
	}
	if r.Hash == "" {
		return fmt.Errorf("%w: receipt.Hash is empty (content fingerprint required)", ErrIDGenerator)
	}
	if r.Size <= 0 {
		return fmt.Errorf("%w: receipt.Size must be positive (got %d)", ErrInvalidReceipt, r.Size)
	}
	if r.LocalPath == "" {
		return fmt.Errorf("%w: receipt.LocalPath %q is empty (stage must live SOMEWHERE)", ErrInvalidReceipt, r.LocalPath)
	}
	return nil
}

// ── Store — the canonical application-layer port ─────────────────────

// Store is the FASE 3 application-layer staging port. Concrete
// implementations (Push 3.1b: StoreService) satisfy this
// interface (compile-time assertion in service.go). Callers
// from the publisher worker pool + finalizer consume the port
// without importing infrastructure.
//
// godlike/06 SSOT: this interface is the SOLE canonical surface
// for FASE 3 staging. The concrete is built at the composition
// root (internal/app/build_bundles_*.go).
type Store interface {
	// Stage persists the inbound content + computes the SHA-256
	// hash during write (io.MultiWriter pattern, FASE 3 (a)
	// "hash during write"). Pre-flight Validate gates bogus
	// inputs BEFORE the file is touched. The artifact row is
	// INSERTed via the Repository port AFTER the file is durable
	// on disk; a Repository Insert failure triggers a deferred
	// os.Remove of the local file to prevent orphan-on-error.
	//
	// Returns a typed error from the staging/artifact hierarchy
	// on any failure; the receipt is non-nil ONLY on success.
	Stage(ctx context.Context, req StageRequest) (*StageReceipt, error)
}
