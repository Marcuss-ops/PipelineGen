// Package artifactcache persists deterministic artifact-cache mappings in
// SQLite and stores the immutable bytes in the platform CAS.
package artifactcache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/cas"
)

var ErrNotWired = errors.New("artifact cache sqlite adapter: not wired")

type Cache struct {
	db      *sql.DB
	cas     *cas.Store
	content capregistry.ContentObjectStore
}

func New(db *sql.DB, store *cas.Store, content capregistry.ContentObjectStore) (*Cache, error) {
	if db == nil || store == nil {
		return nil, ErrNotWired
	}
	return &Cache{db: db, cas: store, content: content}, nil
}

var _ capcache.Cache = (*Cache)(nil)
var _ capcache.MetricsRecorder = (*Cache)(nil)
var _ capcache.ClaimStore = (*Cache)(nil)
var _ capcache.LeaseStore = (*Cache)(nil)

func (c *Cache) Lookup(ctx context.Context, key capcache.Key, expectedWorkMS int64) (*capcache.Entry, bool, error) {
	if c == nil || c.db == nil || c.cas == nil {
		return nil, false, ErrNotWired
	}
	digest, err := key.Digest()
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var entry capcache.Entry
	var status string
	err = c.db.QueryRowContext(ctx, `
		SELECT cache_key, source_sha256, operation, parameters_json,
		       processor_version, artifact_sha256, size_bytes, mime_type,
		       status, created_at, last_accessed_at
		FROM artifact_cache_entries
		WHERE cache_key = ?`, digest).Scan(
		&entry.CacheKey, &entry.SourceSHA256, &entry.Operation,
		&entry.ParametersJSON, &entry.ProcessorVersion, &entry.ArtifactSHA256,
		&entry.SizeBytes, &entry.MIMEType, &status, &entry.CreatedAt,
		&entry.LastAccessedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if metricErr := c.bumpMetric(ctx, key.Operation, false, 0, 0, now); metricErr != nil {
			return nil, false, metricErr
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("artifact cache lookup %q: %w", digest, err)
	}
	entry.Status = status
	if status != "READY" || entry.ArtifactSHA256 == "" {
		if metricErr := c.bumpMetric(ctx, key.Operation, false, 0, 0, now); metricErr != nil {
			return nil, false, metricErr
		}
		return nil, false, nil
	}
	verified, err := c.cas.Verify(ctx, entry.ArtifactSHA256)
	if err != nil {
		return nil, false, err
	}
	if !verified.Exists || !verified.Verified {
		_, _ = c.db.ExecContext(ctx, `UPDATE artifact_cache_entries SET status='INVALID', updated_at=?, error_message=? WHERE cache_key=?`, now, "CAS object missing or corrupt", digest)
		if metricErr := c.bumpInvalidation(ctx, key.Operation, now); metricErr != nil {
			return nil, false, metricErr
		}
		if metricErr := c.bumpMetric(ctx, key.Operation, false, 0, 0, now); metricErr != nil {
			return nil, false, metricErr
		}
		return nil, false, nil
	}
	if _, err := c.db.ExecContext(ctx, `UPDATE artifact_cache_entries SET last_accessed_at=?, updated_at=? WHERE cache_key=?`, now, now, digest); err != nil {
		return nil, false, fmt.Errorf("artifact cache touch %q: %w", digest, err)
	}
	if metricErr := c.bumpMetric(ctx, key.Operation, true, entry.SizeBytes, expectedWorkMS, now); metricErr != nil {
		return nil, false, metricErr
	}
	return &entry, true, nil
}

// Claim obtains a durable single-builder lease. A READY row is returned
// immediately; an unexpired BUILDING row is waited on so concurrent workers
// reuse the first completed artifact instead of running the same work.
func (c *Cache) Claim(ctx context.Context, key capcache.Key, lease time.Duration, expectedWorkMS int64) (capcache.Claim, error) {
	if c == nil || c.db == nil {
		return capcache.Claim{}, ErrNotWired
	}
	digest, err := key.Digest()
	if err != nil {
		return capcache.Claim{}, err
	}
	if lease <= 0 {
		lease = 15 * time.Minute
	}
	// A crashed worker can leave a BUILDING lease valid for the full lease
	// duration. Never make a batch wait 15 minutes for that stale owner: the
	// caller can safely recompute after this bounded contention window.
	waitWindow := 5 * time.Second
	if expectedWorkMS > 0 {
		candidate := time.Duration(expectedWorkMS) * time.Millisecond
		if candidate > waitWindow && candidate < 30*time.Second {
			waitWindow = candidate
		}
	}
	waitDeadline := time.Now().Add(waitWindow)
	for {
		now := time.Now().UTC()
		leaseUntil := now.Add(lease).Format(time.RFC3339Nano)
		var status, storedLeaseUntil string
		var entry capcache.Entry
		err := c.db.QueryRowContext(ctx, `SELECT cache_key, source_sha256, operation, parameters_json, processor_version, artifact_sha256, size_bytes, mime_type, status, created_at, last_accessed_at, COALESCE(lease_until, '') FROM artifact_cache_entries WHERE cache_key=?`, digest).Scan(&entry.CacheKey, &entry.SourceSHA256, &entry.Operation, &entry.ParametersJSON, &entry.ProcessorVersion, &entry.ArtifactSHA256, &entry.SizeBytes, &entry.MIMEType, &status, &entry.CreatedAt, &entry.LastAccessedAt, &storedLeaseUntil)
		if errors.Is(err, sql.ErrNoRows) {
			leaseID := uuid.NewString()
			_, err = c.db.ExecContext(ctx, `INSERT INTO artifact_cache_entries (cache_key,source_sha256,operation,parameters_json,processor_version,artifact_sha256,size_bytes,mime_type,status,lease_id,lease_until,created_at,last_accessed_at,updated_at,error_message) VALUES (?,?,?,?,?,'',0,'','BUILDING',?,?, ?, ?, ?, '')`, digest, key.SourceSHA256, key.Operation, nonEmpty(key.ParametersJSON, "{}"), key.ProcessorVersion, leaseID, leaseUntil, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if err == nil {
				if metricErr := c.bumpMetric(ctx, key.Operation, false, 0, 0, now.Format(time.RFC3339Nano)); metricErr != nil {
					return capcache.Claim{}, metricErr
				}
				return capcache.Claim{LeaseID: leaseID, Acquired: true}, nil
			}
			var existingStatus string
			probeErr := c.db.QueryRowContext(ctx, `SELECT status FROM artifact_cache_entries WHERE cache_key=?`, digest).Scan(&existingStatus)
			if probeErr == nil {
				continue // another writer won the insert race
			}
			if errors.Is(probeErr, sql.ErrNoRows) {
				return capcache.Claim{}, fmt.Errorf("artifact cache claim %q: %w", digest, err)
			}
			return capcache.Claim{}, fmt.Errorf("artifact cache claim %q: %w (probe: %v)", digest, err, probeErr)
		}
		if err != nil {
			return capcache.Claim{}, fmt.Errorf("artifact cache claim %q: %w", digest, err)
		}
		entry.Status = status
		if status == "READY" && entry.ArtifactSHA256 != "" {
			verified, verifyErr := c.cas.Verify(ctx, entry.ArtifactSHA256)
			if verifyErr != nil {
				return capcache.Claim{}, verifyErr
			}
			if verified.Exists && verified.Verified {
				entry.LastAccessedAt = now.Format(time.RFC3339Nano)
				_, _ = c.db.ExecContext(ctx, `UPDATE artifact_cache_entries SET last_accessed_at=?,updated_at=? WHERE cache_key=?`, entry.LastAccessedAt, entry.LastAccessedAt, digest)
				if metricErr := c.bumpMetric(ctx, key.Operation, true, entry.SizeBytes, expectedWorkMS, entry.LastAccessedAt); metricErr != nil {
					return capcache.Claim{}, metricErr
				}
				return capcache.Claim{Entry: &entry}, nil
			}
		}
		leaseActive := status == "BUILDING" && storedLeaseUntil != ""
		if leaseActive {
			if until, parseErr := time.Parse(time.RFC3339Nano, storedLeaseUntil); parseErr == nil && until.After(now) {
				if time.Now().After(waitDeadline) {
					return capcache.Claim{}, fmt.Errorf("%w: %s", capcache.ErrLeaseBusy, digest)
				}
				timer := time.NewTimer(100 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return capcache.Claim{}, ctx.Err()
				case <-timer.C:
				}
				continue
			}
		}
		leaseID := uuid.NewString()
		res, updateErr := c.db.ExecContext(ctx, `UPDATE artifact_cache_entries SET status='BUILDING',lease_id=?,lease_until=?,updated_at=?,error_message='' WHERE cache_key=? AND (status!='BUILDING' OR lease_until IS NULL OR lease_until<=?)`, leaseID, leaseUntil, now.Format(time.RFC3339Nano), digest, now.Format(time.RFC3339Nano))
		if updateErr != nil {
			return capcache.Claim{}, updateErr
		}
		if n, _ := res.RowsAffected(); n == 1 {
			if metricErr := c.bumpMetric(ctx, key.Operation, false, 0, 0, now.Format(time.RFC3339Nano)); metricErr != nil {
				return capcache.Claim{}, metricErr
			}
			return capcache.Claim{LeaseID: leaseID, Acquired: true}, nil
		}
	}
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (c *Cache) Store(ctx context.Context, key capcache.Key, content io.Reader, mimeType string, expectedWorkMS int64) (*capcache.Entry, error) {
	return c.storeWithLease(ctx, key, "", content, mimeType, expectedWorkMS)
}

func (c *Cache) StoreWithLease(ctx context.Context, key capcache.Key, leaseID string, content io.Reader, mimeType string, expectedWorkMS int64) (*capcache.Entry, error) {
	return c.storeWithLease(ctx, key, leaseID, content, mimeType, expectedWorkMS)
}

func (c *Cache) storeWithLease(ctx context.Context, key capcache.Key, leaseID string, content io.Reader, mimeType string, _ int64) (*capcache.Entry, error) {
	if c == nil || c.db == nil || c.cas == nil {
		return nil, ErrNotWired
	}
	if content == nil {
		return nil, fmt.Errorf("%w: nil content", capcache.ErrInvalidKey)
	}
	digest, err := key.Digest()
	if err != nil {
		return nil, err
	}
	obj, err := c.cas.Put(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("artifact cache CAS put %q: %w", digest, err)
	}
	if c.content != nil {
		uri, pathErr := c.cas.LocalPath(obj.SHA256)
		if pathErr != nil {
			return nil, pathErr
		}
		if err := c.content.Put(ctx, capregistry.ContentObject{
			SHA256: obj.SHA256, SizeBytes: obj.SizeBytes, MimeType: mimeType,
			StorageURI: uri, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			IntegrityStatus: capregistry.IntegrityUnverified,
		}); err != nil {
			return nil, fmt.Errorf("artifact cache content registry %q: %w", obj.SHA256, err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(leaseID) == "" {
		var status string
		statusErr := c.db.QueryRowContext(ctx, `SELECT status FROM artifact_cache_entries WHERE cache_key=?`, digest).Scan(&status)
		if statusErr == nil && status == "BUILDING" {
			return nil, fmt.Errorf("%w: store %q requires the active lease", capcache.ErrLeaseLost, digest)
		}
		if statusErr != nil && !errors.Is(statusErr, sql.ErrNoRows) {
			return nil, fmt.Errorf("artifact cache store status %q: %w", digest, statusErr)
		}
	}
	params := key.ParametersJSON
	if params == "" {
		params = "{}"
	}
	var result sql.Result
	if strings.TrimSpace(leaseID) != "" {
		result, err = c.db.ExecContext(ctx, `
			UPDATE artifact_cache_entries
			SET artifact_sha256=?, size_bytes=?, mime_type=?, status='READY',
			    lease_id='', lease_until=NULL, last_accessed_at=?, updated_at=?, error_message=''
			WHERE cache_key=? AND status='BUILDING' AND lease_id=?`,
			obj.SHA256, obj.SizeBytes, strings.TrimSpace(mimeType), now, now, digest, leaseID)
		if err == nil {
			if n, _ := result.RowsAffected(); n != 1 {
				return nil, fmt.Errorf("%w: store %q", capcache.ErrLeaseLost, digest)
			}
		}
	} else {
		result, err = c.db.ExecContext(ctx, `
			INSERT INTO artifact_cache_entries
			(cache_key, source_sha256, operation, parameters_json, processor_version,
			 artifact_sha256, size_bytes, mime_type, status, lease_id, lease_until,
			 created_at, last_accessed_at, updated_at, error_message)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'READY', '', NULL, ?, ?, ?, '')
			ON CONFLICT(cache_key) DO UPDATE SET
				artifact_sha256=excluded.artifact_sha256, size_bytes=excluded.size_bytes,
				mime_type=excluded.mime_type, status='READY', lease_id='', lease_until=NULL,
				last_accessed_at=excluded.last_accessed_at, updated_at=excluded.updated_at,
				error_message=''`,
			digest, key.SourceSHA256, key.Operation, params, key.ProcessorVersion,
			obj.SHA256, obj.SizeBytes, strings.TrimSpace(mimeType), now, now, now)
	}
	if err != nil {
		return nil, fmt.Errorf("artifact cache store %q: %w", digest, err)
	}
	return &capcache.Entry{CacheKey: digest, SourceSHA256: key.SourceSHA256, Operation: key.Operation, ParametersJSON: params, ProcessorVersion: key.ProcessorVersion, ArtifactSHA256: obj.SHA256, SizeBytes: obj.SizeBytes, MIMEType: mimeType, Status: "READY", CreatedAt: now, LastAccessedAt: now}, nil
}

func (c *Cache) ReleaseClaim(ctx context.Context, key capcache.Key, leaseID, reason string) error {
	if c == nil || c.db == nil {
		return ErrNotWired
	}
	if strings.TrimSpace(leaseID) == "" {
		return capcache.ErrLeaseLost
	}
	digest, err := key.Digest()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := c.db.ExecContext(ctx, `
		UPDATE artifact_cache_entries
		SET status='FAILED', lease_id='', lease_until=NULL, updated_at=?, error_message=?
		WHERE cache_key=? AND status='BUILDING' AND lease_id=?`,
		now, strings.TrimSpace(reason), digest, leaseID)
	if err != nil {
		return fmt.Errorf("artifact cache release %q: %w", digest, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: release %q", capcache.ErrLeaseLost, digest)
	}
	return nil
}

func (c *Cache) Open(ctx context.Context, entry *capcache.Entry) (io.ReadCloser, error) {
	if c == nil || c.cas == nil {
		return nil, ErrNotWired
	}
	if entry == nil || entry.ArtifactSHA256 == "" {
		return nil, fmt.Errorf("%w: empty cache entry", capcache.ErrInvalidKey)
	}
	return c.cas.Open(ctx, entry.ArtifactSHA256)
}

func (c *Cache) Invalidate(ctx context.Context, key capcache.Key) error {
	if c == nil || c.db == nil {
		return ErrNotWired
	}
	digest, err := key.Digest()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := c.db.ExecContext(ctx, `UPDATE artifact_cache_entries SET status='INVALID', updated_at=? WHERE cache_key=?`, now, digest)
	if err != nil {
		return fmt.Errorf("artifact cache invalidate %q: %w", digest, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return c.bumpInvalidation(ctx, key.Operation, now)
	}
	return err
}

func (c *Cache) bumpInvalidation(ctx context.Context, operation, now string) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO artifact_cache_metrics
		(operation, hit_count, miss_count, invalidation_count, avoided_bytes, avoided_work_ms, created_at, updated_at)
		VALUES (?, 0, 0, 1, 0, 0, ?, ?)
		ON CONFLICT(operation) DO UPDATE SET
			invalidation_count=invalidation_count+1,
			updated_at=excluded.updated_at`, operation, now, now)
	if err != nil {
		return fmt.Errorf("artifact cache invalidation metrics %q: %w", operation, err)
	}
	return nil
}

func (c *Cache) Metrics(ctx context.Context, operation string) (capcache.Metrics, error) {
	if c == nil || c.db == nil {
		return capcache.Metrics{}, ErrNotWired
	}
	var m capcache.Metrics
	err := c.db.QueryRowContext(ctx, `SELECT operation, hit_count, miss_count, invalidation_count, avoided_bytes, avoided_work_ms FROM artifact_cache_metrics WHERE operation=?`, operation).Scan(&m.Operation, &m.HitCount, &m.MissCount, &m.InvalidationCount, &m.AvoidedBytes, &m.AvoidedWorkMS)
	if errors.Is(err, sql.ErrNoRows) {
		return capcache.Metrics{Operation: operation}, nil
	}
	if err != nil {
		return m, fmt.Errorf("artifact cache metrics %q: %w", operation, err)
	}
	return m, nil
}

func (c *Cache) RecordOutcome(ctx context.Context, operation string, hit bool, avoidedBytes, avoidedWorkMS int64) error {
	if c == nil || c.db == nil {
		return ErrNotWired
	}
	if strings.TrimSpace(operation) == "" {
		return capcache.ErrInvalidKey
	}
	return c.bumpMetric(ctx, operation, hit, avoidedBytes, avoidedWorkMS, time.Now().UTC().Format(time.RFC3339Nano))
}

func (c *Cache) bumpMetric(ctx context.Context, operation string, hit bool, avoidedBytes, avoidedWorkMS int64, now string) error {
	if operation == "" {
		return capcache.ErrInvalidKey
	}
	hits, misses := 0, 1
	if hit {
		hits, misses = 1, 0
	}
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO artifact_cache_metrics
		(operation, hit_count, miss_count, invalidation_count, avoided_bytes, avoided_work_ms, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?, ?, ?)
		ON CONFLICT(operation) DO UPDATE SET
			hit_count=hit_count+excluded.hit_count,
			miss_count=miss_count+excluded.miss_count,
			avoided_bytes=avoided_bytes+excluded.avoided_bytes,
			avoided_work_ms=avoided_work_ms+excluded.avoided_work_ms,
			updated_at=excluded.updated_at`,
		operation, hits, misses, avoidedBytes, avoidedWorkMS, now, now)
	if err != nil {
		return fmt.Errorf("artifact cache metrics %q: %w", operation, err)
	}
	return nil
}
