package research

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	RankingVersion = "boxing-ranking-v1"

	weightWealth       = 0.30
	weightCareer       = 0.20
	weightPaydays      = 0.15
	weightBusinesses   = 0.20
	weightEndorsements = 0.10
	weightLosses       = 0.15
)

var (
	ErrInvalidRankingRequest       = errors.New("research: invalid ranking request")
	ErrInsufficientRankingEvidence = errors.New("research: insufficient ranking evidence")
)

// RankingRequest contains the initial ten candidates and their completed
// research. ReplacementCandidates are researched subjects outside the initial
// list that may replace a candidate whose evidence fails validation.
type RankingRequest struct {
	Topic                 string
	InitialCandidates     []string
	ResearchedPacks       []EvidencePack
	ReplacementCandidates []EvidencePack
}

// RankingResult is the immutable post-research handoff to narrative planning.
// FinalPacks preserves every source-backed pack selected for the ranking.
type RankingResult struct {
	Version      string                 `json:"version"`
	Topic        string                 `json:"topic"`
	Entries      []RankedBoxer          `json:"entries"`
	FinalPacks   []EvidencePack         `json:"final_packs"`
	Replacements []CandidateReplacement `json:"replacements,omitempty"`
	Conflicts    []EstimateConflict     `json:"conflicts,omitempty"`
}

// RankedBoxer records the rank, score components, evidence confidence, and
// prose rationale. Scores are relative to this researched cohort, not claims
// of mathematically certain net worth.
type RankedBoxer struct {
	Rank           int            `json:"rank"`
	Subject        string         `json:"subject"`
	Score          float64        `json:"score"`
	SupportScore   float64        `json:"support_score"`
	Confidence     float64        `json:"confidence"`
	ScoreBreakdown ScoreBreakdown `json:"score_breakdown"`
	ConflictIDs    []string       `json:"conflict_ids,omitempty"`
	Rationale      string         `json:"rationale"`
}

// ScoreBreakdown exposes the explicit relative signals used by the resolver.
// Losses are a penalty; all other components are positive contributions.
type ScoreBreakdown struct {
	Wealth       float64 `json:"wealth"`
	Career       float64 `json:"career"`
	Paydays      float64 `json:"paydays"`
	Businesses   float64 `json:"businesses"`
	Endorsements float64 `json:"endorsements"`
	Losses       float64 `json:"losses"`
}

// CandidateReplacement documents why an initial candidate was removed and
// which better-supported researched candidate took the slot.
type CandidateReplacement struct {
	OriginalSubject    string  `json:"original_subject"`
	ReplacementSubject string  `json:"replacement_subject"`
	Reason             string  `json:"reason"`
	OriginalError      string  `json:"original_error"`
	ReplacementSupport float64 `json:"replacement_support"`
}

// EstimateConflict preserves disagreement between comparable estimates. The
// resolver never averages these values into a fabricated exact figure.
type EstimateConflict struct {
	ID          string    `json:"id"`
	Subject     string    `json:"subject"`
	Category    string    `json:"category"`
	EvidenceIDs []string  `json:"evidence_ids"`
	ValuesUSD   []float64 `json:"values_usd"`
	SpreadRatio float64   `json:"spread_ratio"`
	Description string    `json:"description"`
}

type rankingCandidate struct {
	pack          EvidencePack
	supportScore  float64
	confidence    float64
	breakdown     ScoreBreakdown
	conflicts     []EstimateConflict
	conflictIDs   []string
	rationaleData rationaleData
}

type rationaleData struct {
	wealthValue, careerValue, paydayValue float64
	businessValue, endorsementValue       float64
	lossValue                             float64
	hasWealth, hasCareer, hasPayday       bool
	hasBusiness, hasEndorsement, hasLoss  bool
}

// ResolveRanking validates the initial research, replaces unsupported
// candidates from the replacement pool, detects conflicting estimates, and
// returns a deterministic ranking. It fails closed if ten supported final
// candidates cannot be assembled.
func ResolveRanking(req RankingRequest) (RankingResult, error) {
	if strings.TrimSpace(req.Topic) == "" {
		return RankingResult{}, fmt.Errorf("%w: topic is required", ErrInvalidRankingRequest)
	}
	if len(req.InitialCandidates) != TenBoxerPackCount {
		return RankingResult{}, fmt.Errorf("%w: requires %d initial candidates, got %d", ErrInvalidRankingRequest, TenBoxerPackCount, len(req.InitialCandidates))
	}

	initialNames, err := uniqueNames(req.InitialCandidates)
	if err != nil {
		return RankingResult{}, fmt.Errorf("%w: %v", ErrInvalidRankingRequest, err)
	}
	researched := indexPacks(req.ResearchedPacks)
	replacements := indexPacks(req.ReplacementCandidates)
	// Reserve all initial subjects so a replacement cannot silently reuse
	// another initial candidate that will be processed later in the loop.
	used := make(map[string]struct{}, TenBoxerPackCount)
	for key := range initialNames {
		used[key] = struct{}{}
	}
	selected := make([]EvidencePack, 0, TenBoxerPackCount)
	result := RankingResult{Version: RankingVersion, Topic: req.Topic}

	for _, original := range req.InitialCandidates {
		key := normalizedName(original)
		pack, ok := researched[key]
		if ok {
			if err := pack.Validate(); err == nil {
				selected = append(selected, pack)
				used[normalizedName(pack.Subject)] = struct{}{}
				continue
			} else {
				replacement, replacementKey, replacementSupport, replacementErr := bestReplacement(replacements, used)
				if replacementErr != nil {
					return RankingResult{}, fmt.Errorf("%w: candidate %q: %v", ErrInsufficientRankingEvidence, original, replacementErr)
				}
				selected = append(selected, replacement)
				used[replacementKey] = struct{}{}
				result.Replacements = append(result.Replacements, CandidateReplacement{
					OriginalSubject: original, ReplacementSubject: replacement.Subject,
					Reason:        "initial candidate failed the source-backed evidence contract and was replaced by the strongest available supported candidate",
					OriginalError: err.Error(), ReplacementSupport: replacementSupport,
				})
				continue
			}
		}

		replacement, replacementKey, replacementSupport, replacementErr := bestReplacement(replacements, used)
		if replacementErr != nil {
			return RankingResult{}, fmt.Errorf("%w: candidate %q: %v", ErrInsufficientRankingEvidence, original, replacementErr)
		}
		selected = append(selected, replacement)
		used[replacementKey] = struct{}{}
		result.Replacements = append(result.Replacements, CandidateReplacement{
			OriginalSubject: original, ReplacementSubject: replacement.Subject,
			Reason:        "initial candidate had no completed supported evidence pack and was replaced by the strongest available supported candidate",
			OriginalError: "research pack missing", ReplacementSupport: replacementSupport,
		})
	}

	candidates := make([]rankingCandidate, 0, len(selected))
	for _, pack := range selected {
		candidate, err := evaluateCandidate(pack)
		if err != nil {
			return RankingResult{}, fmt.Errorf("%w: selected candidate %q: %v", ErrInsufficientRankingEvidence, pack.Subject, err)
		}
		candidates = append(candidates, candidate)
	}
	normalizeBreakdowns(candidates)
	for i := range candidates {
		candidates[i].conflictIDs = make([]string, 0, len(candidates[i].conflicts))
		for _, conflict := range candidates[i].conflicts {
			candidates[i].conflictIDs = append(candidates[i].conflictIDs, conflict.ID)
			result.Conflicts = append(result.Conflicts, conflict)
		}
	}
	sort.SliceStable(result.Conflicts, func(i, j int) bool { return result.Conflicts[i].ID < result.Conflicts[j].ID })

	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidateScore(candidates[i])
		right := candidateScore(candidates[j])
		if left != right {
			return left > right
		}
		if candidates[i].confidence != candidates[j].confidence {
			return candidates[i].confidence > candidates[j].confidence
		}
		return normalizedName(candidates[i].pack.Subject) < normalizedName(candidates[j].pack.Subject)
	})

	result.FinalPacks = append([]EvidencePack(nil), selected...)
	// Reorder final packs to rank order so the narrative planner can consume
	// one canonical ordering without maintaining a second subject map.
	result.FinalPacks = result.FinalPacks[:0]
	for i, candidate := range candidates {
		score := candidateScore(candidate)
		result.FinalPacks = append(result.FinalPacks, candidate.pack)
		result.Entries = append(result.Entries, RankedBoxer{
			Rank: i + 1, Subject: candidate.pack.Subject, Score: score,
			SupportScore: candidate.supportScore, Confidence: candidate.confidence,
			ScoreBreakdown: candidate.breakdown, ConflictIDs: candidate.conflictIDs,
			Rationale: buildRationale(candidate, score),
		})
	}
	return result, nil
}

func evaluateCandidate(pack EvidencePack) (rankingCandidate, error) {
	if err := pack.Validate(); err != nil {
		return rankingCandidate{}, err
	}
	candidate := rankingCandidate{pack: pack}
	candidate.supportScore, candidate.confidence = supportMetrics(pack)
	candidate.rationaleData = collectValues(pack)
	candidate.conflicts = detectConflicts(pack)
	return candidate, nil
}

func bestReplacement(packs map[string]EvidencePack, used map[string]struct{}) (EvidencePack, string, float64, error) {
	candidates := make([]rankingCandidate, 0, len(packs))
	for key, pack := range packs {
		if _, exists := used[key]; exists {
			continue
		}
		candidate, err := evaluateCandidate(pack)
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate)
	}
	if len(candidates) == 0 {
		return EvidencePack{}, "", 0, errors.New("no supported replacement candidate available")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].supportScore != candidates[j].supportScore {
			return candidates[i].supportScore > candidates[j].supportScore
		}
		return normalizedName(candidates[i].pack.Subject) < normalizedName(candidates[j].pack.Subject)
	})
	winner := candidates[0]
	return winner.pack, normalizedName(winner.pack.Subject), winner.supportScore, nil
}

func normalizeBreakdowns(candidates []rankingCandidate) {
	max := [6]float64{}
	for _, candidate := range candidates {
		values := [6]float64{
			candidate.rationaleData.wealthValue, candidate.rationaleData.careerValue,
			candidate.rationaleData.paydayValue, candidate.rationaleData.businessValue,
			candidate.rationaleData.endorsementValue, candidate.rationaleData.lossValue,
		}
		for i, value := range values {
			if value > max[i] {
				max[i] = value
			}
		}
	}
	for i := range candidates {
		values := [6]float64{
			candidates[i].rationaleData.wealthValue, candidates[i].rationaleData.careerValue,
			candidates[i].rationaleData.paydayValue, candidates[i].rationaleData.businessValue,
			candidates[i].rationaleData.endorsementValue, candidates[i].rationaleData.lossValue,
		}
		components := [6]*float64{
			&candidates[i].breakdown.Wealth, &candidates[i].breakdown.Career,
			&candidates[i].breakdown.Paydays, &candidates[i].breakdown.Businesses,
			&candidates[i].breakdown.Endorsements, &candidates[i].breakdown.Losses,
		}
		for j, value := range values {
			if max[j] > 0 {
				*components[j] = math.Log1p(value) / math.Log1p(max[j])
			}
		}
	}
}

func candidateScore(candidate rankingCandidate) float64 {
	return weightWealth*candidate.breakdown.Wealth +
		weightCareer*candidate.breakdown.Career +
		weightPaydays*candidate.breakdown.Paydays +
		weightBusinesses*candidate.breakdown.Businesses +
		weightEndorsements*candidate.breakdown.Endorsements -
		weightLosses*candidate.breakdown.Losses
}

func financialEvidenceItems(pack EvidencePack) []FinancialEvidence {
	items := make([]FinancialEvidence, 0, len(pack.CareerEarnings)+len(pack.FightPaydays)+len(pack.CurrentWealthEstimates))
	items = append(items, pack.CareerEarnings...)
	items = append(items, pack.FightPaydays...)
	items = append(items, pack.CurrentWealthEstimates...)
	return items
}

func supportMetrics(pack EvidencePack) (float64, float64) {
	claims, cited := 0, 0
	confidenceTotal := 0.0
	for _, fact := range pack.Facts {
		claims++
		cited += len(fact.SourceIDs)
		confidenceTotal += fact.Confidence
	}
	for _, item := range financialEvidenceItems(pack) {
		claims++
		cited += len(item.SourceIDs)
		confidenceTotal += item.Confidence
	}
	for _, item := range pack.Businesses {
		claims++
		cited += len(item.SourceIDs)
		confidenceTotal += item.Confidence
	}
	for _, item := range pack.Endorsements {
		claims++
		cited += len(item.SourceIDs)
		confidenceTotal += item.Confidence
	}
	for _, item := range pack.FinancialEvents {
		claims++
		cited += len(item.SourceIDs)
		confidenceTotal += item.Confidence
	}
	confidence := 0.0
	if claims > 0 {
		confidence = confidenceTotal / float64(claims)
	}
	sourceCoverage := math.Min(1, float64(len(pack.Sources))/4)
	citationCoverage := 0.0
	if claims > 0 {
		citationCoverage = math.Min(1, float64(cited)/float64(claims*2))
	}
	return 0.60*sourceCoverage + 0.25*citationCoverage + 0.15*confidence, confidence
}

func collectValues(pack EvidencePack) rationaleData {
	var data rationaleData
	data.wealthValue, data.hasWealth = maxFinancialValue(pack.CurrentWealthEstimates)
	data.careerValue, data.hasCareer = maxFinancialValue(pack.CareerEarnings)
	data.paydayValue, data.hasPayday = maxFinancialValue(pack.FightPaydays)
	for _, item := range pack.Businesses {
		if item.FinancialOutcome == nil {
			continue
		}
		if value, ok := comparableUSD(*item.FinancialOutcome); ok && value > data.businessValue {
			data.businessValue, data.hasBusiness = value, true
		}
	}
	for _, item := range pack.Endorsements {
		if item.Compensation == nil {
			continue
		}
		if value, ok := comparableUSD(*item.Compensation); ok && value > data.endorsementValue {
			data.endorsementValue, data.hasEndorsement = value, true
		}
	}
	for _, item := range pack.FinancialEvents {
		if item.Impact == nil || !strings.Contains(strings.ToLower(item.Kind), "loss") {
			continue
		}
		if value, ok := comparableUSD(*item.Impact); ok && value > data.lossValue {
			data.lossValue, data.hasLoss = value, true
		}
	}
	return data
}

func maxFinancialValue(items []FinancialEvidence) (float64, bool) {
	max := 0.0
	found := false
	for _, item := range items {
		if value, ok := comparableUSD(item.Value); ok && value > max {
			max, found = value, true
		}
	}
	return max, found
}

func comparableUSD(value MoneyValue) (float64, bool) {
	if value.USDValue != nil && validMoney(*value.USDValue) {
		return *value.USDValue, true
	}
	if strings.EqualFold(strings.TrimSpace(value.Currency), "USD") {
		if value.Amount != nil && validMoney(*value.Amount) {
			return *value.Amount, true
		}
		if value.Low != nil && validMoney(*value.Low) {
			return *value.Low, true
		}
	}
	return 0, false
}

func detectConflicts(pack EvidencePack) []EstimateConflict {
	groups := map[string][]FinancialEvidence{
		"current_wealth":  pack.CurrentWealthEstimates,
		"career_earnings": pack.CareerEarnings,
		"fight_paydays":   pack.FightPaydays,
	}
	var conflicts []EstimateConflict
	for category, items := range groups {
		values := make([]float64, 0, len(items))
		ids := make([]string, 0, len(items))
		for _, item := range items {
			if item.Value.Kind != MoneyEstimate {
				continue
			}
			value, ok := comparableUSD(item.Value)
			if !ok || value <= 0 {
				continue
			}
			values = append(values, value)
			ids = append(ids, item.ID)
		}
		if len(values) < 2 {
			continue
		}
		low, high := values[0], values[0]
		for _, value := range values[1:] {
			low, high = math.Min(low, value), math.Max(high, value)
		}
		spread := high / low
		if spread <= 1.20 {
			continue
		}
		id := fmt.Sprintf("%s-%s-conflict", normalizedName(pack.Subject), category)
		conflicts = append(conflicts, EstimateConflict{
			ID: id, Subject: pack.Subject, Category: category, EvidenceIDs: ids,
			ValuesUSD: values, SpreadRatio: spread,
			Description: "credible estimates differ materially; the resolver preserves the disagreement and does not average them into an exact value",
		})
	}
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].ID < conflicts[j].ID })
	return conflicts
}

func buildRationale(candidate rankingCandidate, score float64) string {
	parts := make([]string, 0, 4)
	data := candidate.rationaleData
	if data.hasWealth {
		parts = append(parts, "supported current-wealth evidence")
	}
	if data.hasCareer || data.hasPayday {
		parts = append(parts, "documented boxing earnings and paydays")
	}
	if data.hasBusiness || data.hasEndorsement {
		parts = append(parts, "documented commercial activity outside the ring")
	}
	if len(candidate.conflicts) > 0 {
		parts = append(parts, "conflicting estimates are explicitly flagged rather than averaged")
	}
	if len(parts) == 0 {
		parts = append(parts, "source-backed qualitative evidence without a comparable USD amount")
	}
	return fmt.Sprintf("Relative score %.4f based on %s; this position is evidence-weighted and is not a mathematically certain net-worth claim.", score, strings.Join(parts, ", "))
}

func indexPacks(packs []EvidencePack) map[string]EvidencePack {
	indexed := make(map[string]EvidencePack, len(packs))
	for _, pack := range packs {
		key := normalizedName(pack.Subject)
		if key != "" {
			indexed[key] = pack
		}
	}
	return indexed
}

func uniqueNames(names []string) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		key := normalizedName(name)
		if key == "" {
			return nil, errors.New("candidate subject is required")
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate candidate %q", name)
		}
		seen[key] = struct{}{}
	}
	return seen, nil
}

func normalizedName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
