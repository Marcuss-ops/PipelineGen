package adapters

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"go.uber.org/zap"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// RebaseSceneAnnotations is the final output-boundary normalization. It is
// exported for the use-case finalizer so persisted/API SpecScene surfaces
// cannot retain spans from an earlier scene text version.
func RebaseSceneAnnotations(ann *scriptpkg.SceneAnnotations, text string) *scriptpkg.SceneAnnotations {
	return rebaseSceneAnnotations(ann, text)
}

// rebaseSceneAnnotations aligns provisional annotations with the final scene
// text. Extraction can precede scene synthesis, so spans produced from a
// segment hint must never be copied verbatim into the generated prose.
// Keyword spans that overlap a phrase or entity are removed to keep the
// annotation categories semantically distinct.
func rebaseSceneAnnotations(in *scriptpkg.SceneAnnotations, text string) *scriptpkg.SceneAnnotations {
	if in == nil {
		return nil
	}
	out := *in
	out.ImportantPhrases = nil
	out.ImportantWords = nil
	out.PrimaryEntities = nil
	out.SecondaryEntities = nil

	phraseSpans := make([]scriptpkg.AnnotationSpan, 0, len(in.ImportantPhrases))
	for _, span := range in.ImportantPhrases {
		if grounded, ok := findAnnotationSpan(text, span.Text); ok {
			span.StartRune, span.EndRune, span.Text = grounded.StartRune, grounded.EndRune, grounded.Text
			phraseSpans = append(phraseSpans, span)
		}
	}
	out.ImportantPhrases = phraseSpans

	entitySpans := make([]scriptpkg.AnnotationSpan, 0)
	rebaseEntity := func(entity scriptpkg.AnnotatedEntity) (scriptpkg.AnnotatedEntity, bool) {
		if strings.TrimSpace(entity.CanonicalName) == "" {
			entity.CanonicalName = strings.TrimSpace(entity.Text)
		}
		if strings.TrimSpace(entity.ID) == "" {
			entity.ID = "entity-" + safeAnnotationID(entity.CanonicalName)
		}
		mentions := append([]scriptpkg.AnnotationSpan(nil), entity.Mentions...)
		entity.Mentions = nil
		for _, mention := range mentions {
			if grounded, ok := findAnnotationSpan(text, mention.Text); ok {
				entity.Mentions = append(entity.Mentions, grounded)
			}
		}
		if len(entity.Mentions) == 0 {
			if grounded, ok := findAnnotationSpan(text, entity.Text); ok {
				entity.Mentions = []scriptpkg.AnnotationSpan{grounded}
			}
		}
		return entity, len(entity.Mentions) > 0
	}
	for _, entity := range in.PrimaryEntities {
		if grounded, ok := rebaseEntity(entity); ok {
			out.PrimaryEntities = append(out.PrimaryEntities, grounded)
			entitySpans = append(entitySpans, grounded.Mentions...)
		}
	}
	for _, entity := range in.SecondaryEntities {
		if grounded, ok := rebaseEntity(entity); ok {
			out.SecondaryEntities = append(out.SecondaryEntities, grounded)
			entitySpans = append(entitySpans, grounded.Mentions...)
		}
	}
	for _, word := range in.ImportantWords {
		grounded, ok := findAnnotationSpan(text, word.Text)
		if !ok || annotationSpanOverlapsAny(grounded, phraseSpans) || annotationSpanOverlapsAny(grounded, entitySpans) {
			continue
		}
		word.StartRune, word.EndRune, word.Text = grounded.StartRune, grounded.EndRune, grounded.Text
		key := strings.ToLower(strings.TrimSpace(word.Text))
		duplicate := false
		for _, existing := range out.ImportantWords {
			if strings.ToLower(strings.TrimSpace(existing.Text)) == key {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out.ImportantWords = append(out.ImportantWords, word)
		}
	}
	deduplicateGroundedEntities(&out)
	return &out
}

func deduplicateGroundedEntities(ann *scriptpkg.SceneAnnotations) {
	if ann == nil {
		return
	}
	keepLongest := func(in []scriptpkg.AnnotatedEntity) []scriptpkg.AnnotatedEntity {
		out := make([]scriptpkg.AnnotatedEntity, 0, len(in))
		for _, candidate := range in {
			replaced := false
			discard := false
			for i := range out {
				if out[i].Type != candidate.Type || !annotationSpansOverlap(out[i].Mentions, candidate.Mentions) {
					continue
				}
				if len([]rune(out[i].CanonicalName)) >= len([]rune(candidate.CanonicalName)) {
					discard = true
					break
				}
				out[i] = candidate
				replaced = true
				break
			}
			if !discard && !replaced {
				out = append(out, candidate)
			}
		}
		return out
	}
	ann.PrimaryEntities = keepLongest(ann.PrimaryEntities)
	secondary := keepLongest(ann.SecondaryEntities)
	filtered := secondary[:0]
	for _, candidate := range secondary {
		overlapsPrimary := false
		for _, primary := range ann.PrimaryEntities {
			if annotationSpansOverlap(primary.Mentions, candidate.Mentions) {
				overlapsPrimary = true
				break
			}
		}
		if !overlapsPrimary {
			filtered = append(filtered, candidate)
		}
	}
	ann.SecondaryEntities = filtered
}

func annotationSpansOverlap(left, right []scriptpkg.AnnotationSpan) bool {
	for _, a := range left {
		for _, b := range right {
			if a.StartRune < b.EndRune && b.StartRune < a.EndRune {
				return true
			}
		}
	}
	return false
}

func annotationSpanOverlapsAny(span scriptpkg.AnnotationSpan, others []scriptpkg.AnnotationSpan) bool {
	for _, other := range others {
		if span.StartRune < other.EndRune && other.StartRune < span.EndRune {
			return true
		}
	}
	return false
}

func findAnnotationSpan(text, value string) (scriptpkg.AnnotationSpan, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return scriptpkg.AnnotationSpan{}, false
	}
	runes, want := []rune(text), []rune(value)
	for start := 0; start+len(want) <= len(runes); start++ {
		match := true
		for i := range want {
			if runes[start+i] != want[i] {
				match = false
				break
			}
		}
		if match {
			return scriptpkg.AnnotationSpan{Text: string(want), StartRune: start, EndRune: start + len(want)}, true
		}
	}
	// Hints may differ only in capitalization or diacritics from the final
	// generated text (Cafe vs Cafe with an accent). Return the actual slice so
	// grounding and offsets remain canonical.
	for start := 0; start+len(want) <= len(runes); start++ {
		if equalsFoldAccents(string(runes[start:start+len(want)]), value) {
			return scriptpkg.AnnotationSpan{
				Text:      string(runes[start : start+len(want)]),
				StartRune: start,
				EndRune:   start + len(want),
			}, true
		}
	}
	return scriptpkg.AnnotationSpan{}, false
}

// equalsFoldAccents compares case-insensitively after folding common Latin
// diacritics. The comparison never changes the returned span text, so the
// document keeps its original spelling while the annotation stays grounded.
func equalsFoldAccents(left, right string) bool {
	a, b := []rune(left), []rune(right)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if foldAnnotationRune(a[i]) != foldAnnotationRune(b[i]) {
			return false
		}
	}
	return true
}

func foldAnnotationRune(r rune) rune {
	switch r {
	case 'à', 'á', 'â', 'ã', 'ä', 'å', 'À', 'Á', 'Â', 'Ã', 'Ä', 'Å':
		return 'a'
	case 'è', 'é', 'ê', 'ë', 'È', 'É', 'Ê', 'Ë':
		return 'e'
	case 'ì', 'í', 'î', 'ï', 'Ì', 'Í', 'Î', 'Ï':
		return 'i'
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'Ò', 'Ó', 'Ô', 'Õ', 'Ö':
		return 'o'
	case 'ù', 'ú', 'û', 'ü', 'Ù', 'Ú', 'Û', 'Ü':
		return 'u'
	case 'ç', 'Ç':
		return 'c'
	case 'ñ', 'Ñ':
		return 'n'
	case 'ý', 'ÿ', 'Ý', 'Ÿ':
		return 'y'
	}
	return unicode.ToLower(r)
}

// normalizeAnnotationType maps an extracted entity type onto the canonical
// annotation kind through the single kernel registry
// (script.NormalizeAnnotationType). It is a thin alias so the batch merger
// and the runner projection share one taxonomy owner.
func normalizeAnnotationType(raw string) string {
	return scriptpkg.NormalizeAnnotationType(raw)
}

func safeAnnotationID(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if b.Len() > 0 {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// semanticProductionProvider is the provenance stamped on assets retrieved by
// the semantic production plan. It is distinct from the per-segment
// `internet_images` path so a report can prove which queries were executed by
// the planner.
const semanticProductionProvider = "semantic_production_plan"

// SemanticAssetSearch adapts the canonical InternetImageSearcher to the
// semantic batch planner's narrow port. It performs no search of its own and
// owns no cache: every call is the provider path the rest of the pipeline uses,
// so the measured hit rate and latency describe the real retrieval surface.
type SemanticAssetSearch struct {
	searcher InternetImageSearcher
	scorer   SemanticCandidateScorer
}

// NewSemanticAssetSearch builds the adapter. A nil searcher yields a nil
// adapter, which the wiring treats as "planner unavailable" instead of
// registering a port that can only fail.
func NewSemanticAssetSearch(searcher InternetImageSearcher, scorers ...SemanticCandidateScorer) *SemanticAssetSearch {
	if searcher == nil {
		return nil
	}
	var scorer SemanticCandidateScorer
	if len(scorers) > 0 {
		scorer = scorers[0]
	}
	return &SemanticAssetSearch{searcher: searcher, scorer: scorer}
}

var _ scriptgen.SemanticAssetSearchPort = (*SemanticAssetSearch)(nil)

// MaxSemanticVerificationCandidates bounds how many provider candidates one
// semantic query sends to the pixel/VLM verifier. Provider recall returns
// 16–24 images per query; verifying all of them was the dominant cost of the
// 10 s batch (the ANN lookup is ~1.7 ms). The provider order is the ranking,
// and a candidate without an accepted verdict can never become a hit, so the
// tail only spends budget without changing the answer.
const MaxSemanticVerificationCandidates = 8

// capSemanticVerificationCandidates keeps the first limit candidates, which
// are the ones the provider ranked highest. It never reorders the input and
// returns the original slice when no cap is needed.
func capSemanticVerificationCandidates(in []scriptpkg.SegmentAssetCandidate, limit int) []scriptpkg.SegmentAssetCandidate {
	if limit <= 0 || len(in) <= limit {
		return in
	}
	return in[:limit]
}

// Search executes one exact-source query.
func (s *SemanticAssetSearch) Search(ctx context.Context, req scriptgen.SemanticAssetSearchRequest) (scriptgen.SemanticAssetSearchResponse, error) {
	if s == nil || s.searcher == nil {
		return scriptgen.SemanticAssetSearchResponse{}, fmt.Errorf("semantic asset search: searcher is not configured")
	}
	entity := strings.TrimSpace(req.Entity)
	if entity == "" {
		entity = strings.TrimSpace(req.Query)
	}
	recallStarted := time.Now()
	candidates, err := s.searcher.SearchImages(ctx, InternetImageSearchRequest{
		SegmentID:    strings.TrimSpace(req.CandidateID),
		Query:        req.Query,
		Entity:       entity,
		QueryClass:   req.QueryClass,
		QueryOrigin:  req.QueryOrigin,
		TemplateID:   req.TemplateID,
		Illustrative: req.Illustrative,
		Language:     req.Language,
		Limit:        req.Limit,
		Provider:     semanticProductionProvider,
	})
	if err != nil {
		return scriptgen.SemanticAssetSearchResponse{}, err
	}
	recallMS := time.Since(recallStarted).Milliseconds()
	candidates = capSemanticVerificationCandidates(candidates, MaxSemanticVerificationCandidates)
	for i := range candidates {
		if candidates[i].RetrievalProvider == "" && strings.TrimSpace(candidates[i].Provider) != "" &&
			!strings.EqualFold(strings.TrimSpace(candidates[i].Provider), scriptpkg.VidRushProviderInternetImages) {
			candidates[i].RetrievalProvider = strings.TrimSpace(candidates[i].Provider)
		}
		candidates[i].Provider = scriptpkg.VidRushProviderInternetImages
	}
	class := strings.TrimSpace(req.QueryClass)
	if class == "" {
		class = "generic"
	}
	verifyStarted := time.Now()
	candidates, err = resolveEntityVisualCandidates(ctx, s.scorer, class, req.SceneContext, candidates)
	if err != nil {
		return scriptgen.SemanticAssetSearchResponse{}, fmt.Errorf("semantic candidate verification: %w", err)
	}
	// Split recall from verification: the batch previously exposed only
	// total_ms, so a 10 s query could not be attributed to provider recall or
	// to the pixel/VLM verdict. Both stages are now visible per query and are
	// carried to the batch report for aggregation.
	verifyMS := time.Since(verifyStarted).Milliseconds()
	zap.L().Info("semantic asset search timing",
		zap.String("query", req.Query),
		zap.String("class", class),
		zap.Int("candidates", len(candidates)),
		zap.Int("verify_cap", MaxSemanticVerificationCandidates),
		zap.Int64("recall_ms", recallMS),
		zap.Int64("verify_ms", verifyMS))
	response := scriptgen.SemanticAssetSearchResponse{
		Hits:     make([]scriptgen.SemanticAssetHit, 0, len(candidates)),
		Provider: semanticProductionProvider,
		RecallMS: recallMS,
		VerifyMS: verifyMS,
	}
	for _, candidate := range candidates {
		if isPlaceholderImageReference(candidate.SourceURL, candidate.MIMEType) ||
			isPlaceholderImageReference(candidate.PreviewURL, candidate.MIMEType) {
			continue
		}
		assetID := strings.TrimSpace(candidate.AssetID)
		if assetID == "" {
			assetID = strings.TrimSpace(candidate.ContentSHA256)
		}
		if assetID == "" {
			assetID = strings.TrimSpace(candidate.PreviewURL)
		}
		url := strings.TrimSpace(candidate.PreviewURL)
		if url == "" {
			url = strings.TrimSpace(candidate.SourceURL)
		}
		if assetID == "" && url == "" {
			continue
		}
		// The upstream provider is the provenance the plan cache invalidates
		// by. When a cascade returns several providers, the answer is attributed
		// to the first one that produced a hit, not to whichever candidate
		// happened to be appended last.
		provider := strings.TrimSpace(candidate.RetrievalProvider)
		if provider == "" {
			provider = strings.TrimSpace(candidate.Provider)
		}
		if provider != "" && response.Provider == semanticProductionProvider {
			response.Provider = provider
		}
		response.Hits = append(response.Hits, scriptgen.SemanticAssetHit{
			AssetID: assetID, URL: url, SHA256: strings.TrimSpace(candidate.ContentSHA256),
			DriveLink: strings.TrimSpace(candidate.DriveLink), MediaType: strings.TrimSpace(candidate.MIMEType),
			Provider: provider, Entity: strings.TrimSpace(candidate.Entity),
			Width: candidate.Width, Height: candidate.Height,
			Score: candidate.Score, RelevanceScore: candidate.RelevanceScore,
			SemanticScore: candidate.SemanticScore, TechnicalQualityScore: candidate.TechnicalQualityScore,
			SemanticStatus:  strings.TrimSpace(candidate.SemanticStatus),
			VerifierVersion: strings.TrimSpace(candidate.VerifierVersion),
		})
	}
	return response, nil
}
