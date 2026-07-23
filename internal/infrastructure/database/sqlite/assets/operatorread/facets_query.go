package operatorread

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/operator"
)

func (r *InventoryReader) facets(ctx context.Context) (*operator.AssetInventoryFacets, error) {
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
		{name: "asset_state", keyColumn: "asset_state", out: assetStates},
		{name: "index_state", keyColumn: "index_state", out: indexStates},
		{name: "source", keyColumn: "source", out: sources},
		{name: "provider", keyColumn: "provider", out: providers},
	}

	for _, q := range queries {
		if err := r.runFacetQuery(ctx, q.name, q.keyColumn, q.out); err != nil {
			return nil, fmt.Errorf("operatorread.facets %s: %w", q.name, err)
		}
	}

	return &operator.AssetInventoryFacets{
		MediaTypes:      mergeCanonical(mediaTypes, operator.MediaTypeLabels()),
		LifecycleStates: mergeCanonical(lifecycleStates, operator.LifecycleStateLabels()),
		AssetStates:     mergeCanonical(assetStates, operator.AssetStateLabels()),
		IndexStates:     mergeCanonical(indexStates, operator.IndexStateLabels()),
		Sources:         fromMap(sources),
		Providers:       fromMap(providers),
	}, nil
}

func (r *InventoryReader) runFacetQuery(ctx context.Context, name, keyColumn string, out map[string]int64) error {
	q := fmt.Sprintf(`SELECT COALESCE(%s, '') AS k, COUNT(*) AS c FROM media_assets WHERE lifecycle_state != 'DELETED' GROUP BY k`, keyColumn)
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
		if key == "" && name != "source" && name != "provider" {
			continue
		}
		out[key] = count
	}
	return rows.Err()
}

// mergeCanonical ensures every canonical value appears in the facet group,
// even when the database count is zero. Labels are supplied by helpers in
// the domain package.
func mergeCanonical(counts map[string]int64, labels map[string]string) []operator.FacetGroup {
	out := make([]operator.FacetGroup, 0, len(labels))
	for code, label := range labels {
		out = append(out, operator.FacetGroup{
			Code:  code,
			Label: label,
			Count: counts[code],
		})
	}
	return out
}

func fromMap(m map[string]int64) []operator.FacetGroup {
	out := make([]operator.FacetGroup, 0, len(m))
	for code, count := range m {
		out = append(out, operator.FacetGroup{Code: code, Label: code, Count: count})
	}
	return out
}
