// Package imagesearch — live_relevance_e2e_test.go is the LIVE relevance
// harness for Image Search. It closes the last gap of the golden battery:
//
//	FRASE → Extraction → Canonicalizzazione → Image Search Query
//	      → Risultati → Relevance validation → Immagine scelta
//
// The unit battery (golden_battery_test.go) certifies up to the ordered
// Image Search Query. This harness runs those exact queries against the LIVE
// providers (native Pexels image search + the Artlist fallback-chain video
// searchers) and certifies the result side:
//
//	relevance@1 / relevance@3 / relevance@5   (requires human labels)
//	wrong-identity rate                       (deterministic, from the
//	                                           battery's forbidden/negated
//	                                           metadata — no human judgment)
//	negated-person selection                  (deterministic, must be zero)
//
// WHY HUMAN LABELS FOR relevance@k: both live surfaces echo the search term
// into the candidate title ("Pexels image: <term> by <photographer>"), so a
// candidate's metadata alone cannot prove visual relevance. The harness
// therefore records the live candidate snapshot and computes the two
// metrics that ARE decidable without vision deterministically (wrong
// identity + negated person), and the relevance@k floors from a committed
// human label file. This is the same split the battery spec draws: identity
// correctness is machine-verifiable, visual relevance is a judgment call.
//
// WORKFLOW (operator):
//
//	1. IMAGESEARCH_LIVE_E2E=1 PEXELS_API_KEY=... go test ./internal/capabilities/imagesearch/ -run TestLiveImageSearchRelevance_Record -v
//	   → writes testdata/live_relevance_snapshot.json (never committed empty).
//	2. Label each candidate in testdata/live_relevance_labels.json
//	   (relevant / wrong_identity per candidate id, per case).
//	3. Commit snapshot + labels. From then on the normal suite certifies
//	   them: go test ./internal/capabilities/imagesearch/ -run TestImageSearchLiveRelevance_Certify -v
//
// RATE LIMITS: Pexels free tier is ~200 req/hour. The full battery is
// ~28 cases × 1-4 queries × 2 providers, so a full record run can approach
// the limit. Use IMAGESEARCH_E2E_CASES=T18,T19,T27 to record the golden
// regression pairs only, and IMAGESEARCH_E2E_QUERY_LIMIT to shrink the
// per-query candidate cap (default 10).
package imagesearch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	assetproviders "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/fallback"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/imagery/pexels"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

const (
	liveE2EEnv        = "IMAGESEARCH_LIVE_E2E"
	envPexelsAPIKey   = "PEXELS_API_KEY"
	envPexelsBaseURL  = "PEXELS_BASE_URL"
	envPixabayAPIKey  = "PIXABAY_API_KEY"
	envCaseFilter     = "IMAGESEARCH_E2E_CASES"
	envQueryLimit     = "IMAGESEARCH_E2E_QUERY_LIMIT"
	defaultSnapshotPath = "testdata/live_relevance_snapshot.json"
	defaultLabelsPath   = "testdata/live_relevance_labels.json"

	liveSnapshotSchema = "live-relevance-v1"
	liveLabelsSchema   = "live-relevance-labels-v1"
	// liveDefaultQueryLimit caps candidates per (query, provider).
	liveDefaultQueryLimit = 10
	// liveCertifyTopK is the relevance window the certification floors use.
	liveCertifyTopK = 5
)

// ── Snapshot / label file shapes ──────────────────────────────────────

// liveCandidate is the recorded, provider-agnostic surface of one search hit.
// Title and PageURL are the only fields the wrong-identity oracle reads, so
// both are always populated by the record step.
type liveCandidate struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	PageURL      string `json:"page_url"`
	PreviewURL   string `json:"preview_url"`
	ThumbnailURL string `json:"thumbnail_url"`
	Creator      string `json:"creator,omitempty"`
	Provider     string `json:"provider"`
}

// liveQueryResults groups one provider's candidates for one query.
type liveQueryResults struct {
	Provider   string          `json:"provider"`
	Query      string          `json:"query"`
	Candidates []liveCandidate `json:"candidates"`
}

// liveCaseResults is the full recorded surface of one battery case.
type liveCaseResults struct {
	ID      string              `json:"id"`
	Text    string              `json:"text"`
	Queries []string            `json:"queries"`
	Results []liveQueryResults  `json:"results"`
}

// liveSnapshot is the committed record of one live run. It is the input of
// the certify test; labels are kept in a separate file so re-recording never
// overwrites human judgments.
type liveSnapshot struct {
	Schema     string            `json:"schema"`
	RecordedAt string            `json:"recorded_at"`
	Providers  []string          `json:"providers"`
	Cases      []liveCaseResults `json:"cases"`
}

// liveCandidateLabel is the human judgment for one candidate of one case.
type liveCandidateLabel struct {
	Relevant      bool `json:"relevant"`
	WrongIdentity bool `json:"wrong_identity"`
}

// liveLabels maps case id → candidate id → human judgment.
type liveLabels struct {
	Schema string                              `json:"schema"`
	Labels map[string]map[string]liveCandidateLabel `json:"labels"`
}

// ── Live searcher adapters ────────────────────────────────────────────

// liveSearcher is the uniform live-search surface used by the record step.
// Both provider families return providerassets.ProviderAsset, so one adapter
// type covers image and video surfaces.
type liveSearcher interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]liveCandidate, error)
}

type pexelsImageSearcher struct {
	p *pexels.Provider
}

func (s pexelsImageSearcher) Name() string { return "pexels_images" }

func (s pexelsImageSearcher) Search(ctx context.Context, query string, limit int) ([]liveCandidate, error) {
	res, err := s.p.Search(ctx, assetproviders.SearchRequest{Query: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	return mapLiveCandidates(res.Candidates, s.Name()), nil
}

type pexelsVideoSearcher struct {
	p *fallback.Pexels
}

func (s pexelsVideoSearcher) Name() string { return "pexels_videos" }

func (s pexelsVideoSearcher) Search(ctx context.Context, query string, limit int) ([]liveCandidate, error) {
	cands, err := s.p.Search(ctx, artapp.SearchRequest{Term: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	return mapLiveCandidates(cands, s.Name()), nil
}

type pixabayVideoSearcher struct {
	p *fallback.Pixabay
}

func (s pixabayVideoSearcher) Name() string { return "pixabay_videos" }

func (s pixabayVideoSearcher) Search(ctx context.Context, query string, limit int) ([]liveCandidate, error) {
	cands, err := s.p.Search(ctx, artapp.SearchRequest{Term: query, Limit: limit})
	if err != nil {
		return nil, err
	}
	return mapLiveCandidates(cands, s.Name()), nil
}

func mapLiveCandidates(assets []providerassets.ProviderAsset, provider string) []liveCandidate {
	out := make([]liveCandidate, 0, len(assets))
	for _, a := range assets {
		out = append(out, liveCandidate{
			ID:           strings.TrimSpace(a.ID),
			Title:        strings.TrimSpace(a.Title),
			PageURL:      strings.TrimSpace(a.PageURL),
			PreviewURL:   strings.TrimSpace(a.PreviewURL),
			ThumbnailURL: strings.TrimSpace(a.ThumbnailURL),
			Creator:      strings.TrimSpace(a.Creator),
			Provider:     provider,
		})
	}
	return out
}

// ── Deterministic wrong-identity oracle ───────────────────────────────

// caseForbiddenTokens returns every phrase that must NEVER drive an image for
// this case: the battery's forbidden queries, forbidden entities and negated
// entities. A live candidate whose title/page URL contains one of these is a
// wrong-identity hit (e.g. a Michael B. Jordan actor photo for T18, a Mike
// Tyson photo for T27).
func caseForbiddenTokens(gc goldenCase) []string {
	out := make([]string, 0, len(gc.forbidQueries)+len(gc.forbidEntities)+len(gc.wantNegated))
	out = append(out, gc.forbidQueries...)
	for _, e := range gc.forbidEntities {
		out = append(out, e.text)
	}
	for _, e := range gc.wantNegated {
		out = append(out, e.text)
	}
	return out
}

// wrongIdentityReason returns the forbidden phrase matched by the candidate
// ("" when the candidate is not a wrong-identity hit). Matching is a
// lower-case substring scan over Title + PageURL — the two provider-authored
// fields. The battery's forbidden phrases are designed to NOT be substrings
// of the expected queries (T19 forbids "Michael Jordan" — "michael b jordan"
// never contains the adjacent pair), so the oracle is precise on the golden
// pairs it must police.
func wrongIdentityReason(c liveCandidate, forbidden []string) string {
	hay := strings.ToLower(c.Title + " " + c.PageURL)
	for _, phrase := range forbidden {
		phrase = strings.ToLower(strings.TrimSpace(phrase))
		if phrase == "" {
			continue
		}
		if strings.Contains(hay, phrase) {
			return phrase
		}
	}
	return ""
}

// negatedReason returns the negated-entity phrase matched by the candidate.
// It is a subset of the forbidden oracle, kept separate because the battery
// metrics track negated-person selection as its own zero floor.
func negatedReason(c liveCandidate, negated []wantEntity) string {
	hay := strings.ToLower(c.Title + " " + c.PageURL)
	for _, e := range negated {
		phrase := strings.ToLower(strings.TrimSpace(e.text))
		if phrase == "" {
			continue
		}
		if strings.Contains(hay, phrase) {
			return phrase
		}
	}
	return ""
}

// ── Metrics engine (pure — unit-tested without live providers) ────────

// liveCaseMetrics is one case's result-side certification row.
type liveCaseMetrics struct {
	id               string
	imageResults     int // distinct candidates recorded for the case
	topCandidates    []liveCandidate
	relevantTop1     bool // top-1 candidate labeled relevant
	relevantTop3     int  // count of relevant among top-3
	relevantTop5     int  // count of relevant among top-5
	wrongIdentity    int  // wrong-identity hits among top-5
	negatedSeen      int  // negated-person hits among top-5
	wrongSelected    bool // the selected (top-1) image is a wrong identity
	labeled          bool // case has at least one human label
}

func (m liveCaseMetrics) pass() bool {
	return !m.wrongSelected && m.negatedSeen == 0 && (!m.labeled || m.relevantTop1)
}

// distinctCandidateCount counts unique candidates across the whole recorded
// case (all queries and providers), without materializing a top-k slice.
func distinctCandidateCount(res liveCaseResults) int {
	seen := make(map[string]bool)
	n := 0
	for _, qr := range res.Results {
		for _, c := range qr.Candidates {
			if c.ID == "" || seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			n++
		}
	}
	return n
}

// topCandidates merges the case's recorded results in query order, then
// provider order, deduplicating by candidate id and capping at k. The primary
// query leads, mirroring the VidRush fan-out (primary first).
func topCandidates(res liveCaseResults, k int) []liveCandidate {
	seen := make(map[string]bool, k)
	out := make([]liveCandidate, 0, k)
	for _, qr := range res.Results {
		for _, c := range qr.Candidates {
			if c.ID == "" || seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			out = append(out, c)
			if len(out) == k {
				return out
			}
		}
	}
	return out
}

// certifyLiveCase computes one case's metrics from the recorded snapshot and
// (optional) human labels. Wrong-identity and negated detection are always
// deterministic; relevance@k requires labels (a case without labels still
// contributes its deterministic floors, never its relevance numbers).
func certifyLiveCase(gc goldenCase, res liveCaseResults, labels map[string]liveCandidateLabel) liveCaseMetrics {
	m := liveCaseMetrics{id: gc.id}
	m.topCandidates = topCandidates(res, liveCertifyTopK)
	m.imageResults = distinctCandidateCount(res)
	forbidden := caseForbiddenTokens(gc)
	negated := gc.wantNegated
	for i, c := range m.topCandidates {
		label, hasLabel := labels[c.ID]
		if hasLabel {
			m.labeled = true
			if label.Relevant {
				switch i {
				case 0:
					m.relevantTop1 = true
					fallthrough
				case 1, 2:
					m.relevantTop3++
					fallthrough
				default:
					m.relevantTop5++
				}
			}
			if label.WrongIdentity {
				m.wrongIdentity++
				if i == 0 {
					m.wrongSelected = true
				}
			}
			if label.WrongIdentity && negatedReason(c, negated) != "" && i < liveCertifyTopK {
				m.negatedSeen++
			}
			continue
		}
		// No human label: fall back to the deterministic oracle for the
		// identity floors only.
		if reason := wrongIdentityReason(c, forbidden); reason != "" {
			m.wrongIdentity++
			if i == 0 {
				m.wrongSelected = true
			}
		}
		if negatedReason(c, negated) != "" {
			m.negatedSeen++
		}
	}
	return m
}

// liveCertification is the aggregate of a full snapshot run.
type liveCertification struct {
	rows            []liveCaseMetrics
	casesWithResults int
	labeledCases     int
	rel1, rel3, rel5 int // cases with ≥1 relevant candidate in the top-k window
	wrongIdentityTotal int
	wrongSelectedCases int
	negatedTotal       int
}

// certifyLiveSnapshot runs every recorded case through certifyLiveCase and
// aggregates. casesByID supplies the battery metadata (forbidden/negated).
// An abstract case (T24/T25/T26 — wantRequired=false) recorded with queries
// is a snapshot-integrity violation: those sentences must never be searched,
// so the row is poisoned and fails certification.
func certifyLiveSnapshot(snap liveSnapshot, labels *liveLabels, casesByID map[string]goldenCase) liveCertification {
	cert := liveCertification{}
	for _, res := range snap.Cases {
		gc, ok := casesByID[res.ID]
		if !ok {
			continue
		}
		if !gc.wantRequired && len(res.Results) > 0 {
			cert.rows = append(cert.rows, liveCaseMetrics{
				id:            res.ID,
				wrongSelected: true,
			})
			cert.wrongSelectedCases++
			continue
		}
		var caseLabels map[string]liveCandidateLabel
		if labels != nil {
			caseLabels = labels.Labels[res.ID]
		}
		m := certifyLiveCase(gc, res, caseLabels)
		cert.rows = append(cert.rows, m)
		if m.imageResults > 0 {
			cert.casesWithResults++
		}
		if m.labeled {
			cert.labeledCases++
		}
		if m.labeled {
			if m.relevantTop1 {
				cert.rel1++
			}
			if m.relevantTop3 > 0 {
				cert.rel3++
			}
			if m.relevantTop5 > 0 {
				cert.rel5++
			}
		}
		cert.wrongIdentityTotal += m.wrongIdentity
		if m.wrongSelected {
			cert.wrongSelectedCases++
		}
		cert.negatedTotal += m.negatedSeen
	}
	return cert
}

func (c liveCertification) report() string {
	var b strings.Builder
	b.WriteString("\n===== LIVE RELEVANCE: IMAGE SEARCH (Pexels + Artlist chain) =====\n")
	b.WriteString(fmt.Sprintf("%-6s %-8s %-8s %-8s %-8s %-9s %-9s %s\n",
		"ID", "results", "rel@1", "rel@3", "rel@5", "wrongID5", "negated5", "verdict"))
	for _, m := range c.rows {
		verdict := "PASS"
		if !m.pass() {
			verdict = "FAIL"
		}
		rel1 := "-"
		if m.labeled {
			rel1 = fmt.Sprintf("%v", m.relevantTop1)
		}
		rel3 := "-"
		rel5 := "-"
		if m.labeled {
			rel3 = fmt.Sprintf("%d", m.relevantTop3)
			rel5 = fmt.Sprintf("%d", m.relevantTop5)
		}
		b.WriteString(fmt.Sprintf("%-6s %-8d %-8s %-8s %-8s %-9d %-9d %s\n",
			m.id, m.imageResults, rel1, rel3, rel5, m.wrongIdentity, m.negatedSeen, verdict))
	}
	b.WriteString("\n--- metrics ---\n")
	b.WriteString(fmt.Sprintf("cases with results       = %d\n", c.casesWithResults))
	b.WriteString(fmt.Sprintf("labeled cases            = %d (relevance@k requires labels)\n", c.labeledCases))
	if c.labeledCases > 0 {
		b.WriteString(fmt.Sprintf("relevance@1              = %.4f (%d/%d)\n", float64(c.rel1)/float64(c.labeledCases), c.rel1, c.labeledCases))
		b.WriteString(fmt.Sprintf("relevance@3              = %.4f (%d/%d)\n", float64(c.rel3)/float64(c.labeledCases), c.rel3, c.labeledCases))
		b.WriteString(fmt.Sprintf("relevance@5              = %.4f (%d/%d)\n", float64(c.rel5)/float64(c.labeledCases), c.rel5, c.labeledCases))
	} else {
		b.WriteString("relevance@1/3/5          = UNLABELED (commit testdata/live_relevance_labels.json)\n")
	}
	b.WriteString(fmt.Sprintf("wrong-identity top-5     = %d\n", c.wrongIdentityTotal))
	b.WriteString(fmt.Sprintf("wrong-person selection   = %d (must be 0)\n", c.wrongSelectedCases))
	b.WriteString(fmt.Sprintf("negated-person top-5     = %d (must be 0)\n", c.negatedTotal))
	b.WriteString("CERTIFICATION FLOORS: relevance@1>=0.90 relevance@5>=0.80 wrong=0 negated=0\n")
	return b.String()
}

// assertFloors enforces the certification floors. The identity floors are
// always decidable and always enforced; the relevance floors are enforced
// only when a human label file has been committed.
func (c liveCertification) assertFloors(t *testing.T) {
	t.Helper()
	if c.wrongSelectedCases != 0 {
		t.Errorf("wrong-person selection = %d, want 0 (T06/T18/T19/T20/T21/T22/T23/T27)", c.wrongSelectedCases)
	}
	if c.negatedTotal != 0 {
		t.Errorf("negated-person selections = %d, want 0 (T27)", c.negatedTotal)
	}
	for _, m := range c.rows {
		if !m.pass() {
			t.Errorf("case %s failed live relevance certification: %+v", m.id, m)
		}
	}
	if c.labeledCases == 0 {
		t.Log("relevance@k floors not enforced: no committed human labels (run the record step and label testdata/live_relevance_labels.json)")
		return
	}
	rel1 := float64(c.rel1) / float64(c.labeledCases)
	rel5 := float64(c.rel5) / float64(c.labeledCases)
	if rel1 < 0.90 {
		t.Errorf("image relevance@1 = %.4f, want >= 0.90", rel1)
	}
	if rel5 < 0.80 {
		t.Errorf("image relevance@5 = %.4f, want >= 0.80", rel5)
	}
}

// ── Record step (live, env-gated) ─────────────────────────────────────

func liveSearchers(t *testing.T) []liveSearcher {
	t.Helper()
	apiKey := os.Getenv(envPexelsAPIKey)
	if strings.TrimSpace(apiKey) == "" {
		t.Skipf("%s requires %s", liveE2EEnv, envPexelsAPIKey)
	}
	baseURL := os.Getenv(envPexelsBaseURL)
	searchers := []liveSearcher{
		pexelsImageSearcher{p: pexels.NewProvider(pexels.Config{APIKey: apiKey, BaseURL: baseURL, SourceName: "pexels_images"})},
		pexelsVideoSearcher{p: fallback.NewPexels(fallback.Config{APIKey: apiKey, BaseURL: baseURL, SourceName: "pexels_videos"})},
	}
	if pixabayKey := strings.TrimSpace(os.Getenv(envPixabayAPIKey)); pixabayKey != "" {
		searchers = append(searchers, pixabayVideoSearcher{p: fallback.NewPixabay(fallback.Config{APIKey: pixabayKey, SourceName: "pixabay_videos"})})
	}
	return searchers
}

func liveQueryLimit() int {
	limit := liveDefaultQueryLimit
	if raw := strings.TrimSpace(os.Getenv(envQueryLimit)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}
	return limit
}

// caseFilter returns the comma-separated case-id allowlist from the env, or
// nil for "all cases".
func caseFilter() []string {
	raw := strings.TrimSpace(os.Getenv(envCaseFilter))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func inFilter(filter []string, id string) bool {
	if filter == nil {
		return true
	}
	for _, f := range filter {
		if f == id {
			return true
		}
	}
	return false
}

// TestLiveImageSearchRelevance_Record runs the battery queries against the
// live providers and writes the snapshot. It is the ONLY test that touches
// the network; the certify test below certifies whatever snapshot is
// committed. Opt-in via IMAGESEARCH_LIVE_E2E=1 (+ PEXELS_API_KEY).
func TestLiveImageSearchRelevance_Record(t *testing.T) {
	if os.Getenv(liveE2EEnv) != "1" {
		t.Skipf("set %s=1 (and %s) to run the live relevance record", liveE2EEnv, envPexelsAPIKey)
	}
	searchers := liveSearchers(t)
	limit := liveQueryLimit()
	filter := caseFilter()
	resolver := NewResolver(localnlp.NewExtractor())

	snap := liveSnapshot{
		Schema:     liveSnapshotSchema,
		RecordedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, s := range searchers {
		snap.Providers = append(snap.Providers, s.Name())
	}

	for _, gc := range goldenCases() {
		if !gc.wantRequired {
			// Abstract sentences must NOT be searched — the battery already
			// certifies the no-image decision; recording a live query here
			// would violate T24/T25/T26 by construction.
			continue
		}
		if !inFilter(filter, gc.id) {
			continue
		}
		// Use the resolver's ACTUAL ordered queries (the certified surface),
		// not the battery's expected list, so drift is caught live.
		dec := resolver.Resolve(context.Background(), Request{Text: gc.text, Language: "en", PriorPersons: gc.prior})
		if !dec.Required || len(dec.Queries) == 0 {
			t.Fatalf("[%s] resolver did not produce queries for a required case: %+v", gc.id, dec)
		}
		res := liveCaseResults{ID: gc.id, Text: gc.text, Queries: dec.Queries}
		for _, q := range dec.Queries {
			for _, s := range searchers {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				cands, err := s.Search(ctx, q, limit)
				cancel()
				if err != nil {
					// Empty-result is a valid outcome (provider had nothing
					// for the term); transport errors are recorded as empty
					// too — the snapshot documents what was actually
					// retrievable, and the certify step only scores hits.
					t.Logf("[%s] %s %q: %v", gc.id, s.Name(), q, err)
					continue
				}
				res.Results = append(res.Results, liveQueryResults{Provider: s.Name(), Query: q, Candidates: cands})
			}
		}
		snap.Cases = append(snap.Cases, res)
	}

	raw, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(defaultSnapshotPath, raw, 0o644); err != nil {
		t.Fatalf("write snapshot %s: %v", defaultSnapshotPath, err)
	}
	t.Logf("wrote %s (%d cases, providers %v)", defaultSnapshotPath, len(snap.Cases), snap.Providers)

	// Deterministic identity floors on the fresh run — no human labels yet.
	casesByID := make(map[string]goldenCase, len(goldenCases()))
	for _, gc := range goldenCases() {
		casesByID[gc.id] = gc
	}
	cert := certifyLiveSnapshot(snap, nil, casesByID)
	t.Log(cert.report())
	cert.assertFloors(t)
}

// ── Certify step (offline, certifies the committed snapshot) ──────────

func loadLiveSnapshot(t *testing.T) (liveSnapshot, bool) {
	t.Helper()
	raw, err := os.ReadFile(defaultSnapshotPath)
	if err != nil {
		return liveSnapshot{}, false
	}
	var snap liveSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode %s: %v", defaultSnapshotPath, err)
	}
	return snap, true
}

func loadLiveLabels(t *testing.T) *liveLabels {
	t.Helper()
	raw, err := os.ReadFile(defaultLabelsPath)
	if err != nil {
		return nil
	}
	var labels liveLabels
	if err := json.Unmarshal(raw, &labels); err != nil {
		t.Fatalf("decode %s: %v", defaultLabelsPath, err)
	}
	return &labels
}

// TestImageSearchLiveRelevance_Certify certifies the committed live snapshot.
// It never touches the network: with no snapshot committed it skips; with a
// snapshot it always enforces the deterministic identity floors (wrong
// identity = 0, negated = 0) and, when human labels are committed, the
// relevance@1/3/5 floors (>= 0.90 / >= 0.80).
func TestImageSearchLiveRelevance_Certify(t *testing.T) {
	snap, ok := loadLiveSnapshot(t)
	if !ok {
		t.Skipf("no committed live snapshot at %s — run the record step first", defaultSnapshotPath)
	}
	labels := loadLiveLabels(t)
	casesByID := make(map[string]goldenCase, len(goldenCases()))
	for _, gc := range goldenCases() {
		casesByID[gc.id] = gc
	}
	cert := certifyLiveSnapshot(snap, labels, casesByID)
	t.Log(cert.report())
	cert.assertFloors(t)
}

// ── Metrics engine unit test (no live providers) ──────────────────────

func TestLiveRelevanceMetricsEngine(t *testing.T) {
	casesByID := make(map[string]goldenCase, len(goldenCases()))
	for _, gc := range goldenCases() {
		casesByID[gc.id] = gc
	}

	// T27 is the negation golden pair: the snapshot has a Mike Tyson hit at
	// RANK 0 — the deterministic oracle must flag it as wrong-identity AND
	// negated AND wrongSelected, so the case fails certification.
	t27 := casesByID["T27"]
	t27Snap := liveCaseResults{
		ID: "T27", Text: t27.text, Queries: []string{"Tyson Fury boxer"},
		Results: []liveQueryResults{{
			Provider: "pexels_images", Query: "Tyson Fury boxer",
			Candidates: []liveCandidate{
				{ID: "mike-tyson-2", Title: "Pexels: Mike Tyson boxer by Y", PageURL: "https://www.pexels.com/photo/mike-tyson-2/"},
				{ID: "tyson-fury-1", Title: "Pexels: Tyson Fury boxer by X", PageURL: "https://www.pexels.com/photo/tyson-fury-boxing-1/"},
			},
		}},
	}
	m := certifyLiveCase(t27, t27Snap, nil)
	if m.wrongIdentity != 1 || !m.wrongSelected {
		t.Fatalf("T27 deterministic wrong-identity = %d (selected=%v), want 1/true", m.wrongIdentity, m.wrongSelected)
	}
	if m.negatedSeen != 1 {
		t.Fatalf("T27 negated detection = %d, want 1", m.negatedSeen)
	}
	if m.pass() {
		t.Fatal("T27 with a Mike Tyson candidate at rank 0 must not pass")
	}

	// Same case, wrong hit at rank 1: wrongSelected stays false (the top-1
	// is the correct identity) but negatedSeen still trips the floor.
	deep := liveCaseResults{
		ID: "T27", Text: t27.text, Queries: []string{"Tyson Fury boxer"},
		Results: []liveQueryResults{{
			Provider: "pexels_images", Query: "Tyson Fury boxer",
			Candidates: []liveCandidate{
				{ID: "tyson-fury-1", Title: "Pexels: Tyson Fury boxer by X", PageURL: "https://www.pexels.com/photo/tyson-fury-boxing-1/"},
				{ID: "mike-tyson-2", Title: "Pexels: Mike Tyson boxer by Y", PageURL: "https://www.pexels.com/photo/mike-tyson-2/"},
			},
		}},
	}
	m = certifyLiveCase(t27, deep, nil)
	if m.wrongSelected || m.wrongIdentity != 1 || m.negatedSeen != 1 {
		t.Fatalf("T27 deep: wrongSelected=%v wrongIdentity=%d negatedSeen=%d, want false/1/1", m.wrongSelected, m.wrongIdentity, m.negatedSeen)
	}
	if m.pass() {
		t.Fatal("T27 with a negated hit in the top-5 must not pass even when the top-1 is correct")
	}

	// T18 is the identity pair: a Michael B. Jordan actor hit is a
	// wrong-identity for a "Michael Jordan basketball" query; a basketball
	// hit is not. relevance@k requires labels.
	t18 := casesByID["T18"]
	t18Snap := liveCaseResults{
		ID: "T18", Text: t18.text, Queries: []string{"Michael Jordan basketball"},
		Results: []liveQueryResults{{
			Provider: "pexels_images", Query: "Michael Jordan basketball",
			Candidates: []liveCandidate{
				{ID: "mbj-1", Title: "Pexels: Michael B Jordan actor by Z", PageURL: "https://www.pexels.com/photo/michael-b-jordan-actor-1/"},
				{ID: "mj-2", Title: "Pexels: Michael Jordan basketball by W", PageURL: "https://www.pexels.com/photo/michael-jordan-basketball-2/"},
			},
		}},
	}
	m = certifyLiveCase(t18, t18Snap, nil)
	if m.wrongIdentity != 1 || !m.wrongSelected {
		t.Fatalf("T18 deterministic wrong-identity = %d (selected=%v), want 1/true", m.wrongIdentity, m.wrongSelected)
	}

	// The provider returned the wrong identity FIRST (rank 0): even when
	// labeled, the case must FAIL because the selection would pick the actor
	// photo over the basketball one.
	m = certifyLiveCase(t18, t18Snap, map[string]liveCandidateLabel{
		"mbj-1": {Relevant: false, WrongIdentity: true},
		"mj-2":  {Relevant: true, WrongIdentity: false},
	})
	if !m.wrongSelected || m.wrongIdentity != 1 {
		t.Fatalf("T18 wrong identity at rank 0: wrongSelected=%v wrongIdentity=%d, want true/1", m.wrongSelected, m.wrongIdentity)
	}
	if m.pass() {
		t.Fatal("T18 with a wrong identity at rank 0 must fail certification")
	}

	// When the relevant identity leads, the labeled case passes and
	// relevance@1/3/5 are exact.
	ordered := liveCaseResults{
		ID: "T18", Text: t18.text, Queries: []string{"Michael Jordan basketball"},
		Results: []liveQueryResults{{
			Provider: "pexels_images", Query: "Michael Jordan basketball",
			Candidates: []liveCandidate{
				{ID: "mj-2", Title: "Pexels: Michael Jordan basketball by W", PageURL: "https://www.pexels.com/photo/michael-jordan-basketball-2/"},
				{ID: "mbj-1", Title: "Pexels: Michael B Jordan actor by Z", PageURL: "https://www.pexels.com/photo/michael-b-jordan-actor-1/"},
			},
		}},
	}
	m = certifyLiveCase(t18, ordered, map[string]liveCandidateLabel{
		"mj-2":  {Relevant: true, WrongIdentity: false},
		"mbj-1": {Relevant: false, WrongIdentity: true},
	})
	if m.wrongSelected || m.wrongIdentity != 1 || !m.relevantTop1 {
		t.Fatalf("T18 labeled ordered: wrongSelected=%v wrongIdentity=%d relevantTop1=%v, want false/1/true", m.wrongSelected, m.wrongIdentity, m.relevantTop1)
	}
	if m.relevantTop3 != 1 || m.relevantTop5 != 1 {
		t.Fatalf("T18 labeled ordered: relevantTop3=%d relevantTop5=%d, want 1/1", m.relevantTop3, m.relevantTop5)
	}
	if !m.pass() {
		t.Fatal("T18 with the relevant identity leading must pass")
	}

	// Aggregate: one clean case (T18 labeled) + one poisoned case (T27)
	// → wrong-person selection = 1, negated = 1, relevance floors fail.
	clean := liveCaseResults{
		ID: "T01", Text: "Floyd Mayweather became one of the most recognizable boxers in the world.",
		Queries: []string{"Floyd Mayweather"},
		Results: []liveQueryResults{{
			Provider: "pexels_images", Query: "Floyd Mayweather",
			Candidates: []liveCandidate{
				{ID: "fm-1", Title: "Pexels: Floyd Mayweather by P", PageURL: "https://www.pexels.com/photo/floyd-mayweather-1/"},
				{ID: "fm-2", Title: "Pexels: Floyd Mayweather by Q", PageURL: "https://www.pexels.com/photo/floyd-mayweather-2/"},
			},
		}},
	}
	snap := liveSnapshot{
		Schema: liveSnapshotSchema, RecordedAt: "test",
		Cases: []liveCaseResults{clean, ordered, deep},
	}
	fullLabels := &liveLabels{Schema: liveLabelsSchema, Labels: map[string]map[string]liveCandidateLabel{
		"T01": {"fm-1": {Relevant: true}, "fm-2": {Relevant: true}},
		"T18": {"mj-2": {Relevant: true}, "mbj-1": {Relevant: false, WrongIdentity: true}},
		"T27": {"tyson-fury-1": {Relevant: true}, "mike-tyson-2": {Relevant: false, WrongIdentity: true}},
	}}
	cert := certifyLiveSnapshot(snap, fullLabels, casesByID)
	if cert.wrongSelectedCases != 0 {
		t.Fatalf("aggregate wrong-person selection = %d, want 0 (T27's wrong hit sits at rank 1)", cert.wrongSelectedCases)
	}
	if cert.negatedTotal != 1 {
		t.Fatalf("aggregate negated selections = %d, want 1", cert.negatedTotal)
	}
	// relevance@1: all three cases have a labeled-relevant top-1 (T18's
	// basketball leads; T27's Tyson Fury leads).
	if cert.labeledCases != 3 || cert.rel1 != 3 {
		t.Fatalf("labeled=%d rel1=%d, want 3/3", cert.labeledCases, cert.rel1)
	}
	if cert.rel3 != 3 || cert.rel5 != 3 {
		t.Fatalf("rel3=%d rel5=%d, want 3/3", cert.rel3, cert.rel5)
	}
	// T27 still has a negated hit in the top-5, so the aggregate must fail
	// the identity floors even though every relevance@k is perfect — the
	// point of the negated floor.
	if cert.negatedTotal == 0 {
		t.Fatal("aggregate with a poisoned T27 must fail the negated floor")
	}
	// And a rank-0 wrong identity (t27Snap) must trip wrong-person selection.
	rank0Cert := certifyLiveSnapshot(liveSnapshot{Schema: liveSnapshotSchema, Cases: []liveCaseResults{clean, ordered, t27Snap}}, fullLabels, casesByID)
	if rank0Cert.wrongSelectedCases != 1 {
		t.Fatalf("aggregate with T27 wrong hit at rank 0: wrong-person selection = %d, want 1", rank0Cert.wrongSelectedCases)
	}

	// An abstract case recorded with queries is a snapshot-integrity
	// violation (T24/T25/T26 must never be searched).
	t24 := casesByID["T24"]
	badSnap := liveSnapshot{Schema: liveSnapshotSchema, Cases: []liveCaseResults{{
		ID: "T24", Text: t24.text, Queries: []string{"success patience discipline"},
		Results: []liveQueryResults{{
			Provider: "pexels_images", Query: "success patience discipline",
			Candidates: []liveCandidate{{ID: "x-1", Title: "Pexels: success by A", PageURL: "https://www.pexels.com/photo/success-1/"}},
		}},
	}}}
	badCert := certifyLiveSnapshot(badSnap, nil, casesByID)
	if badCert.wrongSelectedCases != 1 || badCert.rows[0].pass() {
		t.Fatal("abstract case recorded with queries must fail certification (no-image decision violated)")
	}

	// A clean aggregate (no poisoned case) passes every floor.
	cleanSnap := liveSnapshot{Schema: liveSnapshotSchema, Cases: []liveCaseResults{clean, ordered}}
	cleanLabels := &liveLabels{Schema: liveLabelsSchema, Labels: map[string]map[string]liveCandidateLabel{
		"T01": {"fm-1": {Relevant: true}, "fm-2": {Relevant: true}},
		"T18": {"mj-2": {Relevant: true}, "mbj-1": {Relevant: false, WrongIdentity: true}},
	}}
	cleanCert := certifyLiveSnapshot(cleanSnap, cleanLabels, casesByID)
	if cleanCert.wrongSelectedCases != 0 || cleanCert.negatedTotal != 0 {
		t.Fatalf("clean aggregate: wrongSelected=%d negated=%d, want 0/0", cleanCert.wrongSelectedCases, cleanCert.negatedTotal)
	}
	if cleanCert.labeledCases != 2 || cleanCert.rel1 != 2 || cleanCert.rel5 != 2 {
		t.Fatalf("clean aggregate: labeled=%d rel1=%d rel5=%d, want 2/2/2", cleanCert.labeledCases, cleanCert.rel1, cleanCert.rel5)
	}
	_ = cleanCert.report()
}
