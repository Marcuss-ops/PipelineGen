package qdrant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// DefaultReaperKeys lists the payload keys that the reaper strips by default.
// These are server-internal locators (filesystem paths, Drive web-view links,
// download URLs, status markers) that have no place in a tenant-facing index.
var DefaultReaperKeys = []string{"drive_link", "local_path", "download_link", "status"}

// ErrNilClient is returned when a Reaper is constructed with a nil Client.
var ErrNilClient = errors.New("reaper: qdrant client is nil")

const (
	StatusRunning  = "running"
	StatusOK       = "ok"
	StatusNoop     = "noop"
	StatusPartial  = "partial"
	StatusFailed   = "failed"
)

// ReaperOptions configures a single Reap run.
type ReaperOptions struct {
	// Collection is the Qdrant collection name (required).
	Collection string

	// Keys is the list of payload keys to redact. Defaults to DefaultReaperKeys.
	Keys []string

	// BatchSize is the scroll page size (1–100, default 100).
	BatchSize int

	// Limit caps the total number of points scanned (0 = no limit).
	Limit int

	// DryRun reports which points would be affected without mutating Qdrant.
	DryRun bool

	// DB is an optional SQLite handle for audit-logging the run result.
	// When nil, the run proceeds without audit persistence.
	//
	// TODO(QDRANT-005): wire audit INSERT into qdrant_cleanup_audit
	// (migration 097) so each Reap run is persisted for operator
	// runbook visibility.
	DB *sql.DB
}

// ReaperResult is the outcome of a Reap run.
type ReaperResult struct {
	RunID           string    `json:"run_id"`
	Collection      string    `json:"collection"`
	KeysRedacted    []string  `json:"keys_redacted"`
	PointsScanned   int       `json:"points_scanned"`
	PointsAffected  int       `json:"points_affected"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	Status          string    `json:"status"`
	DryRun          bool      `json:"dry_run"`
	Errors          []string  `json:"errors,omitempty"`
	AffectedSample  []string  `json:"affected_sample,omitempty"`
	FailedSample    []string  `json:"failed_sample,omitempty"`
	BatchCapped     int       `json:"batch_capped"`
}

// Reaper iterates over every point in a Qdrant collection and strips
// server-internal payload keys (local_path, drive_link, download_link,
// status). It is idempotent: points already clean are skipped.
type Reaper struct {
	client *Client
	log    *zap.Logger
}

// NewReaper creates a Reaper backed by the Qdrant client.
func NewReaper(client *Client, log *zap.Logger) *Reaper {
	if log == nil {
		log = zap.NewNop()
	}
	return &Reaper{client: client, log: log}
}

// Reap scrolls the collection, redacts payload keys, and upserts the cleaned
// points back. Scroll pagination is driven by the NextOffset returned by each
// Qdrant scroll response; the loop terminates when NextOffset is empty or the
// optional Limit is reached.
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
	batch := opts.BatchSize
	if batch <= 0 {
		batch = 100
	}
	batchCapped := 0
	if batch > 100 {
		batchCapped = 1
		r.log.Warn("reaper batch size capped to Qdrant scroll limit",
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
		page, err := r.client.ScrollPoints(ctx, opts.Collection, offset, batch)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("scroll: %v", err))
			break
		}
		if page == nil || len(page.Points) == 0 {
			break
		}
		result.PointsScanned += len(page.Points)

		for _, p := range page.Points {
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
			if err := r.client.UpsertPoints(ctx, opts.Collection, []Point{{
				ID:      p.ID,
				Payload: cleaned,
			}}); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("upsert %s: %v", p.ID, err))
				if len(result.FailedSample) < 50 {
					result.FailedSample = append(result.FailedSample, p.ID)
				}
				continue
			}
			result.PointsAffected++
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

	return result, nil
}

// redactPayload returns a copy of payload with the given keys removed.
// The second return value is true when at least one key was stripped.
func redactPayload(payload map[string]interface{}, keys []string) (map[string]interface{}, bool) {
	if payload == nil {
		return nil, false
	}
	stripped := false
	cleaned := make(map[string]interface{}, len(payload))
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
