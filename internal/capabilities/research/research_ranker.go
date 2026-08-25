// Package app — research_ranker.go.
//
// The research ranker orders researched candidates by an explicit ranking
// metric. For financial metrics (net worth, career/annual earnings, purse)
// the order is a deterministic numeric sort on verified claim values — the
// LLM is used only for best-effort rationale enrichment, so duplicate ranks
// and null scores are structurally impossible. For non-financial metrics
// (sports achievement, generic) the LLM orders semantically under a strict
// JSON contract with a deterministic fallback. Every degradation is
// observable: raw model output is logged at debug, parse/validation failures
// at warn, and any fallback records metric, strategy and reason.
package research

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
	"go.uber.org/zap"
)

type ollamaResearchRanker struct {
	client ResearchGenerationClient
	model  string
	logger *zap.Logger
}

// ResearchGenerationClient is the narrow model boundary required by the
// research capability. Provider clients satisfy it without importing app.
type ResearchGenerationClient interface {
	SimpleGenerate(context.Context, string, string, time.Duration, map[string]any) (string, error)
}

// NewResearchRanker constructs the canonical Ollama-backed research ranker.
func NewResearchRanker(client ResearchGenerationClient, model string, logger *zap.Logger) scriptports.ResearchRanker {
	return &ollamaResearchRanker{client: client, model: model, logger: logger}
}

type rankingEvidence struct {
	CandidateID string   `json:"candidate_id"`
	Label       string   `json:"label"`
	Sources     []string `json:"sources"`
	Claims      []string `json:"verified_claims"`
	ClaimIDs    []string `json:"claim_ids"`
}

const (
	rankerMaxClaimsPerCandidate = 4
	rankerClaimChars            = 300
	rankerMaxSources            = 6
)

// rankerGenOptions mirrors the writer-path generation defaults (see
// internal/platform/ollama/generate.go): without an explicit
// num_ctx Ollama falls back to the model default (4096), which the
// multi-candidate ranker prompt overflows and the model answers with a
// truncated `{"` instead of the ranking JSON.
func rankerGenOptions() map[string]any {
	return map[string]any{
		"format":      "json",
		"num_ctx":     types.DefaultNumCtx,
		"num_predict": 8192,
		"temperature": 0.2,
	}
}

type rankerResponse struct {
	RankingMetric string               `json:"ranking_metric"`
	Items         []rankerResponseItem `json:"items"`
}

type rankerResponseItem struct {
	CandidateID      string   `json:"candidate_id"`
	Rank             int      `json:"rank"`
	Score            float64  `json:"score"`
	EvidenceClaimIDs []string `json:"evidence_claim_ids"`
	Rationale        string   `json:"rationale"`
}

func (r *ollamaResearchRanker) Rank(ctx context.Context, topic string, metric scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
	if r == nil || r.client == nil {
		return scriptports.ResearchRankingResult{}, fmt.Errorf("research ranker: ollama client is not configured")
	}
	if metric.IsFinancial() {
		return r.rankFinancialDeterministic(ctx, topic, metric, inputs)
	}
	return r.rankSemantic(ctx, topic, metric, inputs)
}

// rankFinancialDeterministic is the PRIMARY ordering path for financial
// metrics. It sorts candidates by the median verified claim amount (via
// metricAwareFallback) and never asks the LLM to order — duplicate ranks and
// null scores are impossible by construction. The LLM is called only as a
// best-effort rationale enricher; if it fails, the deterministic rationale
// stands and the ranking is unchanged.
func (r *ollamaResearchRanker) rankFinancialDeterministic(ctx context.Context, topic string, metric scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
	ranking, info := metricAwareFallback(inputs, metric)
	info.RequestedMetric = metric.String()
	info.FallbackUsed = false

	r.logInfo("ranker.deterministic",
		zap.String("ranking_metric", metric.String()),
		zap.String("strategy", info.Strategy),
		zap.Int("candidates_with_evidence", info.CandidatesWithEvidence),
		zap.Bool("uncertain", info.Uncertain),
	)

	r.enrichRationales(ctx, topic, metric, inputs, ranking)

	return scriptports.ResearchRankingResult{Ranking: ranking, Info: info}, nil
}

// rankSemantic orders candidates with the LLM for non-financial metrics
// (sports achievement, generic) where there is no deterministic numeric value
// to sort on. It keeps the strict contract + parse retry + deterministic
// fallback for invalid model output.
func (r *ollamaResearchRanker) rankSemantic(ctx context.Context, topic string, metric scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput) (scriptports.ResearchRankingResult, error) {
	evidence := buildRankingEvidence(inputs)
	payload, err := json.Marshal(evidence)
	if err != nil {
		return scriptports.ResearchRankingResult{}, fmt.Errorf("research ranker: input encode: %w", err)
	}
	r.logInfo("ranker.start", zap.String("ranking_metric", metric.String()), zap.Int("candidate_count", len(inputs)), zap.String("model", r.model))

	prompt := fmt.Sprintf(rankSemanticPromptTemplate, topic, metric, metric.Description(), metric, len(inputs), payload)
	output, err := r.client.SimpleGenerate(ctx, r.model, prompt, 120*time.Second, rankerGenOptions())
	if err != nil {
		return scriptports.ResearchRankingResult{}, fmt.Errorf("research ranker: generation: %w", err)
	}
	r.logDebug("ranker.model_response", zap.String("ranking_metric", metric.String()), zap.Int("attempt", 1), zap.Int("raw_output_chars", len(output)), zap.String("raw_output", output))

	parsed, echoedMetric, parseErr := parseResearchRankingOutput(output)
	if parseErr != nil {
		r.logWarn("ranker.parse_failed", zap.String("ranking_metric", metric.String()), zap.Int("candidate_count", len(inputs)), zap.Int("attempt", 1), zap.String("parse_error", parseErr.Error()), zap.Int("raw_output_chars", len(output)))
		retryPrompt := fmt.Sprintf(rankSemanticRetryPromptTemplate, metric, metric, len(inputs), metric, payload)
		retryOutput, retryErr := r.client.SimpleGenerate(ctx, r.model, retryPrompt, 120*time.Second, rankerGenOptions())
		if retryErr != nil {
			r.logWarn("ranker.retry_error", zap.String("ranking_metric", metric.String()), zap.String("retry_error", retryErr.Error()))
			return r.fallback(inputs, metric, rankFallbackParse, "retry generation failed"), nil
		}
		r.logDebug("ranker.model_response", zap.String("ranking_metric", metric.String()), zap.Int("attempt", 2), zap.Int("raw_output_chars", len(retryOutput)), zap.String("raw_output", retryOutput))
		parsed, echoedMetric, parseErr = parseResearchRankingOutput(retryOutput)
		if parseErr != nil {
			r.logWarn("ranker.parse_failed", zap.String("ranking_metric", metric.String()), zap.Int("candidate_count", len(inputs)), zap.Int("attempt", 2), zap.String("parse_error", parseErr.Error()), zap.Int("raw_output_chars", len(retryOutput)))
			return r.fallback(inputs, metric, rankFallbackParse, ""), nil
		}
	}
	if validationErr := validateResearchRankingOutput(metric, echoedMetric, inputs, parsed); validationErr != nil {
		r.logWarn("ranker.validation_failed", zap.String("ranking_metric", metric.String()), zap.Int("candidate_count", len(inputs)), zap.String("validation_error", validationErr.Error()))
		return r.fallback(inputs, metric, rankFallbackValidation, ""), nil
	}
	result := scriptports.ResearchRankingResult{
		Ranking: parsed,
		Info: scriptpkg.ResearchRankingInfo{
			RequestedMetric:        metric.String(),
			ResolvedMetric:         metric.String(),
			Strategy:               "llm_verified_evidence",
			FallbackUsed:           false,
			CandidatesWithEvidence: countScoredCandidates(parsed),
		},
	}
	r.logInfo("ranker.completed", zap.String("ranking_metric", metric.String()), zap.String("strategy", result.Info.Strategy), zap.Bool("fallback_used", false), zap.Int("candidate_count", len(inputs)))
	return result, nil
}

func (r *ollamaResearchRanker) fallback(inputs []scriptports.ResearchCandidateRankingInput, metric scriptpkg.RankingMetric, reason string, detail string) scriptports.ResearchRankingResult {
	ranking, info := metricAwareFallback(inputs, metric)
	info.RequestedMetric = metric.String()
	info.FallbackUsed = true
	info.FallbackReason = reason
	if detail != "" {
		info.FallbackReason = reason + ": " + detail
	}
	r.logWarn("ranker.fallback", zap.String("ranking_metric", metric.String()), zap.String("reason", reason), zap.String("strategy", info.Strategy), zap.Int("candidates_with_evidence", info.CandidatesWithEvidence), zap.Bool("uncertain", info.Uncertain))
	return scriptports.ResearchRankingResult{Ranking: ranking, Info: info}
}

func (r *ollamaResearchRanker) logInfo(message string, fields ...zap.Field) {
	if r.logger != nil {
		r.logger.Info(message, fields...)
	}
}

func (r *ollamaResearchRanker) logDebug(message string, fields ...zap.Field) {
	if r.logger != nil {
		r.logger.Debug(message, fields...)
	}
}

func (r *ollamaResearchRanker) logWarn(message string, fields ...zap.Field) {
	if r.logger != nil {
		r.logger.Warn(message, fields...)
	}
}

// buildRankingEvidence projects candidates into a compact model input.
// Claims carry synthetic ids (c1..cn per candidate) so the model can attach
// evidence_claim_ids and the validator can enforce them. The input is
// deliberately bounded (a handful of claims per candidate, trimmed) so the
// prompt stays inside the model's context window; oversized prompts caused
// the model to emit truncated JSON like `{"`.
func buildRankingEvidence(inputs []scriptports.ResearchCandidateRankingInput) []rankingEvidence {
	evidence := make([]rankingEvidence, 0, len(inputs))
	for _, input := range inputs {
		item := rankingEvidence{CandidateID: input.CandidateID, Label: input.Label}
		for _, source := range input.Sources {
			if len(item.Sources) >= rankerMaxSources {
				break
			}
			item.Sources = append(item.Sources, strings.TrimSpace(source.Title+" — "+source.URL))
		}
		for index, claim := range input.Claims {
			if !claim.Verified || len(item.Claims) >= rankerMaxClaimsPerCandidate {
				continue
			}
			item.Claims = append(item.Claims, trimRankerText(claim.Text, rankerClaimChars))
			item.ClaimIDs = append(item.ClaimIDs, fmt.Sprintf("c%d", index+1))
		}
		evidence = append(evidence, item)
	}
	return evidence
}

// parseResearchRankingOutput extracts the JSON object from a model response
// that may contain markdown fences or surrounding prose, and decodes the
// strict contract. Legacy bare-array responses fail parsing so they are
// retried and, if still malformed, fall back with a logged reason.
func parseResearchRankingOutput(output string) ([]scriptports.ResearchCandidateRanking, string, error) {
	output = strings.TrimSpace(output)
	start := strings.Index(output, "{")
	if start < 0 {
		return nil, "", fmt.Errorf("no JSON object in model output")
	}
	end := strings.LastIndex(output, "}")
	if end <= start {
		return nil, "", fmt.Errorf("unterminated JSON object in model output")
	}
	var response rankerResponse
	if err := json.Unmarshal([]byte(output[start:end+1]), &response); err != nil {
		return nil, "", fmt.Errorf("model output decode: %w", err)
	}
	if strings.TrimSpace(response.RankingMetric) == "" {
		return nil, "", fmt.Errorf("model output missing ranking_metric")
	}
	ranking := make([]scriptports.ResearchCandidateRanking, 0, len(response.Items))
	for _, item := range response.Items {
		ranking = append(ranking, scriptports.ResearchCandidateRanking{
			CandidateID: strings.TrimSpace(item.CandidateID),
			Rank:        item.Rank,
			Score:       item.Score,
			Rationale:   strings.TrimSpace(item.Rationale),
		})
	}
	return ranking, strings.TrimSpace(response.RankingMetric), nil
}

// validateResearchRankingOutput enforces the strict ranking contract: the
// metric must match the requested one, every input candidate must appear
// exactly once, and ranks must be exactly 1..N. Any violation means the
// model output cannot be trusted and the caller falls back.
func validateResearchRankingOutput(metric scriptpkg.RankingMetric, echoedMetric string, inputs []scriptports.ResearchCandidateRankingInput, ranking []scriptports.ResearchCandidateRanking) error {
	if normalized := scriptpkg.NormalizeRankingMetric(echoedMetric); normalized != metric {
		return fmt.Errorf("ranking metric %q does not match requested metric %q", echoedMetric, metric)
	}
	expected := make(map[string]struct{}, len(inputs))
	claimIDs := make(map[string]map[string]struct{}, len(inputs))
	for _, input := range inputs {
		expected[input.CandidateID] = struct{}{}
		ids := make(map[string]struct{})
		for index, claim := range input.Claims {
			if claim.Verified {
				ids[fmt.Sprintf("c%d", index+1)] = struct{}{}
			}
		}
		claimIDs[input.CandidateID] = ids
	}
	if len(ranking) != len(inputs) {
		return fmt.Errorf("expected %d ranked candidates, got %d", len(inputs), len(ranking))
	}
	seenCandidates := make(map[string]struct{}, len(ranking))
	seenRanks := make(map[int]struct{}, len(ranking))
	for _, ranked := range ranking {
		if ranked.CandidateID == "" {
			return fmt.Errorf("ranking contains a candidate with empty candidate_id")
		}
		if _, ok := expected[ranked.CandidateID]; !ok {
			return fmt.Errorf("ranking returned unknown candidate %q", ranked.CandidateID)
		}
		if _, ok := seenCandidates[ranked.CandidateID]; ok {
			return fmt.Errorf("ranking duplicated candidate %q", ranked.CandidateID)
		}
		seenCandidates[ranked.CandidateID] = struct{}{}
		if ranked.Rank < 1 || ranked.Rank > len(inputs) {
			return fmt.Errorf("candidate %q returned invalid rank %d", ranked.CandidateID, ranked.Rank)
		}
		if _, ok := seenRanks[ranked.Rank]; ok {
			return fmt.Errorf("duplicate rank %d", ranked.Rank)
		}
		seenRanks[ranked.Rank] = struct{}{}
	}
	for rank := 1; rank <= len(inputs); rank++ {
		if _, ok := seenRanks[rank]; !ok {
			return fmt.Errorf("ranking missing rank %d", rank)
		}
	}
	// For financial metrics the model must attach a numeric evidence value
	// to most candidates. A ranking where the majority of scores are null
	// (e.g. `"score": null`) does not order by the requested metric and is
	// less reliable than the deterministic fallback, so it fails validation.
	if metric.IsFinancial() {
		scored := 0
		for _, ranked := range ranking {
			if ranked.Score > 0 {
				scored++
			}
		}
		required := (len(inputs) + 1) / 2
		if scored < required {
			return fmt.Errorf("financial ranking has numeric scores for only %d/%d candidates (need %d)", scored, len(inputs), required)
		}
	}
	return nil
}

func countScoredCandidates(ranking []scriptports.ResearchCandidateRanking) int {
	count := 0
	for _, ranked := range ranking {
		if ranked.Score > 0 {
			count++
		}
	}
	return count
}

func trimRankerText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}

// ── Rationale enrichment (financial metrics only) ───────────────────
//
// The deterministic numeric sort owns the ORDER; the LLM is invoked only to
// rewrite each candidate's rationale in natural language (context + explicit
// uncertainty). The enrichment is strictly best-effort and order-safe: it
// matches rationales by candidate_id and only replaces the Rationale string,
// never Rank or Score, so a hallucinating model cannot change the ordering.

type rankerRationaleResponse struct {
	Rationales []rankerRationaleItem `json:"rationales"`
}

type rankerRationaleItem struct {
	CandidateID string `json:"candidate_id"`
	Rationale   string `json:"rationale"`
}

func (r *ollamaResearchRanker) enrichRationales(ctx context.Context, topic string, metric scriptpkg.RankingMetric, inputs []scriptports.ResearchCandidateRankingInput, ranking []scriptports.ResearchCandidateRanking) {
	payload := buildRationalePayload(ranking, inputs, metric)
	if payload == "" {
		return
	}
	prompt := fmt.Sprintf(rankRationalePromptTemplate, topic, metric, metric.Description(), payload)
	output, err := r.client.SimpleGenerate(ctx, r.model, prompt, 60*time.Second, rankerGenOptions())
	if err != nil {
		r.logWarn("ranker.rationale_skipped", zap.String("ranking_metric", metric.String()), zap.String("reason", err.Error()))
		return
	}
	r.logDebug("ranker.rationale_model_response", zap.String("ranking_metric", metric.String()), zap.Int("raw_output_chars", len(output)), zap.String("raw_output", output))
	rationales, err := parseRationaleOutput(output)
	if err != nil {
		r.logWarn("ranker.rationale_skipped", zap.String("ranking_metric", metric.String()), zap.String("reason", "parse: "+err.Error()))
		return
	}
	byID := make(map[string]string, len(rationales))
	for _, item := range rationales {
		if candidate := strings.TrimSpace(item.CandidateID); candidate != "" && strings.TrimSpace(item.Rationale) != "" {
			byID[candidate] = strings.TrimSpace(item.Rationale)
		}
	}
	applied := 0
	for i := range ranking {
		if rationale, ok := byID[ranking[i].CandidateID]; ok {
			ranking[i].Rationale = rationale
			applied++
		}
	}
	if applied > 0 {
		r.logInfo("ranker.rationale_enriched", zap.String("ranking_metric", metric.String()), zap.Int("applied", applied), zap.Int("candidates", len(ranking)))
	} else {
		r.logWarn("ranker.rationale_skipped", zap.String("ranking_metric", metric.String()), zap.String("reason", "no matching candidate_id in rationale output"))
	}
}

// buildRationalePayload renders the FIXED ranking (rank + score + verified
// claims) for the rationale prompt. The order is already decided; the model
// is only asked to explain it.
func buildRationalePayload(ranking []scriptports.ResearchCandidateRanking, inputs []scriptports.ResearchCandidateRankingInput, metric scriptpkg.RankingMetric) string {
	claimsByID := make(map[string][]string, len(inputs))
	for _, input := range inputs {
		claims := make([]string, 0, rankerMaxClaimsPerCandidate)
		for _, claim := range input.Claims {
			if !claim.Verified || len(claims) >= rankerMaxClaimsPerCandidate {
				continue
			}
			claims = append(claims, trimRankerText(claim.Text, rankerClaimChars))
		}
		claimsByID[input.CandidateID] = claims
	}
	var b strings.Builder
	for _, ranked := range ranking {
		if ranked.Score > 0 {
			fmt.Fprintf(&b, "%d. %s — verified amount $%s\n", ranked.Rank, ranked.CandidateID, formatUSD(ranked.Score))
		} else {
			fmt.Fprintf(&b, "%d. %s — no verified %s amount in claims\n", ranked.Rank, ranked.CandidateID, metric.String())
		}
		for _, claim := range claimsByID[ranked.CandidateID] {
			fmt.Fprintf(&b, "   - %s\n", claim)
		}
	}
	return b.String()
}

// parseRationaleOutput extracts the rationale JSON object from a model
// response (markdown fences / prose tolerant, like parseResearchRankingOutput).
func parseRationaleOutput(output string) ([]rankerRationaleItem, error) {
	output = strings.TrimSpace(output)
	start := strings.Index(output, "{")
	if start < 0 {
		return nil, fmt.Errorf("no JSON object in rationale output")
	}
	end := strings.LastIndex(output, "}")
	if end <= start {
		return nil, fmt.Errorf("unterminated JSON object in rationale output")
	}
	var response rankerRationaleResponse
	if err := json.Unmarshal([]byte(output[start:end+1]), &response); err != nil {
		return nil, fmt.Errorf("rationale output decode: %w", err)
	}
	return response.Rationales, nil
}

// rankSemanticPromptTemplate orders candidates for a semantic metric (sports
// achievement, influence, controversy, generic) where no numeric value exists.
// It forces a 0-100 qualitative score so the ranking is not reduced to
// arbitrary integer ranks, and it forbids metric substitution (e.g. ranking
// "most controversial" by net worth or titles). Financial metrics never reach
// this prompt: they are ordered deterministically by verified claim values.
const rankSemanticPromptTemplate = `You are the canonical editorial ranking resolver. Rank the researched candidates for %q by the requested criterion ONLY: %s.

Criterion definition: %s

There is no single numeric value to sort on; weigh the qualitative evidence. Assign each candidate a score from 0 to 100 on this criterion, then rank strictly by that score (highest first). Ties are allowed only when the evidence is genuinely equivalent, and the rationale must explain why.

Use ONLY documented evidence from the provided verified claims. Do not invent facts, and do not rank by a different criterion — for example, do not rank "most controversial" by net worth, titles, or championships.

Return JSON only, with this exact schema:
{"ranking_metric":"%s","items":[{"candidate_id":"...","rank":1,"score":85,"evidence_claim_ids":["c1",...],"rationale":"one sentence citing the evidence"}]}

Contract: include every candidate exactly once; use every rank from 1 through %d; candidate_id must match the provided candidate_id exactly; evidence_claim_ids must reference the provided claim_ids of that candidate; score must be an integer from 0 to 100.

EVIDENCE:
%s`

const rankSemanticRetryPromptTemplate = `Return ONLY a valid JSON object, no prose, no code fences:
{"ranking_metric":"%s","items":[{"candidate_id":"...","rank":1,"score":0,"evidence_claim_ids":[],"rationale":"..."}]}

Assign each candidate an integer score 0-100 on the criterion "%s" and rank highest-first. Do not substitute a different criterion.

Include every candidate exactly once with ranks 1..%d. The metric is %s and must be echoed verbatim.

Candidates and evidence:
%s`

const rankRationalePromptTemplate = `You are the editorial rationale writer. The candidate ranking for %q is ALREADY FIXED by verified numeric evidence for the metric "%s" (%s). Do NOT reorder, add, or remove candidates.

For each candidate below, write a one-sentence rationale that explains the evidence behind their rank and explicitly flags uncertainty when the evidence is thin or single-sourced.

Return JSON only, with this exact schema:
{"rationales":[{"candidate_id":"...","rationale":"one sentence"}]}

Include every candidate exactly once; candidate_id must match the provided candidate_id exactly.

FIXED RANKING AND EVIDENCE:
%s`
