package script

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// EditorialManifestVersion is the canonical per-scene editorial contract.
const EditorialManifestVersion = "editorial.v1"

// EditorialSceneBullet is an extractive sentence span within one scene.
// Sentence indices are global (transcript-wide) when GloballyIndexed is true,
// otherwise local to the scene text.
type EditorialSceneBullet struct {
	SentenceStart int    `json:"sentence_start"`
	SentenceEnd   int    `json:"sentence_end"`
	Text          string `json:"text"`
}

// EditorialHighlight is a short verbatim span for synchronized overlays.
type EditorialHighlight struct {
	SentenceIndex  int     `json:"sentence_index"`
	StartByte      int     `json:"start_byte"`
	EndByte        int     `json:"end_byte"`
	Text           string  `json:"text"`
	Score          float64 `json:"score"`
	VisualEligible bool    `json:"visual_eligible"`
}

// EditorialScene is the immutable per-scene editorial product.
// Title is nil when no reliable candidate exists (TitleStatus=unavailable);
// diagnostics may use SceneID, never a fabricated public title.
type EditorialScene struct {
	SceneID         string                 `json:"scene_id"`
	Title           *string                `json:"title,omitempty"`
	TitleStatus     string                 `json:"title_status"`
	TitleSource     string                 `json:"title_source"`
	Bullets         []EditorialSceneBullet `json:"bullets"`
	Highlights      []EditorialHighlight   `json:"highlights"`
	GloballyIndexed bool                   `json:"globally_indexed"`
}

// EditorialManifest is the single canonical editorial result per job.
// Translations are projections of this EN-canonical manifest, never a
// second extraction source.
type EditorialManifest struct {
	SchemaVersion  string           `json:"schema_version"`
	SourceLanguage string           `json:"source_language"`
	ProfileVersion string           `json:"profile_version"`
	Scenes         []EditorialScene `json:"scenes"`
	Certified      bool             `json:"certified"`
	Fingerprint    string           `json:"fingerprint"`
}

// SceneAnalysisInput carries verified scene identity into analysis.
// StartByte/EndByte locate Text verbatim inside the transcript when known.
type SceneAnalysisInput struct {
	SceneID   string  `json:"scene_id"`
	Text      string  `json:"text"`
	Topic     *string `json:"topic,omitempty"`
	StartByte *int    `json:"start_byte,omitempty"`
	EndByte   *int    `json:"end_byte,omitempty"`
}

// EditorialFingerprintInput covers everything that influences the result.
type EditorialFingerprintInput struct {
	Transcript       string
	SceneIDs         []string
	SceneTexts       []string
	SceneOffsets     [][2]int64
	SceneTopics      []string
	Language         string
	ProfileVersion   string
	RankingWeights   map[string]float64
	TokenizerVersion string
	EmbeddingModel   string
	EmbeddingVersion string
	AlgorithmVersion string
}

// ComputeEditorialFingerprint hashes the canonical JSON of the full input.
func ComputeEditorialFingerprint(in EditorialFingerprintInput) (string, error) {
	if strings.TrimSpace(in.Transcript) == "" {
		return "", errors.New("editorial fingerprint requires transcript")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return digest.SHA256Bytes(raw), nil
}

// Validate performs fail-closed structural checks (no semantic judgment).
func (m *EditorialManifest) Validate() error {
	if m == nil {
		return errors.New("nil editorial manifest")
	}
	if m.SchemaVersion != EditorialManifestVersion {
		return errors.New("unknown editorial schema version")
	}
	seen := map[string]struct{}{}
	for _, s := range m.Scenes {
		if strings.TrimSpace(s.SceneID) == "" {
			return errors.New("scene with empty id")
		}
		if _, dup := seen[s.SceneID]; dup {
			return errors.New("duplicate scene identity")
		}
		seen[s.SceneID] = struct{}{}
		if s.TitleStatus != "resolved" && s.TitleStatus != "unavailable" {
			return errors.New("invalid title_status")
		}
		if s.TitleStatus == "unavailable" && s.Title != nil {
			return errors.New("unavailable title must have no text")
		}
	}
	return nil
}
