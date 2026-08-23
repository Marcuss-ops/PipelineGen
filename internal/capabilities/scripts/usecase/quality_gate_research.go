package usecase

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// researchCandidateCoverageChecker prevents a successful research job from
// silently omitting one of the subjects that the evidence pack promised.
// The resolver already guarantees evidence coverage; this second check guards
// the model boundary, where an LLM can still skip a candidate in its prose.
type researchCandidateCoverageChecker struct{}

func (researchCandidateCoverageChecker) Name() string { return "research_candidate_coverage" }

func (researchCandidateCoverageChecker) Check(in qualityGateInput) []string {
	if in.plan.ResearchEvidence == nil || len(in.plan.ResearchEvidence.Candidates) == 0 {
		return nil
	}
	text := researchCoverageText(in.result.Output.Text)
	missing := make([]string, 0)
	for _, candidate := range in.plan.ResearchEvidence.Candidates {
		if !strings.Contains(text, researchCoverageText(candidate.Label)) {
			missing = append(missing, candidate.Label)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("research output omits %d ranked candidate(s): %s", len(missing), strings.Join(missing, ", "))}
}

func researchCoverageText(value string) string {
	value = norm.NFD.String(strings.ToLower(value))
	var b strings.Builder
	for _, r := range value {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
