package nlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const maxNERResponseBytes = 1 << 20

// SpacyNERAdapter calls the configured spaCy sidecar and converts Python
// Unicode-codepoint offsets into the UTF-8 byte offsets required by VisualNER.
// It does not perform fallback or model selection.
type SpacyNERAdapter struct {
	endpoint   string
	client     *http.Client
	timingMu   sync.RWMutex
	lastTiming SpacyInferenceTiming
}

func NewSpacyNERAdapter(baseURL string, client *http.Client) (*SpacyNERAdapter, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("spacy NER: valid http(s) base URL without credentials, query, or fragment is required")
	}
	parsed.Path = path.Join(parsed.Path, "ner", "extract")
	if !strings.HasPrefix(parsed.Path, "/") {
		parsed.Path = "/" + parsed.Path
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &SpacyNERAdapter{endpoint: parsed.String(), client: client}, nil
}

type spacyNERRequest struct {
	Version     string `json:"version"`
	Operation   string `json:"operation"`
	SourceText  string `json:"source_text"`
	Language    string `json:"language"`
	EntityCount int    `json:"entity_count"`
}

type spacyNEREntity struct {
	Text      string `json:"text"`
	Label     string `json:"label"`
	StartChar int    `json:"start_char"`
	EndChar   int    `json:"end_char"`
}

type spacyNERResponse struct {
	Version       string           `json:"version"`
	Entities      []spacyNEREntity `json:"entities"`
	Model         string           `json:"model"`
	ModelLoadMS   float64          `json:"model_load_ms"`
	InferenceMS   float64          `json:"inference_ms"`
	MaxRSSProcess float64          `json:"sidecar_process_max_rss_mb"`
}

type SpacyInferenceTiming struct {
	Model           string
	ModelLoadMS     float64
	InferenceMS     float64
	SidecarMaxRSSMB float64
}

func (a *SpacyNERAdapter) LastInferenceTiming() SpacyInferenceTiming {
	if a == nil {
		return SpacyInferenceTiming{}
	}
	a.timingMu.RLock()
	defer a.timingMu.RUnlock()
	return a.lastTiming
}

func (a *SpacyNERAdapter) Extract(ctx context.Context, language, sourceText string, entityCount int) ([]scriptgen.VisualEntity, error) {
	if a == nil || a.client == nil || a.endpoint == "" {
		return nil, fmt.Errorf("spacy NER: adapter is not configured")
	}
	if !utf8.ValidString(sourceText) || strings.TrimSpace(sourceText) == "" || utf8.RuneCountInString(sourceText) > 100_000 {
		return nil, fmt.Errorf("spacy NER: source text must contain 1..100000 valid Unicode codepoints")
	}
	if entityCount < 0 || entityCount > 1000 {
		return nil, fmt.Errorf("spacy NER: entity count must be in 0..1000")
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return nil, fmt.Errorf("spacy NER: language is required")
	}
	body, err := json.Marshal(spacyNERRequest{
		Version: "ner.v1", Operation: "extract", SourceText: sourceText,
		Language: language, EntityCount: entityCount,
	})
	if err != nil {
		return nil, fmt.Errorf("spacy NER: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("spacy NER: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spacy NER: request: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxNERResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("spacy NER: read response: %w", err)
	}
	if len(responseBody) > maxNERResponseBytes {
		return nil, fmt.Errorf("spacy NER: response exceeds %d bytes", maxNERResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spacy NER: endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var decoded spacyNERResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("spacy NER: decode response: %w", err)
	}
	if decoded.Version != "ner.v1" {
		return nil, fmt.Errorf("spacy NER: unsupported response version %q", decoded.Version)
	}
	if strings.TrimSpace(decoded.Model) == "" {
		return nil, fmt.Errorf("spacy NER: response model identity is required")
	}
	effectiveEntityCount := entityCount
	if effectiveEntityCount == 0 {
		// Match Rust VisualNER's shared contract: zero selects its safe default.
		effectiveEntityCount = 3
	}
	if len(decoded.Entities) > 1000 || len(decoded.Entities) > effectiveEntityCount {
		return nil, fmt.Errorf("spacy NER: response exceeds requested entity count")
	}
	if !finiteNonNegative(decoded.ModelLoadMS) || !finiteNonNegative(decoded.InferenceMS) || !finiteNonNegative(decoded.MaxRSSProcess) {
		return nil, fmt.Errorf("spacy NER: response timing/resource values must be finite and non-negative")
	}
	a.timingMu.Lock()
	a.lastTiming = SpacyInferenceTiming{Model: decoded.Model, ModelLoadMS: decoded.ModelLoadMS, InferenceMS: decoded.InferenceMS, SidecarMaxRSSMB: decoded.MaxRSSProcess}
	a.timingMu.Unlock()
	codepointOffsets := utf8ByteOffsets(sourceText)
	entities := make([]scriptgen.VisualEntity, 0, len(decoded.Entities))
	for i, candidate := range decoded.Entities {
		if candidate.StartChar < 0 || candidate.EndChar < 0 || candidate.StartChar >= len(codepointOffsets) || candidate.EndChar >= len(codepointOffsets) {
			return nil, fmt.Errorf("spacy NER: entity[%d] has invalid character offsets", i)
		}
		start, end := codepointOffsets[candidate.StartChar], codepointOffsets[candidate.EndChar]
		if end <= start || end > len(sourceText) || !utf8.ValidString(sourceText[start:end]) {
			return nil, fmt.Errorf("spacy NER: entity[%d] has invalid character offsets", i)
		}
		evidence := sourceText[start:end]
		if candidate.Text != evidence {
			return nil, fmt.Errorf("spacy NER: entity[%d] text is not the exact source span", i)
		}
		label := normalizeSpacyLabel(candidate.Label)
		if label == "" {
			return nil, fmt.Errorf("spacy NER: entity[%d] has unsupported label %q", i, candidate.Label)
		}
		entities = append(entities, scriptgen.VisualEntity{
			Text: evidence, Type: scriptpkg.EntityType(label), Start: start, End: end,
			Evidence: evidence,
		})
	}
	return entities, nil
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func utf8ByteOffsets(text string) []int {
	offsets := make([]int, 0, utf8.RuneCountInString(text)+1)
	for byteOffset := range text {
		offsets = append(offsets, byteOffset)
	}
	return append(offsets, len(text))
}

func normalizeSpacyLabel(label string) string {
	switch strings.ToUpper(strings.TrimSpace(label)) {
	case "PER", "PERSON":
		return "PERSON"
	case "ORG", "ORGANIZATION", "COMPANY", "CORP", "CORPORATION", "BUSINESS":
		return "ORG"
	case "LOC", "GPE", "LOCATION", "PLACE", "CITY", "COUNTRY", "FAC", "FACILITY":
		return "GPE"
	case "DATE", "DATETIME", "CALENDAR_DATE":
		return "DATE"
	case "TIME":
		return "TIME"
	case "CARDINAL":
		return "CARDINAL"
	case "ORDINAL":
		return "ORDINAL"
	case "MONEY":
		return "MONEY"
	case "PERCENT", "PERCENTAGE":
		return "PERCENT"
	case "QUANTITY", "METRIC", "METRICS", "STATISTIC", "STATISTICS", "NUMBER", "NUM":
		return "NUMBER"
	case "PRODUCT":
		return "PRODUCT"
	case "EVENT":
		return "EVENT"
	case "WORK", "WORK_OF_ART":
		return "WORK_OF_ART"
	case "BRAND", "LOGO":
		return "LOGO"
	case "MISC", "NORP", "LAW", "LANGUAGE", "CONCEPT", "VISUAL_CONCEPT":
		return "CONCEPT"
	default:
		return ""
	}
}

var _ scriptgen.NERBackend = (*SpacyNERAdapter)(nil)
