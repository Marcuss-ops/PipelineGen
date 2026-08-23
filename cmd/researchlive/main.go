// Command researchlive runs the live multi-provider research chain against
// real services: SearXNG (SEARXNG_URL, default http://127.0.0.1:8080) and
// DuckDuckGo HTML. It wires the exact production components used by
// wire_script_resolvers.go — provider registry, MultiWebSearcher,
// ResearchSearchCoordinator, WebResearchResolver, page fetcher, and the
// sqlite topicsource cache — and exercises:
//
//  1. single-candidate fanout for Floyd Mayweather Jr., Canelo Álvarez,
//     Roberto Durán, Smokin' Joe Frazier (coordinator escalation logs,
//     subject filter, quality gate, EvidencePack, cache save + replay);
//  2. the 10-subject aggregate fanout with the same verifications.
//
// The ranker is a deterministic evidence-volume stand-in for Ollama (the
// live test targets the research chain, not LLM ranking). Exit code is 0
// only when every subject passes the quality gate and the cache replays.
//
// Usage:
//
//	go run ./cmd/researchlive
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	usecase "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/research"
	webclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/webresearch"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"

	_ "github.com/mattn/go-sqlite3"
)

// liveTargetPool mirrors RESEARCH_TARGET_POOL_SIZE (default 8): the
// coordinator stops collecting subject-valid sources once the pool
// reaches this size. It appears in the startup line, the cache
// fingerprint policy token, and every final report line. Set once in
// main() from the RESEARCH_TARGET_POOL_SIZE env var — the same variable
// production wiring reads — so the live run exercises exactly the
// configured policy.
var liveTargetPool = 8

// searxngAdapter mirrors the production searxngWebSearchProviderAdapter
// (internal/app/composition_helpers.go): the Ollama SearXNG client exposed
// as a scriptports.WebSearchProvider.
type searxngAdapter struct{ searcher *webclient.WebSearcher }

func main() {
	log, logErr := zap.NewDevelopment()
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "logger: %v\n", logErr)
		os.Exit(1)
	}
	defer log.Sync()

	timeout := time.Duration(envInt("WEBSEARCH_TIMEOUT_SECONDS", 30)) * time.Second
	searxngURL := envOr("SEARXNG_URL", "http://127.0.0.1:8080")
	// target pool from the same env var production wiring reads
	// (RESEARCH_TARGET_POOL_SIZE, default 8); clamp to a sane minimum so a
	// mis-set value can not disable the early stop.
	liveTargetPool = envInt("RESEARCH_TARGET_POOL_SIZE", 8)
	if liveTargetPool <= 0 {
		liveTargetPool = 8
	}

	ws := webclient.NewWebSearcherWithConfig(webclient.WebSearcherConfig{
		BaseURL: searxngURL, MaxResults: envInt("SEARXNG_MAX_RESULTS", 10), Timeout: timeout,
		Language: "en", Categories: "general", SafeSearch: 0,
	})
	providers := []scriptports.WebSearchProvider{
		&searxngAdapter{searcher: ws},
		webresearch.NewDuckDuckGoSearchProvider(log),
	}
	multi := webresearch.NewMultiWebSearcher(log, providers...)
	fetcher := webresearch.NewPageFetcher(timeout, 2<<20)
	cache, err := openCache()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cache: %v\n", err)
		os.Exit(1)
	}

	coordinator := research.NewResearchSearchCoordinator(
		&research.SubjectIdentityAdapter{
			Resolve: func(subject string) scriptpkg.SubjectIdentity {
				return usecase.NewSubjectIdentityResolver().Resolve(subject)
			},
		},
		&research.QueryPlannerAdapter{
			FullPlan: func(identity scriptpkg.SubjectIdentity, maxQueries int) []string {
				return usecase.NewQueryPlanner().FullPlan(identity, maxQueries)
			},
		},
		providers, log,
	)
	coordinator.SetTargetPool(liveTargetPool)

	// Install the repository lexicon registry exactly like the composition
	// root (internal/app/lexicon_bootstrap.go): config/lexicons relative to
	// the module root.
	lexiconRoot := envOr("VELOX_LEXICON_ROOT", "config/lexicons")
	lexRegistry, err := linguistics.NewLexiconRegistry(lexiconRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lexicon: %v (run from the module root, refactored/)\n", err)
		os.Exit(1)
	}
	if err := linguistics.SetDefaultLexicon(lexRegistry); err != nil {
		fmt.Fprintf(os.Stderr, "lexicon: install default registry: %v\n", err)
		os.Exit(1)
	}

	resolver := usecase.NewWebResearchResolver(multi, fetcher)
	resolver.SetCache(cache)
	if err := resolver.SetLexicon(linguistics.DefaultLexicon()); err != nil {
		fmt.Fprintf(os.Stderr, "lexicon: %v\n", err)
		os.Exit(1)
	}
	resolver.SetResearchRanker(scriptports.ResearchRankerFunc(evidenceRanker))
	resolver.SetSearchCoordinator(&coordinatorBridge{c: coordinator})
	resolver.SetResearchPolicyVersion(fmt.Sprintf("provider=searxng+duckduckgo,target_pool=%d", liveTargetPool))

	fmt.Printf("live research: searxng=%s providers=%s target_pool=%d\n",
		searxngURL, strings.Join(multi.ProviderNames(), "+"), liveTargetPool)

	ctx := context.Background()
	// Watchdog: instead of aborting when both providers are rate-limited,
	// keep probing every 30s and start automatically as soon as one
	// recovers (up to RESEARCHLIVE_MAX_WAIT_MINUTES).
	if !waitForProviders(ctx, providers) {
		fmt.Printf("\nLIVE SUMMARY: ABORTED — both providers down after the wait budget; retry later\n")
		os.Exit(2)
	}
	allPass := true

	// -subject "Name" runs a single subject only (lightest provider load).
	only := ""
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "-subject" && i+1 < len(os.Args) {
			only = os.Args[i+1]
		}
	}
	if only != "" {
		if !runSubject(ctx, resolver, only, log) {
			allPass = false
		}
		fmt.Printf("\nLIVE SUMMARY: %s\n", map[bool]string{true: "ALL PASS", false: "FAILURES PRESENT"}[allPass])
		if !allPass {
			os.Exit(1)
		}
		return
	}

	phase1 := []string{"Floyd Mayweather Jr.", "Canelo Álvarez", "Roberto Durán", "Smokin' Joe Frazier"}
	still := runSubjectsWithWatchdog(ctx, resolver, providers, phase1)
	if len(still) > 0 {
		allPass = false
		fmt.Printf("phase 1 subjects still failing after retries: %v\n", still)
	}

	ten := []string{
		"Floyd Mayweather Jr.", "Canelo Alvarez", "Mike Tyson", "Manny Pacquiao",
		"Oscar De La Hoya", "Tyson Fury", "Anthony Joshua", "George Foreman",
		"Evander Holyfield", "Lennox Lewis",
	}
	aggregateOK, aggregateFailed := runAggregate(ctx, resolver, ten, log)
	if !aggregateOK {
		allPass = false
	}
	// Watchdog: retry the aggregate candidates that failed (usually a
	// mid-run provider outage) once the providers recover.
	still = runSubjectsWithWatchdog(ctx, resolver, providers, aggregateFailed)
	if len(still) > 0 {
		fmt.Printf("aggregate candidates still failing after retries: %v\n", still)
	} else if len(aggregateFailed) > 0 {
		fmt.Printf("watchdog: all %d failed aggregate candidate(s) recovered individually (aggregate pack not re-produced)\n", len(aggregateFailed))
	}

	fmt.Printf("\nLIVE SUMMARY: %s\n", map[bool]string{true: "ALL PASS", false: "FAILURES PRESENT"}[allPass])
	if !allPass {
		os.Exit(1)
	}
}
