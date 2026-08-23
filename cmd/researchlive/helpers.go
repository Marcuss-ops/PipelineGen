package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/topicsourcecache"
	"go.uber.org/zap"

	_ "github.com/mattn/go-sqlite3"
)

func (a *searxngAdapter) Name() string { return "searxng" }

func (a *searxngAdapter) Search(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	results, err := a.searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	out := make([]scriptports.WebSearchHit, 0, len(results))
	for _, r := range results {
		out = append(out, scriptports.WebSearchHit{Title: r.Title, URL: r.URL, Content: r.Content})
	}
	return out, nil
}

// coordinatorBridge satisfies the (unexported) researchSearchCoordinatorPort
// structurally: Go interfaces are satisfied by method sets, so the cmd can
// wire the coordinator into the resolver without naming the port type.
type coordinatorBridge struct {
	c *app.ResearchSearchCoordinator
}

func (b *coordinatorBridge) SearchWithFallback(ctx context.Context, subject string, queries []string, targetPool int) []usecase.CoordinatorSearchResult {
	results := b.c.SearchWithFallback(ctx, subject, queries, targetPool)
	out := make([]usecase.CoordinatorSearchResult, len(results))
	for i, r := range results {
		out[i] = usecase.CoordinatorSearchResult{Hit: r.Hit, Provider: r.Provider, QueryLevel: r.QueryLevel}
	}
	return out
}

// evidenceRanker is the deterministic live-test stand-in for the Ollama
// ranker: order by evidence volume (sources, then verified claims), tie-break
// by candidate id. It never invents numbers.
func evidenceRanker(_ context.Context, _ string, _ scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
	type item struct {
		id      string
		sources int
		claims  int
	}
	items := make([]item, 0, len(inputs))
	for _, in := range inputs {
		verified := 0
		for _, claim := range in.Claims {
			if claim.Verified {
				verified++
			}
		}
		items = append(items, item{id: in.CandidateID, sources: len(in.Sources), claims: verified})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].sources == items[j].sources {
			if items[i].claims == items[j].claims {
				return items[i].id < items[j].id
			}
			return items[i].claims > items[j].claims
		}
		return items[i].sources > items[j].sources
	})
	out := make([]scriptports.ResearchCandidateRanking, 0, len(items))
	for i, it := range items {
		out = append(out, scriptports.ResearchCandidateRanking{
			CandidateID: it.id, Rank: i + 1, Score: float64(it.sources),
			Rationale: "deterministic live-test ranker by evidence volume",
		})
	}
	return scriptports.ResearchRankingResult{Ranking: out}, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// researchSchema is the final research_cache schema after migrations
// 014 + 174 + 187 (migrations/sqlite).
const researchSchema = `
CREATE TABLE IF NOT EXISTS research_cache (
	key TEXT PRIMARY KEY,
	topic TEXT NOT NULL,
	language TEXT NOT NULL,
	max_steps INTEGER NOT NULL,
	source_text TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now')),
	last_used TEXT NOT NULL DEFAULT (datetime('now')),
	concept_id TEXT,
	topic_fingerprint TEXT,
	source_fingerprint TEXT,
	resolver_version TEXT,
	research_version TEXT,
	hit_count INTEGER NOT NULL DEFAULT 0,
	expires_at DATETIME,
	updated_at TEXT NOT NULL DEFAULT (datetime('now')),
	source_text_hash TEXT NOT NULL DEFAULT '',
	research_report_json TEXT NOT NULL DEFAULT '',
	sources_count INTEGER NOT NULL DEFAULT 0,
	claims_verified INTEGER NOT NULL DEFAULT 0,
	claims_rejected INTEGER NOT NULL DEFAULT 0,
	search_query_count INTEGER NOT NULL DEFAULT 0,
	pages_fetched INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_research_cache_topic ON research_cache(topic);
CREATE INDEX IF NOT EXISTS idx_research_cache_last_used ON research_cache(last_used);
`

func openCache() (*topicsourcecache.Repository, error) {
	db, err := sql.Open("sqlite3", "file:researchlive?mode=memory&cache=shared&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(researchSchema); err != nil {
		return nil, err
	}
	return topicsourcecache.NewRepository(db), nil
}

// researchSource builds the fanout SourceSpec used for every live run:
// prefer_cache so the save + replay path is exercised, MinSources 3 +
// one full page per the financial research gate.
func researchSource(topic string, candidates []string) scriptpkg.SourceSpec {
	return scriptpkg.SourceSpec{
		Type:        scriptpkg.SourceResearch,
		Topic:       topic,
		Search:      true,
		Research:    scriptpkg.ResearchPolicy{Candidates: candidates, MaxQueries: 4, MinSources: 3, MaxPages: 8, RequireCitations: true},
		CachePolicy: scriptpkg.SourceCachePolicy{Mode: scriptpkg.SourceCacheModePreferCache},
	}
}

func runSubject(ctx context.Context, resolver *usecase.WebResearchResolver, subject string, log *zap.Logger) bool {
	src := researchSource(subject+" boxing career earnings", []string{subject})
	fmt.Printf("\n=== LIVE RESEARCH: %s ===\n", subject)
	res, err := resolver.Resolve(ctx, src, scriptpkg.SourceResolutionContext{ItemID: "live:" + subject, Language: "en"})
	if err != nil {
		fmt.Printf("  RESULT: FAIL (%v)\n", err)
		return false
	}
	report := res.ResearchReport
	pack := res.ResearchEvidence
	fmt.Printf("  report: mode=%s status=%s accepted=%d full_page=%d snippet=%d evidence=%.2f gate=%t cache_saved=%t target_pool=%d\n",
		report.Mode, report.Status, report.AcceptedSources, report.FullPageSources, report.SnippetSources,
		report.EvidenceScore, report.QualityGatePassed, report.CacheSaved, liveTargetPool)
	for _, q := range report.Queries {
		fmt.Printf("  query: %s\n", q)
	}
	packOK := pack != nil && len(pack.Candidates) == 1
	if packOK {
		c := pack.Candidates[0]
		fmt.Printf("  evidence_pack: candidate=%s rank=%d score=%.1f sources=%d claims=%d fingerprint=%.12s...\n",
			c.CandidateID, c.Rank, c.Score, len(c.Sources), len(c.Claims), pack.Fingerprint)
	}

	// Cache replay: the identical source spec must resolve from cache.
	res2, err2 := resolver.Resolve(ctx, src, scriptpkg.SourceResolutionContext{ItemID: "live:" + subject, Language: "en"})
	replay := err2 == nil && res2 != nil && res2.ResearchReport != nil && res2.ResearchReport.CacheHit
	fmt.Printf("  cache_replay: %s (mode=%s)\n", map[bool]string{true: "PASS", false: "FAIL"}[replay], replayOrMode(err2, res2))

	pass := err == nil && report.QualityGatePassed && report.AcceptedSources >= 3 && packOK && replay
	fmt.Printf("  RESULT: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[pass])
	return pass
}

func replayOrMode(err error, res *scriptpkg.ResolvedSource) string {
	if err != nil {
		return "error: " + err.Error()
	}
	if res == nil || res.ResearchReport == nil {
		return "no-report"
	}
	return res.ResearchReport.Mode
}

// runAggregate resolves the multi-candidate aggregate source. On failure
// it also runs per-candidate diagnostics (fresh single runs) and returns
// the list of candidates that still failed — the watchdog retries those
// after the providers recover.
func runAggregate(ctx context.Context, resolver *usecase.WebResearchResolver, subjects []string, log *zap.Logger) (bool, []string) {
	topic := "The 10 Richest Boxers of All Time"
	src := researchSource(topic, subjects)
	fmt.Printf("\n=== LIVE AGGREGATE: %d SUBJECTS ===\n", len(subjects))
	res, err := resolver.Resolve(ctx, src, scriptpkg.SourceResolutionContext{ItemID: "live:aggregate", Language: "en"})
	if err != nil {
		fmt.Printf("  RESULT: FAIL (%v)\n", err)
		fmt.Printf("  --- per-candidate diagnostics (fresh single runs) ---\n")
		var failed []string
		for _, subject := range subjects {
			if !runSubject(ctx, resolver, subject, log) {
				failed = append(failed, subject)
			}
		}
		return false, failed
	}
	report := res.ResearchReport
	pack := res.ResearchEvidence
	// The aggregate report does not populate full_page/snippet/evidence
	// counters (they are per-candidate); derive a display-only evidence
	// score from the pack (full page 1.0, snippet 0.55).
	displayFull, displaySnippet := 0, 0
	displayEvidence := 0.0
	for _, c := range pack.Candidates {
		for _, s := range c.Sources {
			switch s.AccessMode {
			case scriptpkg.EvidenceAccessFullPage:
				displayFull++
				displayEvidence += 1.0
			case scriptpkg.EvidenceAccessSnippet:
				displaySnippet++
				displayEvidence += 0.55
			}
		}
	}
	fmt.Printf("  report: mode=%s status=%s accepted=%d full_page=%d snippet=%d evidence=%.2f gate=%t cache_saved=%t target_pool=%d\n",
		report.Mode, report.Status, report.AcceptedSources, displayFull, displaySnippet,
		displayEvidence, report.QualityGatePassed, report.CacheSaved, liveTargetPool)
	if pack == nil {
		fmt.Printf("  evidence_pack: MISSING\n")
		return false, nil
	}
	packageSources, packageClaims := 0, 0
	for _, c := range pack.Candidates {
		packageSources += len(c.Sources)
		packageClaims += len(c.Claims)
	}
	fmt.Printf("  evidence_pack: candidates=%d sources=%d claims=%d fingerprint=%.12s...\n",
		len(pack.Candidates), packageSources, packageClaims, pack.Fingerprint)
	for _, c := range pack.Candidates {
		full, snippet := 0, 0
		for _, s := range c.Sources {
			switch s.AccessMode {
			case scriptpkg.EvidenceAccessFullPage:
				full++
			case scriptpkg.EvidenceAccessSnippet:
				snippet++
			}
		}
		fmt.Printf("    rank %2d  %-22s score=%.1f sources=%d (full=%d snippet=%d)\n", c.Rank, c.CandidateID, c.Score, len(c.Sources), full, snippet)
	}

	res2, err2 := resolver.Resolve(ctx, src, scriptpkg.SourceResolutionContext{ItemID: "live:aggregate", Language: "en"})
	replay := err2 == nil && res2 != nil && res2.ResearchReport != nil && res2.ResearchReport.CacheHit
	fmt.Printf("  cache_replay: %s (mode=%s)\n", map[bool]string{true: "PASS", false: "FAIL"}[replay], replayOrMode(err2, res2))

	pass := err == nil && report.QualityGatePassed && len(pack.Candidates) == len(subjects) && replay
	fmt.Printf("  RESULT: %s\n", map[bool]string{true: "PASS", false: "FAIL"}[pass])
	return pass, nil
}

// probeProviders issues one real query per provider and reports which are
// usable. Live providers degrade without notice (SearXNG engines suspended,
// DDG bot challenges), so the runner aborts early when BOTH are down
// instead of burning minutes on guaranteed-fail research. Returns true when
// at least one provider answered with hits.
func probeProviders(ctx context.Context, providers []scriptports.WebSearchProvider) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	anyUp := false
	for _, p := range providers {
		hits, err := p.Search(probeCtx, "Floyd Mayweather boxing", 3)
		if err != nil {
			fmt.Printf("probe %-10s DOWN (%v)\n", p.Name(), err)
			continue
		}
		fmt.Printf("probe %-10s UP (%d hits)\n", p.Name(), len(hits))
		anyUp = true
	}
	return anyUp
}

// waitForProviders polls probeProviders every 30s until at least one
// provider answers with hits or the wait budget is exhausted
// (RESEARCHLIVE_MAX_WAIT_MINUTES, default 15). This lets the runner start
// (or retry) automatically as soon as the rate-limit cooldown clears,
// instead of aborting or burning calls on guaranteed-fail research.
func waitForProviders(ctx context.Context, providers []scriptports.WebSearchProvider) bool {
	maxWait := time.Duration(envInt("RESEARCHLIVE_MAX_WAIT_MINUTES", 15)) * time.Minute
	deadline := time.Now().Add(maxWait)
	first := true
	for {
		if probeProviders(ctx, providers) {
			return true
		}
		if time.Now().After(deadline) {
			fmt.Printf("watchdog: providers still down after %s, giving up\n", maxWait)
			return false
		}
		if first {
			fmt.Printf("watchdog: providers rate-limited — probing every 30s (up to %s)...\n", maxWait)
			first = false
		}
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
			return false
		}
	}
}

// runSubjectsWithWatchdog runs each subject as a single-candidate
// resolution and retries the failures after the providers recover from
// rate-limiting. Round 0 runs immediately; each later round first waits
// for at least one provider to answer a probe. Stops when everything
// passes, retries are exhausted (RESEARCHLIVE_MAX_RETRIES, default 2), or
// the wait times out. Returns the subjects still failing.
func runSubjectsWithWatchdog(ctx context.Context, resolver *usecase.WebResearchResolver, providers []scriptports.WebSearchProvider, subjects []string) []string {
	retries := envInt("RESEARCHLIVE_MAX_RETRIES", 2)
	pending := append([]string(nil), subjects...)
	for round := 0; round <= retries && len(pending) > 0; round++ {
		if round > 0 {
			if !waitForProviders(ctx, providers) {
				break
			}
			fmt.Printf("watchdog: retry round %d for %d failed subject(s)\n", round, len(pending))
		}
		var next []string
		for _, s := range pending {
			if !runSubject(ctx, resolver, s, nil) {
				next = append(next, s)
			}
		}
		pending = next
	}
	return pending
}
