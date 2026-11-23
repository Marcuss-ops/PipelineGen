package scriptgeneration

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	generationcontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/generation"

	"github.com/Marcuss-ops/PipelineGen/pkg/providerbudget"
)

// TestSemanticSearchBatchSerialAndConcurrentMergeIdentically is the local
// 1-vs-N contract: the same plan and the same provider answers must produce the
// same attempt report, ordering and resolved assets whether the ladders run one
// at a time or concurrently. Latencies are measured wall time, so they are
// normalized; every decision field stays compared.
func TestSemanticSearchBatchSerialAndConcurrentMergeIdentically(t *testing.T) {
	identities := schedulerIdentities()
	queries := []string{"Alberto Stasi", "Andrea Sempio", "Corte d'Appello", "Vigevano"}
	entityIDs := []string{"person_01", "person_02", "org_01", "loc_01"}
	candidates := make([]SemanticProductionCandidate, 0, len(queries))
	hits := make(map[string][]SemanticAssetHit, len(queries))
	for i, query := range queries {
		candidates = append(candidates, planCandidate(
			"vc_"+string(rune('a'+i)), "entity_search", "s01", i+1,
			[]string{entityIDs[i]}, "entity:"+entityIDs[i],
			generationcontract.SemanticSearchQuery{Kind: "exact_mention", Text: query},
		))
		hits[query] = []SemanticAssetHit{{
			AssetID: "asset_" + string(rune('a'+i)),
			URL:     "https://assets.invalid/" + string(rune('a'+i)) + ".jpg",
			SHA256:  strings.Repeat(string(rune('a'+i)), 64),
		}}
	}

	run := func(workers int) (*SemanticProductionPlan, *SemanticSearchBatchReport) {
		plan := schedulerPlan(t, identities, candidates...)
		port := newSchedulerTestPort()
		port.hits = hits
		port.delay = 2 * time.Millisecond
		planner, err := NewSemanticSearchBatchPlanner(port, SemanticSearchBatchConfig{
			MaxConcurrency: workers,
			DisableCache:   true,
			Budget:         providerbudget.New(providerbudget.Config{Interval: 0, Burst: 16}),
		})
		if err != nil {
			t.Fatalf("planner workers=%d: %v", workers, err)
		}
		report, err := planner.Run(context.Background(), plan, "it", false)
		if err != nil {
			t.Fatalf("batch workers=%d: %v", workers, err)
		}
		return plan, report
	}

	serialPlan, serialReport := run(1)
	concurrentPlan, concurrentReport := run(len(queries))

	normalize := func(rows []SemanticSearchAttemptResult) []SemanticSearchAttemptResult {
		out := append([]SemanticSearchAttemptResult(nil), rows...)
		for i := range out {
			out[i].LatencyMS = 0
		}
		return out
	}
	if !reflect.DeepEqual(normalize(serialReport.Attempts), normalize(concurrentReport.Attempts)) {
		t.Fatalf("1-vs-N attempt report drift:\nserial: %+v\nconcurrent: %+v",
			serialReport.Attempts, concurrentReport.Attempts)
	}
	for i := range serialPlan.Units {
		if serialPlan.Units[i].ResolvedAssetID != concurrentPlan.Units[i].ResolvedAssetID ||
			serialPlan.Units[i].SearchStatus != concurrentPlan.Units[i].SearchStatus {
			t.Fatalf("unit %d drift: serial=%+v concurrent=%+v",
				i, serialPlan.Units[i], concurrentPlan.Units[i])
		}
	}
	if serialReport.Metrics.Hits != concurrentReport.Metrics.Hits ||
		serialReport.Metrics.Misses != concurrentReport.Metrics.Misses ||
		serialReport.Metrics.HitRate != concurrentReport.Metrics.HitRate {
		t.Fatalf("1-vs-N metric drift: serial=%+v concurrent=%+v",
			serialReport.Metrics, concurrentReport.Metrics)
	}
}
