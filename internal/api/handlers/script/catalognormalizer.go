package script

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/pkg/textutil"
)

type catalogNormalizerService struct {
	stockRepo   *clips.Repository
	clipsRepo   *clips.Repository
	artlistRepo *clips.Repository
	log         *zap.Logger

	once    sync.Once
	mu      sync.RWMutex
	indexed []catalogEntry
}

type catalogEntry struct {
	Database string
	Source   string
	Name     string
	Path     string
	Link     string
	Tags     []string
	Tokens   []string
	Score    int
}

type NormalizedSegment struct {
	RawHash               string
	SemanticHash          string
	CanonicalSubject      string
	CanonicalKeywords     []string
	CanonicalEntities     []string
	NormalizationSource   string
	NormalizationPath     string
	NormalizationLink     string
	NormalizationScore    int
	OriginalKeywordsJSON  string
	OriginalEntitiesJSON  string
	CanonicalKeywordsJSON string
	CanonicalEntitiesJSON string
}

type SegmentInput struct {
	Topic         string
	Duration      int
	Template      string
	Subject       string
	NarrativeText string
	Keywords      []string
	Entities      []string
}

func newCatalogNormalizerService(stockRepo, clipsRepo, artlistRepo *clips.Repository, log *zap.Logger) *catalogNormalizerService {
	return &catalogNormalizerService{
		stockRepo:   stockRepo,
		clipsRepo:   clipsRepo,
		artlistRepo: artlistRepo,
		log:         log,
	}
}

func (s *catalogNormalizerService) NormalizeSegment(ctx context.Context, input SegmentInput) (*NormalizedSegment, error) {
	if strings.TrimSpace(input.Subject) == "" && strings.TrimSpace(input.NarrativeText) == "" {
		return nil, fmt.Errorf("segment input is empty")
	}
	if err := s.ensureIndex(ctx); err != nil {
		return nil, err
	}

	rawKeywords := uniqueOrderedStrings(append([]string{}, input.Keywords...))
	rawEntities := uniqueOrderedStrings(append([]string{}, input.Entities...))
	rawHash := hashNormalizationPayload(input.Topic, input.Duration, input.Template, input.Subject, input.NarrativeText, rawKeywords, rawEntities)

	semanticCandidate := s.resolveBestCandidate(input.Subject, rawKeywords, rawEntities, input.NarrativeText)
	canonicalSubject := strings.TrimSpace(input.Subject)
	canonicalKeywords := append([]string{}, rawKeywords...)
	canonicalEntities := append([]string{}, rawEntities...)
	source := ""
	path := ""
	link := ""
	score := 0

	if semanticCandidate != nil {
		score = semanticCandidate.Score
		source = semanticCandidate.Source
		path = semanticCandidate.Path
		link = semanticCandidate.Link
		if strings.TrimSpace(semanticCandidate.Name) != "" && shouldPromoteCanonicalSubject(semanticCandidate) {
			canonicalSubject = semanticCandidate.Name
		}
		canonicalKeywords = uniqueOrderedStrings(append(canonicalKeywords, tokenizeForCanonical(semanticCandidate.Name)...))
		canonicalKeywords = uniqueOrderedStrings(append(canonicalKeywords, tokenizeForCanonical(semanticCandidate.Path)...))
	}

	canonicalEntities = s.canonicalizeEntities(canonicalEntities)
	canonicalKeywords = s.canonicalizeKeywords(canonicalKeywords, canonicalSubject)

	canonicalHash := hashNormalizationPayload(input.Topic, input.Duration, input.Template, canonicalSubject, path, canonicalKeywords, canonicalEntities)

	return &NormalizedSegment{
		RawHash:               rawHash,
		SemanticHash:          canonicalHash,
		CanonicalSubject:      canonicalSubject,
		CanonicalKeywords:     canonicalKeywords,
		CanonicalEntities:     canonicalEntities,
		NormalizationSource:   source,
		NormalizationPath:     path,
		NormalizationLink:     link,
		NormalizationScore:    score,
		OriginalKeywordsJSON:  mustJSON(rawKeywords),
		OriginalEntitiesJSON:  mustJSON(rawEntities),
		CanonicalKeywordsJSON: mustJSON(canonicalKeywords),
		CanonicalEntitiesJSON: mustJSON(canonicalEntities),
	}, nil
}

func (s *catalogNormalizerService) ensureIndex(ctx context.Context) error {
	var err error
	s.once.Do(func() {
		var entries []catalogEntry
		entries, err = s.loadEntries(ctx)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.indexed = entries
		s.mu.Unlock()
	})
	return err
}

func (s *catalogNormalizerService) loadEntries(ctx context.Context) ([]catalogEntry, error) {
	type sourceSpec struct {
		db   string
		name string
		repo *clips.Repository
	}

	sources := []sourceSpec{
		{db: "stock.db.sqlite", name: "stock", repo: s.stockRepo},
		{db: "clips.db.sqlite", name: "clips", repo: s.clipsRepo},
		{db: "artlist.db.sqlite", name: "artlist", repo: s.artlistRepo},
	}

	entries := make([]catalogEntry, 0)
	for _, src := range sources {
		if src.repo == nil {
			continue
		}
		clipsList, err := src.repo.ListClips(ctx, "")
		if err != nil {
			if s.log != nil {
				s.log.Warn("catalog normalizer source load failed", zap.String("source", src.name), zap.Error(err))
			}
			continue
		}
		for _, c := range clipsList {
			if c == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(c.Category)) != "folder" {
				continue
			}
			if !isCatalogCandidate(c.Source, c.MediaType, src.name) {
				continue
			}
			name := strings.TrimSpace(c.Name)
			if name == "" {
				name = strings.TrimSpace(c.Filename)
			}
			if name == "" {
				name = strings.TrimSpace(filepath.Base(c.FolderPath))
			}
			if name == "" {
				continue
			}
			path := strings.TrimSpace(c.FolderPath)
			if path == "" {
				path = strings.TrimSpace(c.Group)
			}
			link := strings.TrimSpace(c.ExternalURL)
			if link == "" {
				link = strings.TrimSpace(c.DriveLink)
			}
			if link == "" && strings.TrimSpace(c.FolderID) != "" {
				link = "https://drive.google.com/drive/folders/" + strings.TrimSpace(c.FolderID)
			}
			tokens := uniqueOrderedStrings(append(tokenizeForCanonical(name), tokenizeForCanonical(path)...))
			tokens = uniqueOrderedStrings(append(tokens, tokenizeForCanonical(c.Group)...))
			tokens = uniqueOrderedStrings(append(tokens, c.Tags...))
			entries = append(entries, catalogEntry{
				Database: src.db,
				Source:   src.name,
				Name:     name,
				Path:     path,
				Link:     link,
				Tags:     append([]string{}, c.Tags...),
				Tokens:   tokens,
			})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source == entries[j].Source {
			return entries[i].Name < entries[j].Name
		}
		return sourcePriority(entries[i].Source) > sourcePriority(entries[j].Source)
	})
	return entries, nil
}

func (s *catalogNormalizerService) resolveBestCandidate(subject string, keywords, entities []string, narrative string) *catalogEntry {
	s.mu.RLock()
	entries := append([]catalogEntry{}, s.indexed...)
	s.mu.RUnlock()
	if len(entries) == 0 {
		return nil
	}

	queryTokens := uniqueOrderedStrings(append(tokenizeForCanonical(subject), keywords...))
	queryTokens = uniqueOrderedStrings(append(queryTokens, entities...))
	queryTokens = uniqueOrderedStrings(append(queryTokens, tokenizeForCanonical(narrative)...))

	bestScore := 0
	var best *catalogEntry
	for i := range entries {
		entry := entries[i]
		score := scoreCatalogEntry(queryTokens, entry)
		if score > bestScore {
			bestScore = score
			copyEntry := entry
			copyEntry.Score = score
			best = &copyEntry
		}
	}
	if best == nil || bestScore < 2 {
		return nil
	}
	if best.Source != "stock" {
		for i := range entries {
			entry := entries[i]
			if entry.Source != "stock" {
				continue
			}
			score := scoreCatalogEntry(queryTokens, entry)
			if score >= bestScore || (score >= bestScore-1 && isExactNameMatch(subject, entry.Name, entry.Path)) {
				copyEntry := entry
				copyEntry.Score = score
				best = &copyEntry
				bestScore = score
				break
			}
		}
	}
	return best
}

func (s *catalogNormalizerService) canonicalizeEntities(entities []string) []string {
	if len(entities) == 0 {
		return nil
	}
	out := make([]string, 0, len(entities))
	seen := make(map[string]struct{})
	for _, entity := range entities {
		entity = strings.TrimSpace(entity)
		if entity == "" {
			continue
		}
		candidate := s.resolveBestCandidate(entity, tokenizeForCanonical(entity), nil, entity)
		value := entity
		if candidate != nil && strings.TrimSpace(candidate.Name) != "" {
			value = candidate.Name
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (s *catalogNormalizerService) canonicalizeKeywords(keywords []string, subject string) []string {
	terms := uniqueOrderedStrings(append([]string{}, keywords...))
	terms = append(terms, tokenizeForCanonical(subject)...)
	return uniqueOrderedStrings(terms)
}

func scoreCatalogEntry(queryTokens []string, entry catalogEntry) int {
	if len(queryTokens) == 0 {
		return 0
	}
	targetTokens := append([]string{}, entry.Tokens...)
	targetTokens = uniqueOrderedStrings(targetTokens)
	if len(targetTokens) == 0 {
		return 0
	}

	score := tokenOverlapScore(queryTokens, targetTokens)
	normalizedQuery := normalizeForMatch(strings.Join(queryTokens, " "))
	normalizedName := normalizeForMatch(entry.Name)
	normalizedPath := normalizeForMatch(entry.Path)

	if normalizedQuery != "" && normalizedQuery == normalizedName {
		score += 30
	}
	if normalizedQuery != "" && normalizedPath != "" && strings.Contains(normalizedPath, normalizedQuery) {
		score += 18
	}
	if isExactNameMatch(strings.Join(queryTokens, " "), entry.Name, entry.Path) {
		score += 35
	}
	if entry.Source == "stock" {
		score += 5
	}
	if score > 100 {
		score = 100
	}
	return score
}

func isExactNameMatch(query, name, path string) bool {
	query = normalizeForMatch(query)
	name = normalizeForMatch(name)
	path = normalizeForMatch(path)
	if query == "" {
		return false
	}
	return query == name || query == path || strings.Contains(name, query) || strings.Contains(path, query)
}

func tokenOverlapScore(queryTokens, targetTokens []string) int {
	if len(queryTokens) == 0 || len(targetTokens) == 0 {
		return 0
	}
	targetSet := make(map[string]struct{}, len(targetTokens))
	for _, tok := range targetTokens {
		targetSet[strings.ToLower(tok)] = struct{}{}
	}
	matches := 0
	for _, tok := range queryTokens {
		if _, ok := targetSet[strings.ToLower(tok)]; ok {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	return (matches * 100) / len(queryTokens)
}

func tokenizeForCanonical(text string) []string {
	tokens := textutil.Tokenize(text)
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" || len(tok) < 2 || textutil.IsStopWord(tok) {
			continue
		}
		out = append(out, tok)
	}
	return out
}

func normalizeForMatch(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.ReplaceAll(text, "_", " ")
	text = strings.ReplaceAll(text, "-", " ")
	text = strings.Join(strings.Fields(text), " ")
	return text
}

func uniqueOrderedStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func hashNormalizationPayload(parts ...interface{}) string {
	payload, _ := json.Marshal(parts)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func mustJSON(v any) string {
	if v == nil {
		return "[]"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(data)
}

func sourcePriority(source string) int {
	switch source {
	case "stock":
		return 3
	case "clips":
		return 2
	case "artlist":
		return 1
	default:
		return 0
	}
}

func isCatalogCandidate(source, mediaType, defaultSource string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	defaultSource = strings.ToLower(strings.TrimSpace(defaultSource))
	if defaultSource == "stock" {
		return source == "stock" || mediaType == "stock" || source == ""
	}
	if defaultSource == "clips" {
		return source == "clips" || mediaType == "clip" || mediaType == "clips" || source == ""
	}
	if defaultSource == "artlist" {
		return source == "artlist" || mediaType == "artlist" || source == ""
	}
	return true
}

func shouldPromoteCanonicalSubject(candidate *catalogEntry) bool {
	if candidate == nil {
		return false
	}
	if candidate.Score >= 50 {
		return true
	}
	if candidate.Source == "stock" && candidate.Score >= 35 {
		return true
	}
	return false
}
