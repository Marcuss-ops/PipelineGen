package script

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const ResearchEvidenceVersion = "research-evidence-v1"

type RankedResearchCandidate struct {
	CandidateID string              `json:"candidate_id"`
	Label       string              `json:"label"`
	Rank        int                 `json:"rank"`
	Score       float64             `json:"score,omitempty"`
	Rationale   string              `json:"rationale,omitempty"`
	Fingerprint string              `json:"fingerprint"`
	CacheKey    string              `json:"cache_key,omitempty"`
	Sources     []ResearchWebSource `json:"sources"`
	Claims      []ResearchClaim     `json:"claims"`
	// MetricEvidenceQuality is HIGH|MEDIUM|LOW|NONE for the requested ranking
	// metric; MetricClaimCount is how many verified claims mention it. Together
	// they let a consumer spot a single weak candidate (e.g. Canelo) without
	// treating the whole ranking as uncertain.
	MetricEvidenceQuality string `json:"metric_evidence_quality,omitempty"`
	MetricClaimCount      int    `json:"metric_claim_count,omitempty"`
}

type ResearchEvidencePack struct {
	Version     string                    `json:"version"`
	Topic       string                    `json:"topic"`
	Fingerprint string                    `json:"fingerprint"`
	Candidates  []RankedResearchCandidate `json:"candidates"`
}

func (p *ResearchEvidencePack) Validate() error {
	if p == nil {
		return fmt.Errorf("research evidence pack is nil")
	}
	if p.Version != ResearchEvidenceVersion {
		return fmt.Errorf("unsupported research evidence version %q", p.Version)
	}
	if strings.TrimSpace(p.Topic) == "" {
		return fmt.Errorf("research evidence topic is required")
	}
	if len(p.Candidates) == 0 {
		return fmt.Errorf("research evidence candidates are empty")
	}
	ids := make(map[string]struct{}, len(p.Candidates))
	ranks := make(map[int]struct{}, len(p.Candidates))
	for _, candidate := range p.Candidates {
		if strings.TrimSpace(candidate.CandidateID) == "" {
			return fmt.Errorf("research candidate id is required")
		}
		if _, ok := ids[candidate.CandidateID]; ok {
			return fmt.Errorf("duplicate research candidate %q", candidate.CandidateID)
		}
		ids[candidate.CandidateID] = struct{}{}
		if candidate.Rank < 1 || candidate.Rank > len(p.Candidates) {
			return fmt.Errorf("candidate %q has invalid rank %d", candidate.CandidateID, candidate.Rank)
		}
		if _, ok := ranks[candidate.Rank]; ok {
			return fmt.Errorf("duplicate research rank %d", candidate.Rank)
		}
		ranks[candidate.Rank] = struct{}{}
		if len(candidate.Sources) == 0 {
			return fmt.Errorf("candidate %q has no sources", candidate.CandidateID)
		}
		sources := make(map[string]struct{}, len(candidate.Sources))
		for _, source := range candidate.Sources {
			if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.URL) == "" {
				return fmt.Errorf("candidate %q contains an invalid source", candidate.CandidateID)
			}
			if _, ok := sources[source.ID]; ok {
				return fmt.Errorf("candidate %q has duplicate source %q", candidate.CandidateID, source.ID)
			}
			sources[source.ID] = struct{}{}
		}
		for _, claim := range candidate.Claims {
			if !claim.Verified {
				continue
			}
			for _, sourceID := range claim.SourceIDs {
				if _, ok := sources[sourceID]; !ok {
					return fmt.Errorf("candidate %q claim references unknown source %q", candidate.CandidateID, sourceID)
				}
			}
		}
	}
	for rank := 1; rank <= len(p.Candidates); rank++ {
		if _, ok := ranks[rank]; !ok {
			return fmt.Errorf("research ranking missing rank %d", rank)
		}
	}
	return nil
}

func (p *ResearchEvidencePack) ModelSourceText() string {
	if p == nil {
		return ""
	}
	candidates := append([]RankedResearchCandidate(nil), p.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Rank < candidates[j].Rank })
	var out strings.Builder
	fmt.Fprintf(&out, "Topic: %s\n\n", p.Topic)
	for _, candidate := range candidates {
		fmt.Fprintf(&out, "RANK %d\nCandidate: %s\nRationale: %s\n", candidate.Rank, candidate.Label, candidate.Rationale)
		claimCount := 0
		for _, claim := range candidate.Claims {
			if claim.Verified {
				text := strings.Join(strings.Fields(claim.Text), " ")
				if len(text) > 700 {
					text = text[:700] + "..."
				}
				fmt.Fprintf(&out, "- %s\n", text)
				claimCount++
				if claimCount == 4 {
					break
				}
			}
		}
		out.WriteString("\n")
	}
	return strings.TrimSpace(out.String())
}

// NarrativePlanInstructions is the canonical section contract for a
// multi-candidate research script.  The evidence remains global so it can be
// audited and persisted, but the writer must receive an explicit ownership
// boundary for every scene; otherwise it tends to repeat the best-supported
// candidates in every section.
func (p *ResearchEvidencePack) NarrativePlanInstructions() string {
	if p == nil || len(p.Candidates) == 0 {
		return ""
	}
	candidates := append([]RankedResearchCandidate(nil), p.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Rank < candidates[j].Rank })

	var out strings.Builder
	out.WriteString("CANONICAL NARRATIVE PLAN — FOLLOW EXACTLY\n")
	out.WriteString("Write one unified documentary script with this exact structure:\n")
	out.WriteString("INTRO\n")
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		scene := len(candidates) - candidate.Rank + 1
		fmt.Fprintf(&out, "SCENE %d — RANK #%d — %s\n", scene, candidate.Rank, candidate.Label)
	}
	out.WriteString("CONCLUSION\n\n")
	out.WriteString("SCENE OWNERSHIP RULES:\n")
	out.WriteString("- Each ranked candidate owns exactly one scene: the scene named above.\n")
	out.WriteString("- In a candidate's scene, discuss only that candidate; do not reuse another candidate as the scene's subject.\n")
	out.WriteString("- Do not copy a generic ranking paragraph into multiple scenes.\n")
	out.WriteString("- Do not add candidates that are not in this plan.\n")
	out.WriteString("- Use the candidate's own evidence claims and sources for that scene.\n")
	out.WriteString("- The rank order is fixed after research; do not invent a different order.\n")
	out.WriteString("- Mention the exact candidate name in its own scene heading and narration.\n")
	out.WriteString("- Keep the introduction and conclusion separate from the ranked candidate scenes.\n")
	return strings.TrimSpace(out.String())
}

func (p *ResearchEvidencePack) ComputeFingerprint() (string, error) {
	if p == nil {
		return "", fmt.Errorf("nil research evidence pack")
	}
	copy := *p
	copy.Fingerprint = ""
	raw, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (p *ResearchEvidencePack) Clone() *ResearchEvidencePack {
	if p == nil {
		return nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	var clone ResearchEvidencePack
	if json.Unmarshal(raw, &clone) != nil {
		return nil
	}
	return &clone
}
