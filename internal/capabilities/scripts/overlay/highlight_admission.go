// Package overlay — highlight_admission.go is PREPARATION ONLY (Fase 1).
//
// HighlightAdmissionResolver decides precedence between explicit requested
// phrases and automatic extracted highlights for the same visual slots.
// It is a pure function over immutable candidates and is NOT wired into
// overlay production yet: enabling it changes rendered videos, which belongs
// to the explicit visual-integration phase, not to Fase 1.
package overlay

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// HighlightCandidate is one immutable admission contender.
type HighlightCandidate struct {
	Text      string
	Kind      string // "requested" | "extracted"
	SceneID   string
	StartByte int
	EndByte   int
	Score     float64
}

// AdmittedHighlight is the resolver output for one slot.
type AdmittedHighlight struct {
	Text     string `json:"text"`
	Kind     string `json:"kind"`
	SceneID  string `json:"scene_id"`
	Priority int    `json:"priority"`
}

// AdmissionConfig bounds the visual slots per scene.
type AdmissionConfig struct {
	MaxPerScene int
}

// DefaultAdmissionConfig keeps the historical overlay budget.
func DefaultAdmissionConfig() AdmissionConfig { return AdmissionConfig{MaxPerScene: 2} }

// ResolveHighlightAdmission merges requested + extracted candidates:
// requested wins on identical text, requested never bypasses text/timing
// validation (done upstream), and overflow extracted highlights are simply
// not admitted. Pure and deterministic.
func ResolveHighlightAdmission(requested, extracted []HighlightCandidate, cfg AdmissionConfig) []AdmittedHighlight {
	if cfg.MaxPerScene <= 0 {
		cfg = DefaultAdmissionConfig()
	}
	byScene := map[string][]HighlightCandidate{}
	order := []string{}
	push := func(c HighlightCandidate) {
		if _, ok := byScene[c.SceneID]; !ok {
			order = append(order, c.SceneID)
		}
		byScene[c.SceneID] = append(byScene[c.SceneID], c)
	}
	for _, c := range requested {
		c.Kind = "requested"
		push(c)
	}
	for _, c := range extracted {
		c.Kind = "extracted"
		push(c)
	}
	out := []AdmittedHighlight{}
	for _, scene := range order {
		req, ext := []HighlightCandidate{}, []HighlightCandidate{}
		for _, c := range byScene[scene] {
			if c.Kind == "requested" {
				req = append(req, c)
			} else {
				ext = append(ext, c)
			}
		}
		// Deduplicate: requested text wins over identical extracted text.
		takenText := map[string]struct{}{}
		admitted := []AdmittedHighlight{}
		for _, c := range req {
			key := strings.ToLower(strings.TrimSpace(c.Text))
			takenText[key] = struct{}{}
			admitted = append(admitted, AdmittedHighlight{Text: c.Text, Kind: "requested", SceneID: scene, Priority: 0})
			if len(admitted) >= cfg.MaxPerScene {
				break
			}
		}
		if len(admitted) < cfg.MaxPerScene {
			// Extracted sorted by score desc, stable by text.
			for i := 0; i < len(ext); i++ {
				for j := i + 1; j < len(ext); j++ {
					if ext[j].Score > ext[i].Score {
						ext[i], ext[j] = ext[j], ext[i]
					}
				}
			}
			for _, c := range ext {
				key := strings.ToLower(strings.TrimSpace(c.Text))
				if _, dup := takenText[key]; dup {
					continue
				}
				takenText[key] = struct{}{}
				admitted = append(admitted, AdmittedHighlight{Text: c.Text, Kind: "extracted", SceneID: scene, Priority: 1})
				if len(admitted) >= cfg.MaxPerScene {
					break
				}
			}
		}
		out = append(out, admitted...)
	}
	if out == nil {
		out = []AdmittedHighlight{}
	}
	return out
}

// CandidatesFromManifest projects an EditorialManifest into extracted
// candidates (prep helper for the future visual-integration phase).
func CandidatesFromManifest(m *scriptpkg.EditorialManifest) []HighlightCandidate {
	if m == nil {
		return nil
	}
	var out []HighlightCandidate
	for _, s := range m.Scenes {
		for _, h := range s.Highlights {
			if !h.VisualEligible {
				continue
			}
			out = append(out, HighlightCandidate{
				Text: h.Text, Kind: "extracted", SceneID: s.SceneID,
				StartByte: h.StartByte, EndByte: h.EndByte, Score: h.Score,
			})
		}
	}
	return out
}
