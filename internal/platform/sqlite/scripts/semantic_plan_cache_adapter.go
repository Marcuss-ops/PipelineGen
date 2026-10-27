package scripts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"go.uber.org/zap"
)

// SemanticPlanCacheDefaultTTL is the certified lifetime of one semantic
// provider answer. It matches the in-memory planner cache so a restart does
// not silently change how stale an answer may be.
const SemanticPlanCacheDefaultTTL = time.Hour

// SQLiteSemanticPlanCacheAdapter is the durable L2 plan cache. It stores only
// positive provider answers (a miss is not a durable fact) and keeps the
// answering provider alongside the payload so one provider can be invalidated
// without dropping the rest of the warm index.
//
// The cache plane is rebuildable: every failure here is a miss, never a failed
// run. The advisory TTL is enforced twice: this adapter expires rows on read,
// and the planner still applies its own CacheTTL to the returned StoredAt.
type SQLiteSemanticPlanCacheAdapter struct {
	db  *sql.DB
	ttl time.Duration
	now func() time.Time
	log *zap.Logger
}

var _ scriptgen.SemanticSearchCache = (*SQLiteSemanticPlanCacheAdapter)(nil)
var _ scriptgen.SemanticSearchCacheInvalidator = (*SQLiteSemanticPlanCacheAdapter)(nil)

// NewSQLiteSemanticPlanCacheAdapter builds the adapter. A non-positive TTL
// falls back to SemanticPlanCacheDefaultTTL.
func NewSQLiteSemanticPlanCacheAdapter(db *sql.DB, ttl time.Duration, log *zap.Logger) *SQLiteSemanticPlanCacheAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	if ttl <= 0 {
		ttl = SemanticPlanCacheDefaultTTL
	}
	return &SQLiteSemanticPlanCacheAdapter{db: db, ttl: ttl, now: time.Now, log: log}
}

// Get returns a live answer or a miss. Expired, malformed and unreadable rows
// are deleted and reported as a miss: a stale cache entry must never outlive
// the provider that produced it.
func (a *SQLiteSemanticPlanCacheAdapter) Get(ctx context.Context, key string) (scriptgen.SemanticSearchCacheEntry, bool) {
	if a == nil || a.db == nil || strings.TrimSpace(key) == "" {
		return scriptgen.SemanticSearchCacheEntry{}, false
	}
	var payload, provider, lane, storedRaw, expiresRaw string
	err := a.db.QueryRowContext(ctx, `
		SELECT payload_json, provider, provider_lane, stored_at, expires_at
		FROM semantic_plan_cache
		WHERE cache_key = ?`, key).Scan(&payload, &provider, &lane, &storedRaw, &expiresRaw)
	if err == sql.ErrNoRows {
		return scriptgen.SemanticSearchCacheEntry{}, false
	}
	if err != nil {
		a.log.Warn("semantic plan cache: read failed; treating as a miss", zap.String("key", key), zap.Error(err))
		return scriptgen.SemanticSearchCacheEntry{}, false
	}
	storedAt, storedErr := time.Parse(time.RFC3339Nano, storedRaw)
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, expiresRaw)
	if storedErr != nil || expiresErr != nil || !a.now().Before(expiresAt) {
		a.deleteKey(ctx, key, "expired or malformed timestamps")
		return scriptgen.SemanticSearchCacheEntry{}, false
	}
	var response scriptgen.SemanticAssetSearchResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil || len(response.Hits) == 0 {
		a.deleteKey(ctx, key, "unreadable payload")
		return scriptgen.SemanticSearchCacheEntry{}, false
	}
	return scriptgen.SemanticSearchCacheEntry{
		Response: response, StoredAt: storedAt, ExpiresAt: expiresAt,
		Provider: provider, ProviderLane: lane,
	}, true
}

// Put upserts one positive answer. Empty answers are ignored for the same
// reason the in-memory cache ignores them.
func (a *SQLiteSemanticPlanCacheAdapter) Put(ctx context.Context, key string, entry scriptgen.SemanticSearchCacheEntry) {
	if a == nil || a.db == nil || strings.TrimSpace(key) == "" || len(entry.Response.Hits) == 0 {
		return
	}
	payload, err := json.Marshal(entry.Response)
	if err != nil {
		a.log.Warn("semantic plan cache: encode failed; entry not stored", zap.String("key", key), zap.Error(err))
		return
	}
	storedAt := entry.StoredAt
	if storedAt.IsZero() {
		storedAt = a.now()
	}
	storedAt = storedAt.UTC()
	expiresAt := entry.ExpiresAt
	if expiresAt.IsZero() || !expiresAt.After(storedAt) {
		expiresAt = storedAt.Add(a.ttl)
	}
	expiresAt = expiresAt.UTC()
	if _, err := a.db.ExecContext(ctx, `
		INSERT INTO semantic_plan_cache(cache_key, payload_json, provider, provider_lane, stored_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET
			payload_json = excluded.payload_json,
			provider = excluded.provider,
			provider_lane = excluded.provider_lane,
			stored_at = excluded.stored_at,
			expires_at = excluded.expires_at`,
		key, string(payload), a.answerProvider(entry), strings.TrimSpace(entry.ProviderLane),
		storedAt.Format(time.RFC3339Nano), expiresAt.Format(time.RFC3339Nano)); err != nil {
		a.log.Warn("semantic plan cache: write failed; entry not stored", zap.String("key", key), zap.Error(err))
	}
}

// InvalidateProvider deletes every answer produced by one provider or lane and
// returns how many rows were dropped. It is the targeted invalidation used when
// a provider changes its index or routing contract: warming the other
// providers must not require dropping the whole plan cache.
func (a *SQLiteSemanticPlanCacheAdapter) InvalidateProvider(ctx context.Context, provider string) (int64, error) {
	if a == nil || a.db == nil {
		return 0, fmt.Errorf("semantic plan cache: database unavailable")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return 0, fmt.Errorf("semantic plan cache: provider is required")
	}
	result, err := a.db.ExecContext(ctx, `
		DELETE FROM semantic_plan_cache
		WHERE provider = ? COLLATE NOCASE OR provider_lane = ? COLLATE NOCASE`, provider, provider)
	if err != nil {
		return 0, fmt.Errorf("semantic plan cache: invalidate %q: %w", provider, err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, nil
	}
	if removed > 0 {
		a.log.Info("semantic plan cache: provider invalidated", zap.String("provider", provider), zap.Int64("entries", removed))
	}
	return removed, nil
}

func (a *SQLiteSemanticPlanCacheAdapter) answerProvider(entry scriptgen.SemanticSearchCacheEntry) string {
	if provider := strings.TrimSpace(entry.Provider); provider != "" {
		return provider
	}
	if provider := strings.TrimSpace(entry.Response.Provider); provider != "" {
		return provider
	}
	for _, hit := range entry.Response.Hits {
		if provider := strings.TrimSpace(hit.Provider); provider != "" {
			return provider
		}
	}
	return ""
}

func (a *SQLiteSemanticPlanCacheAdapter) deleteKey(ctx context.Context, key, reason string) {
	if _, err := a.db.ExecContext(ctx, `DELETE FROM semantic_plan_cache WHERE cache_key = ?`, key); err != nil {
		a.log.Warn("semantic plan cache: expired entry could not be deleted",
			zap.String("key", key), zap.String("reason", reason), zap.Error(err))
	}
}
