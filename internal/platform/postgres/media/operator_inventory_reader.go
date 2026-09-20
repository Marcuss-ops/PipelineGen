// Package media — operator_inventory_reader.go: the operator console's
// Content Library / Asset Inspector / facet read model, answered from the
// PostgreSQL media SSOT.
//
// WHY THIS EXISTS. internal/platform/sqlite/assets/operatorread served this
// surface from the OPERATIONAL SQLite mirror (m.lifecycle_state,
// m.index_state, m.embedding_json, m.metadata_json ...) while the canonical
// committer wrote PostgreSQL. The operator console therefore graded a
// database that holds no post-cutover rows: a freshly committed asset was
// invisible to the Content Library, the Inspector reported pre-cutover
// state, and the facet counts described the mirror. This type is the
// 1:1 replacement, column for column, on the engine that owns media_assets.
//
// SEMANTIC PARITY, DELIBERATELY PRESERVED. The retired SQLite reader's
// contract is reproduced exactly:
//
//   - the same single-SELECT shape with the loc_flags / outbox_counts CTEs,
//     so there is no N+1 scan and no per-row round trip;
//   - `lifecycle_state != 'DELETED'` as the base filter, and the same
//     asset-state projection (lifecycle_state + index_state → asset_state),
//     because asset_state is a compatibility view and must not become
//     authoritative again;
//   - the same pagination contract (limit+1 look-ahead → HasMore, the
//     next cursor being the next offset);
//   - the same facet set and the same canonical-value merge, so every
//     canonical enum member keeps appearing with count 0;
//   - the same "not found" shape: a missing or soft-deleted asset resolves
//     to (nil, nil) in Get, never an error.
//
// ONE DELIBERATE DIVERGENCE, AND ONE THAT IS FORCED. The retired reader
// projected the SQLite `media_assets.error` column as `last_error`. That
// column does not exist on PostgreSQL at all (001_media_schema.sql declares
// no `error` on media_assets), so it cannot be projected here even for
// fidelity. The live index-error signal is the
// `metadata_json.$.last_index_error` key, which both engines genuinely
// maintain — pgmedia.SetIndexState jsonb_set/jsonb-removes it
// (mutations.go), and the SQLite committer surface does the same. The value
// is descriptive only (operator.ResolveIndexHealth uses it as the
// IndexHealthView description fallback), so the health CODE is unaffected.
//
// SECOND, SUBTLER DIVERGENCE. SQLite's LIKE is case-INsensitive for ASCII by
// default; PostgreSQL's LIKE is case-sensitive. A verbatim `LIKE` port would
// therefore have silently broken the Content Library search — an operator
// typing "beluga" would stop finding "Beluga underwater". The search arm
// uses ILIKE to preserve the retired observable behaviour.
//
// FILE SPLIT. This file owns the port surface (the type, its construction,
// the three interface methods) and the facet arm; operator_inventory_queries.go
// owns every SELECT, the shared projection and the single scan site. The split
// is not cosmetic — the package is near its max_files_per_package budget, so
// the read model is two cohesive halves (surface + facets | queries) rather
// than the retired reader's five files.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/operator"
	"go.uber.org/zap"
)

// OperatorInventoryReader is the PostgreSQL implementation of the operator
// read model. It is intentionally read-only and projects rows for the UI.
type OperatorInventoryReader struct {
	db  *sql.DB
	log *zap.Logger
}

// Compile-time assertion: the reader satisfies the canonical capability port.
// Drift on the interface is a build failure, not a runtime gap.
var _ operator.AssetInventoryReader = (*OperatorInventoryReader)(nil)

// NewOperatorInventoryReader constructs the read-only inventory reader on the
// PostgreSQL media database. db is required: a nil handle is a programming
// error, not a degrade path (wiring resolves it from the canonical media
// handle and leaves the port unwired when the media plane is closed).
func NewOperatorInventoryReader(db *sql.DB, log *zap.Logger) *OperatorInventoryReader {
	if db == nil {
		panic("media.NewOperatorInventoryReader: db is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &OperatorInventoryReader{db: db, log: log}
}

// DB exposes the underlying handle so callers can assert the reader lives on
// the media SSOT.
func (r *OperatorInventoryReader) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// List implements operator.AssetInventoryReader.
func (r *OperatorInventoryReader) List(ctx context.Context, query operator.AssetInventoryQuery) (operator.AssetInventoryPage, error) {
	return r.list(ctx, query)
}

// Get implements operator.AssetInventoryReader.
func (r *OperatorInventoryReader) Get(ctx context.Context, assetID string) (*operator.AssetInspection, error) {
	return r.get(ctx, assetID)
}

// Facets implements operator.AssetInventoryReader.
func (r *OperatorInventoryReader) Facets(ctx context.Context) (*operator.AssetInventoryFacets, error) {
	return r.facets(ctx)
}

// facets counts every filter dimension over the non-deleted catalog and merges
// the canonical enum members in, so a value with no rows still renders as a
// zero-count option. The retired SQLite reader's facet set is reproduced
// exactly: five raw value distributions plus the derived asset_state
// projection.
func (r *OperatorInventoryReader) facets(ctx context.Context) (*operator.AssetInventoryFacets, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres media: operator inventory reader is not wired")
	}
	mediaTypes := map[string]int64{}
	lifecycleStates := map[string]int64{}
	assetStates := map[string]int64{}
	indexStates := map[string]int64{}
	sources := map[string]int64{}
	providers := map[string]int64{}

	queries := []struct {
		name      string
		keyColumn string
		out       map[string]int64
	}{
		{name: "media_type", keyColumn: "media_type", out: mediaTypes},
		{name: "lifecycle_state", keyColumn: "lifecycle_state", out: lifecycleStates},
		{name: "index_state", keyColumn: "index_state", out: indexStates},
		{name: "source", keyColumn: "source", out: sources},
		{name: "provider", keyColumn: "provider", out: providers},
	}

	for _, q := range queries {
		if err := r.runFacetQuery(ctx, q.name, q.keyColumn, q.out); err != nil {
			return nil, fmt.Errorf("operatorinventory.facets %s: %w", q.name, err)
		}
	}
	if err := r.runAssetStateFacetQuery(ctx, assetStates); err != nil {
		return nil, fmt.Errorf("operatorinventory.facets asset_state: %w", err)
	}

	return &operator.AssetInventoryFacets{
		MediaTypes:      mergeCanonicalFacet(mediaTypes, operator.MediaTypeLabels()),
		LifecycleStates: mergeCanonicalFacet(lifecycleStates, operator.LifecycleStateLabels()),
		AssetStates:     mergeCanonicalFacet(assetStates, operator.AssetStateLabels()),
		IndexStates:     mergeCanonicalFacet(indexStates, operator.IndexStateLabels()),
		Sources:         facetsFromMap(sources),
		Providers:       facetsFromMap(providers),
	}, nil
}

// runAssetStateFacetQuery counts the DERIVED asset_state projection, never the
// physical compatibility column: the facet sidebar must agree with the rows the
// Content Library renders.
func (r *OperatorInventoryReader) runAssetStateFacetQuery(ctx context.Context, out map[string]int64) error {
	q := `SELECT ` + operatorAssetStateProjectionSQL("m") + ` AS k, COUNT(*) AS c
	FROM media_assets m WHERE m.lifecycle_state <> 'DELETED' GROUP BY k`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		out[key] = count
	}
	return rows.Err()
}

func (r *OperatorInventoryReader) runFacetQuery(ctx context.Context, name, keyColumn string, out map[string]int64) error {
	// keyColumn comes from the closed set above, never from caller input, so
	// interpolating it cannot become an injection surface.
	q := `SELECT COALESCE(` + keyColumn + `, '') AS k, COUNT(*) AS c
	FROM media_assets WHERE lifecycle_state <> 'DELETED' GROUP BY k`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var key string
		var count int64
		if err := rows.Scan(&key, &count); err != nil {
			return err
		}
		// An empty source/provider is a real facet ("" = unset); an empty
		// canonical enum value is not a value at all.
		if key == "" && name != "source" && name != "provider" {
			continue
		}
		out[key] = count
	}
	return rows.Err()
}

// mergeCanonicalFacet ensures every canonical value appears in the facet
// group, even when the database count is zero. Labels are supplied by helpers
// in the domain package. The returned slice is sorted by code for stable
// output.
func mergeCanonicalFacet(counts map[string]int64, labels map[string]string) []operator.FacetGroup {
	out := make([]operator.FacetGroup, 0, len(labels))
	for code, label := range labels {
		out = append(out, operator.FacetGroup{
			Code:  code,
			Label: label,
			Count: counts[code],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// facetsFromMap renders a raw value distribution (source / provider) where the
// value IS its own label.
func facetsFromMap(m map[string]int64) []operator.FacetGroup {
	out := make([]operator.FacetGroup, 0, len(m))
	for code, count := range m {
		out = append(out, operator.FacetGroup{Code: code, Label: code, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// parseOperatorTime parses the RFC3339 / naive-timestamp TEXT columns the
// media SSOT mirrors from SQLite. An unparseable or empty stamp yields the
// zero time, exactly as the retired SQLite read model did.
func parseOperatorTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}
