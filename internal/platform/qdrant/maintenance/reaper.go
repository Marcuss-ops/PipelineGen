package maintenance

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// DefaultReaperKeys is the default payload-key redaction list for the
// Reaper. QDRANT-005 closure (June 2026): EMPTY by default. The
// previous default included "status", "drive_link", "local_path",
// "download_link" — keys that the canonical manifest and payload
// mapper actively WRITE. Removing "status" in particular was the
// critical fix: status is the canonical search-filter key (the search
// adapter / clip_search_adapter both filter on it), so the reaper
// erasing it was a self-inflicted search outage. drive_link /
// local_path / download_link are likewise load-bearing locator keys
// that some ingest paths still write for round-tripping; the canonical
// payload mapper has been migrated off them (QDRANT-001) but legacy
// points may still carry them and the reaper must not blanket-erase.
//
// Reaper runs are now EXPLICIT-OPT-IN per key. Operators set
// ReaperOptions.Keys to the exact subset they want redaction applied
// to. Production deployments should leave DefaultReaperKeys empty
// unless they have a documented per-key retention policy.
var DefaultReaperKeys = []string{}

// ErrNilClient is returned when a Reaper is constructed with a nil transport.Client.
var ErrNilClient = errors.New("reaper: qdrant client is nil")

const (
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusNoop    = "noop"
	StatusPartial = "partial"
	StatusFailed  = "failed"
)

// MaxReaperBatchSize is the Qdrant REST scroll-page ceiling. The
// QDRANT-005 closure enforces this hard cap (was a log-only warning
// before): any batch above 100 is silently clamped, BatchCapped is
// bumped, and the effective request is at most 100.
const MaxReaperBatchSize = 100

// ── P1 QDRANT-REAPER PointsSelector (July 2026) ──────────────────────
//
// PointsSelector decouples the point-level decision ("should this point
// be redacted?") from the Reaper's scroll+overwrite loop. A single
// Filter method returns true when the point's payload contains
// redactable keys. Implementations are stateless — the Reaper owns
// iteration, batching, and audit; the selector owns classification.
//
// KeySelector is the canonical implementation: it matches points whose
// payload contains any of the configured redaction keys. Additional
// selectors (e.g. time-based, workspace-scoped) can be added without
// touching the Reaper's scroll loop.
type PointsSelector interface {
	// Filter returns true if the point should have its payload
	// redacted. The Reaper calls this for every scrolled point
	// before deciding whether to include it in the overwrite batch.
	Filter(payload map[string]any) bool
}

// KeySelector matches points that carry any of the configured
// redaction keys in their payload.
type KeySelector struct {
	Keys []string
}

// Filter returns true if payload contains at least one of the
// configured Keys.
func (s *KeySelector) Filter(payload map[string]any) bool {
	if payload == nil || len(s.Keys) == 0 {
		return false
	}
	for _, key := range s.Keys {
		if _, ok := payload[key]; ok {
			return true
		}
	}
	return false
}

// ReaperOptions configures a single Reap run.
type ReaperOptions struct {
	// Collection is the Qdrant collection name (required).
	Collection string

	// Keys is the list of payload keys to redact. Empty → no-op run
	// (the operator must opt in explicitly per key). Defaults to
	// DefaultReaperKeys (empty in production).
	//
	// When Selector is non-nil, Keys is ignored — the selector
	// owns the classification decision. When Selector is nil,
	// a KeySelector{Keys} is constructed internally and Keys
	// drives the redaction decision.
	Keys []string

	// Selector is an optional PointsSelector that decides which
	// points need redaction. When nil, a KeySelector with the
	// provided Keys is used as the default filter. P1 QDRANT-REAPER
	// (July 2026): extracted from the inline redactPayload check
	// so the Reaper loop stays clean and new selector types can be
	// plugged in without touching the scroll+overwrite path.
	Selector PointsSelector

	// BatchSize is the scroll page size (1–MaxReaperBatchSize,
	// default MaxReaperBatchSize).
	BatchSize int

	// Limit caps the total number of points scanned (0 = no limit).
	Limit int

	// DryRun reports which points would be affected without mutating Qdrant.
	DryRun bool

	// DB is an optional SQLite handle for audit-logging the run result.
	// When non-nil, the run result is INSERTed into `qdrant_cleanup_audit`.
	// The table is owned by migrations/sqlite/098_qdrant_cleanup_audit.sql
	// (June 2026 QDRANT-005 PR5 followup) — the lazy CREATE
	// IF NOT EXISTS that used to live inside persistReaperAudit was
	// moved onto the canonical migration runner so the Reaper hot
	// path no longer pays a per-run schema-management syscall and
	// missing migrations surface loudly instead of being
	// self-healed. Nil DB is allowed but surfaced as a Warning at
	// run start so operators can wire it.
	DB *sql.DB
}

// ReaperResult is the outcome of a Reap run.
type ReaperResult struct {
	RunID          string    `json:"run_id"`
	Collection     string    `json:"collection"`
	KeysRedacted   []string  `json:"keys_redacted"`
	PointsScanned  int       `json:"points_scanned"`
	PointsAffected int       `json:"points_affected"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
	Status         string    `json:"status"`
	DryRun         bool      `json:"dry_run"`
	Errors         []string  `json:"errors,omitempty"`
	AffectedSample []string  `json:"affected_sample,omitempty"`
	FailedSample   []string  `json:"failed_sample,omitempty"`
	BatchCapped    int       `json:"batch_capped"`
	// AuditPersisted is true iff ReaperOptions.DB was non-nil AND the
	// INSERT into qdrant_cleanup_audit succeeded. Operators watching
	// dashboards key off this flag to detect audit-path regressions.
	AuditPersisted bool `json:"audit_persisted"`
}

// Reaper iterates over every point in a Qdrant collection and
// SELECTIVELY merges a redaction-subset of payload keys back. It is
// idempotent: points already clean are skipped.
//
// QDRANT-005 closure (June 2026): the previous UpsertPoints-based
// implementation destroyed vector data because UpsertPoints replaces
// the entire point and the reaper only sent ID + payload. The new
// path uses transport.Client.OverwritePayload (PUT /points/payload), which
// performs a SELECTIVE payload merge without touching vectors. This
// is the canonical Qdrant REST contract for payload redaction.
type Reaper struct {
	client *transport.Client
	log    *zap.Logger
}

// NewReaper creates a Reaper backed by the Qdrant client.
func NewReaper(client *transport.Client, log *zap.Logger) *Reaper {
	if log == nil {
		log = zap.NewNop()
	}
	return &Reaper{client: client, log: log}
}

// Reap scrolls the collection, redacts payload keys, and uses
// OverwritePayload (NOT UpsertPoints, which would null vectors) to
// apply the cleaned payload back. Scroll pagination is driven by the
// NextOffset returned by each Qdrant scroll response; the loop
// terminates when NextOffset is empty or the optional Limit is
// reached.
//
// Empty opt-in: if ReaperOptions.Keys is empty AND DefaultReaperKeys
// is empty (production default), Reap returns a no-op result with
// status = StatusNoop and a "no keys specified" note in Errors[0] as
// a defensive signal that the operator forgot to opt in.
func (r *Reaper) Reap(ctx context.Context, opts ReaperOptions) (*ReaperResult, error) {
	if r.client == nil {
		return nil, ErrNilClient
	}
	if opts.Collection == "" {
		return nil, fmt.Errorf("reaper: collection is required")
	}
	keys := opts.Keys
	if len(keys) == 0 {
		keys = DefaultReaperKeys
	}

	// Build the selector: use the provided one, or fall back to a
	// KeySelector built from opts.Keys. The selector owns the
	// "should we redact?" decision; the Reaper loop only iterates.
	//
	// When Selector is non-nil, we trust it even when Keys is empty
	// (the selector may use its own criteria). The no-op guard below
	// only applies when the default KeySelector is in use.
	selector := opts.Selector
	if selector == nil {
		selector = &KeySelector{Keys: keys}
		// Only guard against empty keys when using the default selector.
		// A custom selector may not need Keys at all.
		if len(keys) == 0 {
			started := time.Now().UTC()
			return &ReaperResult{
				RunID:        "noop-no-keys",
				Collection:   opts.Collection,
				KeysRedacted: keys,
				StartedAt:    started,
				CompletedAt:  started,
				Status:       StatusNoop,
				DryRun:       opts.DryRun,
				BatchCapped:  0,
				Errors:       []string{"reaper no-op: no keys specified (QDRANT-005 operator must opt in explicitly per key)"},
			}, nil
		}
	}

	batch := opts.BatchSize
	if batch <= 0 {
		batch = MaxReaperBatchSize
	}
	batchCapped := 0
	if batch > MaxReaperBatchSize {
		batch = MaxReaperBatchSize
		batchCapped = 1
		r.log.Warn("reaper batch size hard-capped to Qdrant scroll limit",
			zap.Int("requested", opts.BatchSize),
			zap.Int("effective", batch))
	}

	runID, err := generateRunID()
	if err != nil {
		return nil, fmt.Errorf("reaper: generate run id: %w", err)
	}

	started := time.Now().UTC()
	result := &ReaperResult{
		RunID:        runID,
		Collection:   opts.Collection,
		KeysRedacted: keys,
		StartedAt:    started,
		DryRun:       opts.DryRun,
		BatchCapped:  batchCapped,
	}

	// Scroll pagination: start with empty offset; each response returns
	// the NextOffset for the following page. The loop exits when Qdrant
	// returns an empty NextOffset (end of collection) or when the
	// optional Limit is reached.
	offset := ""
	for {
		if opts.Limit > 0 && result.PointsScanned >= opts.Limit {
			break
		}
		page, err := r.client.ScrollPoints(ctx, opts.Collection, offset, batch, nil)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("scroll: %v", err))
			break
		}
		if page == nil || len(page.Points) == 0 {
			break
		}
		result.PointsScanned += len(page.Points)

		// Per-page batch: collect redactions and apply via
		// OverwritePayload so vectors are preserved. The per-page
		// batch keeps the Qdrant PUT body small and avoids the
		// per-point fixed cost of N individual calls.
		//
		// P1 QDRANT-REAPER (July 2026): classification is delegated
		// to opts.Selector.Filter so the Reaper loop is a clean
		// scroll→classify→overwrite pipeline. The stripped check
		// is preserved: if the selector matches but no keys were
		// actually stripped (custom selector + different key set),
		// skip the no-op overwrite.
		var redactions []schema.PointPayload
		for _, p := range page.Points {
			if !selector.Filter(p.Payload) {
				continue
			}
			cleaned, stripped := redactPayload(p.Payload, keys)
			if !stripped {
				continue
			}
			if len(result.AffectedSample) < 50 {
				result.AffectedSample = append(result.AffectedSample, p.ID)
			}
			if opts.DryRun {
				result.PointsAffected++
				continue
			}
			redactions = append(redactions, schema.PointPayload{
				ID:      p.ID,
				Payload: cleaned,
			})
		}

		if !opts.DryRun && len(redactions) > 0 {
			if err := r.client.OverwritePayload(ctx, opts.Collection, redactions); err != nil {
				for _, pp := range redactions {
					result.Errors = append(result.Errors, fmt.Sprintf("overwrite %s: %v", pp.ID, err))
					if len(result.FailedSample) < 50 {
						result.FailedSample = append(result.FailedSample, pp.ID)
					}
				}
			} else {
				result.PointsAffected += len(redactions)
			}
		}

		// Advance to the next page. Empty NextOffset signals end-of-collection.
		if page.NextOffset == "" {
			break
		}
		offset = page.NextOffset
	}

	completed := time.Now().UTC()
	result.CompletedAt = completed

	switch {
	case len(result.Errors) > 0 && result.PointsAffected > 0:
		result.Status = StatusPartial
	case len(result.Errors) > 0 && result.PointsAffected == 0:
		result.Status = StatusFailed
	case result.PointsAffected == 0:
		result.Status = StatusNoop
	default:
		result.Status = StatusOK
	}

	if opts.DB != nil {
		if err := persistReaperAudit(ctx, opts.DB, result); err != nil {
			r.log.Warn("reaper audit INSERT failed (QDRANT-005 audit path)",
				zap.String("run_id", result.RunID),
				zap.Error(err))
			result.Errors = append(result.Errors, fmt.Sprintf("audit: %v", err))
			if result.Status == StatusOK {
				result.Status = StatusPartial
			}
		} else {
			result.AuditPersisted = true
		}
	} else {
		r.log.Warn("reaper audit DB not wired; run result not persisted (QDRANT-005 compliance requires DB=non-nil)",
			zap.String("run_id", result.RunID),
			zap.String("collection", result.Collection))
	}

	return result, nil
}

// persistReaperAudit writes a single row into qdrant_cleanup_audit
// recording the run. The table is owned by
// migrations/sqlite/098_qdrant_cleanup_audit.sql — the migration
// runner applies the schema at boot, so this function never touches
// DDL on the Reaper hot path. If the table is missing (e.g. a
// misconfigured db where the migration runner has not yet landed),
// the INSERT will error with a canonical `no such table` failure;
// the caller logs the error and flips AuditPersisted=false + status
// = StatusPartial so the regression is visible on dashboards
// immediately rather than silently self-healed.
func persistReaperAudit(ctx context.Context, db *sql.DB, r *ReaperResult) error {
	if db == nil || r == nil {
		return fmt.Errorf("persistReaperAudit: nil db or result")
	}
	keysJSON, _ := jsonMarshal(r.KeysRedacted)
	errsJSON, _ := jsonMarshal(r.Errors)
	var dryRun int
	if r.DryRun {
		dryRun = 1
	}
	_, err := db.ExecContext(ctx, `
		INSERT OR REPLACE INTO qdrant_cleanup_audit (
			run_id, collection, started_at, completed_at, status,
			points_scanned, points_affected, errors_json, dry_run, keys_redacted_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		r.RunID, r.Collection, r.StartedAt.UTC().Format(time.RFC3339), r.CompletedAt.UTC().Format(time.RFC3339), r.Status,
		r.PointsScanned, r.PointsAffected, errsJSON, dryRun, keysJSON,
	)
	if err != nil {
		return fmt.Errorf("insert qdrant_cleanup_audit: %w", err)
	}
	return nil
}

// jsonMarshal marshals v to a JSON string. Two call sites (audit
// keys_redacted_json + errors_json) — using encoding/json directly
// keeps the import surface minimal.
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// redactPayload returns a copy of payload with the given keys removed.
// The second return value is true when at least one key was stripped.
func redactPayload(payload map[string]any, keys []string) (map[string]any, bool) {
	if payload == nil {
		return nil, false
	}
	stripped := false
	cleaned := make(map[string]any, len(payload))
	for k, v := range payload {
		redact := false
		for _, rk := range keys {
			if k == rk {
				redact = true
				break
			}
		}
		if redact {
			stripped = true
			continue
		}
		cleaned[k] = v
	}
	return cleaned, stripped
}

// generateRunID produces a hex-encoded random 16-byte identifier.
func generateRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	return hex.EncodeToString(b), nil
}
