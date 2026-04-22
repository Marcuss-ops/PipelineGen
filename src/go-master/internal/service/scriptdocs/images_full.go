package scriptdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"velox/go-master/internal/imagesdb"
)

type imageCandidate struct {
	entity string
	query  string
	score  float64
}

var weakImageEntityTerms = map[string]bool{
	"above": true, "beneath": true, "below": true, "before": true, "after": true,
	"around": true, "behind": true, "between": true, "across": true, "through": true,
	"stretching": true, "stretch": true, "stretchs": true, "dominating": true,
	"dominate": true, "dominated": true, "opening": true, "closing": true,
	"visuals": true, "visual": true, "scene": true, "scenes": true,
	"environment": true, "wilderness": true, "beautiful": true, "dramatic": true,
	"sweeping": true, "interspersed": true, "remarkable": true, "future": true,
	"vital": true, "component": true, "testament": true, "nature": true,
	"landscape": true, "landscapes": true, "mystery": true, "focus": true,
	"unparalleled": true, "majestic": true, "vibrant": true, "light": true,
	"however": true, "therefore": true, "moreover": true, "furthermore": true,
	"despite": true, "although": true, "meanwhile": true, "because": true,
	"since": true, "while": true, "during": true, "afterward": true,
	"beforehand": true, "currently": true, "ongoing": true, "thus": true,
	"then": true, "yet": true, "romanian": true,
}

func (s *ScriptDocService) SetImagesDB(db *imagesdb.ImageDB) {
	s.imagesDB = db
}

func (s *ScriptDocService) SetImageFinder(f imageFinderAPI) {
	if f != nil {
		s.imageFinder = f
	}
}

func (s *ScriptDocService) SetImageDownloader(d imageAssetDownloaderAPI) {
	if d != nil {
		s.imageDownloader = d
	}
}

func (s *ScriptDocService) buildImagesFullAssociations(ctx context.Context, topic string, chapters []ScriptChapter, entityImages map[string]string) []ImageAssociation {
	if len(chapters) == 0 {
		return nil
	}

	seenURLs := make(map[string]bool)
	out := make([]ImageAssociation, 0, len(chapters)*4)
	for _, chapter := range chapters {
		out = append(out, s.buildImageAssociationsForChapter(ctx, topic, chapter, entityImages, seenURLs)...)
	}

	if len(out) == 0 {
		for _, candidate := range s.imageCandidatesForTopic(topic, entityImages) {
			rec, cached, trace, err := s.resolveImageForEntityWithTrace(ctx, topic, candidate.entity, candidate.query, ScriptChapter{}, 0)
			if err != nil || rec == nil || strings.TrimSpace(rec.ImageURL) == "" {
				continue
			}
			out = append(out, ImageAssociation{
				Phrase:       topic,
				Entity:       candidate.entity,
				Query:        candidate.query,
				ImageURL:     rec.ImageURL,
				Source:       rec.Source,
				Title:        rec.Title,
				PageURL:      rec.PageURL,
				Score:        candidate.score,
				Cached:       cached,
				LocalPath:    rec.LocalPath,
				MimeType:     rec.MimeType,
				FileSize:     rec.FileSizeBytes,
				AssetHash:    rec.AssetHash,
				DownloadedAt: formatTime(rec.DownloadedAt),
				Resolution:   trace,
			})
			break
		}
	}

	return out
}

func (s *ScriptDocService) buildImageAssociationsForChapter(ctx context.Context, topic string, chapter ScriptChapter, entityImages map[string]string, seenURLs map[string]bool) []ImageAssociation {
	candidates := s.imageCandidatesForChapter(topic, chapter, entityImages)
	target := imageAssociationsTargetCount(chapter)
	if target < 1 {
		target = 1
	}
	produced := 0
	out := make([]ImageAssociation, 0, target)
	for _, candidate := range candidates {
		if produced >= target {
			break
		}
		rec, cached, trace, err := s.resolveImageForEntityWithTrace(ctx, topic, candidate.entity, candidate.query, chapter, chapter.Index+1)
		if err != nil || rec == nil || strings.TrimSpace(rec.ImageURL) == "" {
			continue
		}
		urlKey := strings.ToLower(strings.TrimSpace(rec.ImageURL))
		if urlKey == "" || seenURLs[urlKey] {
			continue
		}
		seenURLs[urlKey] = true
		out = append(out, ImageAssociation{
			Phrase:       compactSnippet(chapter.SourceText, 140),
			Entity:       candidate.entity,
			Query:        candidate.query,
			ImageURL:     rec.ImageURL,
			Source:       rec.Source,
			Title:        rec.Title,
			PageURL:      rec.PageURL,
			StartTime:    chapter.StartTime,
			EndTime:      chapter.EndTime,
			ChapterIndex: chapter.Index + 1,
			Score:        candidate.score,
			Cached:       cached,
			LocalPath:    rec.LocalPath,
			MimeType:     rec.MimeType,
			FileSize:     rec.FileSizeBytes,
			AssetHash:    rec.AssetHash,
			DownloadedAt: formatTime(rec.DownloadedAt),
			Resolution:   trace,
		})
		produced++
	}
	return out
}

func imageAssociationsTargetCount(chapter ScriptChapter) int {
	duration := chapter.EndTime - chapter.StartTime
	if duration <= 0 {
		duration = int(chapter.Confidence * 10)
	}
	if duration <= 0 {
		duration = 12
	}
	// Aim for one visual every ~6 seconds, with a minimum of 1.
	target := (duration + 5) / 6
	if target < 1 {
		target = 1
	}
	if target > 8 {
		target = 8
	}
	return target
}

func (s *ScriptDocService) imageCandidatesForTopic(topic string, entityImages map[string]string) []imageCandidate {
	var candidates []imageCandidate
	if strings.TrimSpace(topic) != "" {
		candidates = append(candidates, imageCandidate{
			entity: topic,
			query:  anchorImageQuery(topic, topic),
			score:  scoreImageCandidate(topic, topic, ScriptChapter{}, 1.05),
		})
	}
	for _, entity := range extractImageEntities(topic, entityImages, nil) {
		candidates = append(candidates, imageCandidate{
			entity: entity,
			query:  anchorImageQuery(topic, entity),
			score:  scoreImageCandidate(entity, topic, ScriptChapter{}, 0.55),
		})
	}
	return sortImageCandidates(dedupeImageCandidates(candidates))
}

func (s *ScriptDocService) imageCandidatesForChapter(topic string, chapter ScriptChapter, entityImages map[string]string) []imageCandidate {
	text := strings.ToLower(chapter.SourceText)
	title := strings.ToLower(chapter.Title)
	seen := make(map[string]bool)
	var candidates []imageCandidate

	addTopic := strings.TrimSpace(topic)
	if addTopic != "" {
		key := normalizeKeyword(addTopic)
		if key != "" {
			seen[key] = true
			candidates = append(candidates, imageCandidate{
				entity: addTopic,
				query:  anchorImageQuery(topic, addTopic),
				score:  scoreImageCandidate(addTopic, topic, chapter, 1.05),
			})
		}
	}

	add := func(entity string, query string, base float64) {
		entity = strings.TrimSpace(entity)
		if entity == "" || isWeakImageEntity(entity) {
			return
		}
		key := normalizeKeyword(entity)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		candidates = append(candidates, imageCandidate{
			entity: entity,
			query:  anchorImageQuery(topic, query),
			score:  scoreImageCandidate(entity, topic, chapter, base),
		})
	}

	for _, entity := range chapter.DominantEntities {
		add(entity, entity, 1.0)
	}

	keys := make([]string, 0, len(entityImages))
	for entity := range entityImages {
		keys = append(keys, entity)
	}
	sort.Strings(keys)
	for _, entity := range keys {
		if normalizeKeyword(entity) == "" {
			continue
		}
		lower := strings.ToLower(entity)
		if strings.Contains(text, lower) {
			add(entity, entity, 0.9)
		}
		if strings.Contains(title, lower) {
			add(entity, entity, 0.85)
		}
	}

	for _, entity := range extractImageEntities(chapter.SourceText, entityImages, chapter.DominantEntities) {
		add(entity, entity, 0.75)
	}

	for _, entity := range extractImageEntities(topic, entityImages, chapter.DominantEntities) {
		add(entity, topic, 0.55)
	}

	return sortImageCandidates(dedupeImageCandidates(candidates))
}

func isWeakImageEntity(entity string) bool {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return true
	}
	key := normalizeKeyword(entity)
	if key == "" {
		return true
	}
	if weakImageEntityTerms[key] {
		return true
	}
	if len(strings.Fields(entity)) == 1 {
		if !isTitleLikeEntity(entity) && len(key) < 6 {
			return true
		}
	}
	return false
}

func extractImageEntities(topic string, entityImages map[string]string, exclude []string) []string {
	seen := make(map[string]bool)
	for _, item := range exclude {
		seen[normalizeKeyword(item)] = true
	}

	allowAll := len(entityImages) == 0
	allowed := make(map[string]bool)
	for entity := range entityImages {
		allowed[normalizeKeyword(entity)] = true
	}

	var out []string
	add := func(entity string) {
		entity = strings.TrimSpace(entity)
		key := normalizeKeyword(entity)
		if key == "" || seen[key] {
			return
		}
		if isWeakImageEntity(entity) {
			return
		}
		if !allowAll && !allowed[key] {
			return
		}
		seen[key] = true
		out = append(out, entity)
	}

	for _, entity := range ExtractProperNouns([]string{topic}) {
		add(entity)
	}
	for _, entity := range ExtractMultiWordEntities([]string{topic}) {
		add(entity)
	}
	for _, entity := range ExtractKeywords(topic) {
		add(entity)
	}
	return out
}

func dedupeImageCandidates(items []imageCandidate) []imageCandidate {
	best := make(map[string]imageCandidate)
	for _, item := range items {
		key := normalizeKeyword(item.entity)
		if key == "" {
			continue
		}
		if current, ok := best[key]; !ok || item.score > current.score {
			best[key] = item
		}
	}
	out := make([]imageCandidate, 0, len(best))
	for _, item := range best {
		out = append(out, item)
	}
	return out
}

func sortImageCandidates(items []imageCandidate) []imageCandidate {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score == items[j].score {
			if len(items[i].entity) == len(items[j].entity) {
				return items[i].entity < items[j].entity
			}
			return len(items[i].entity) > len(items[j].entity)
		}
		return items[i].score > items[j].score
	})
	return items
}

func (s *ScriptDocService) resolveImageForEntity(ctx context.Context, topic, entity, query string, chapter ScriptChapter, chapterIndex int) (*imagesdb.ImageRecord, bool, error) {
	rec, cached, _, err := s.resolveImageForEntityWithTrace(ctx, topic, entity, query, chapter, chapterIndex)
	return rec, cached, err
}

func (s *ScriptDocService) resolveImageForEntityWithTrace(ctx context.Context, topic, entity, query string, chapter ScriptChapter, chapterIndex int) (*imagesdb.ImageRecord, bool, *AssetResolution, error) {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return nil, false, nil, nil
	}
	if strings.TrimSpace(query) == "" {
		query = entity
	}
	trace := newAssetResolution("images", "imagesdb-cache", "entityimages", "download").withOutcome("", "image entity resolution", false)
	trace.RequestKey = normalizeKeyword(entity) + "|" + normalizeKeyword(query)

	if s.imagesDB != nil {
		if rec, ok := s.imagesDB.Get(entity); ok && strings.TrimSpace(rec.ImageURL) != "" {
			rec.VideoID = topic
			rec.ChapterIndex = chapterIndex
			rec.Query = query
			rec.UsedCount++
			if rec.LocalPath == "" || !fileExists(rec.LocalPath) {
				if s.imageDownloader != nil {
					downloaded, err := s.imageDownloader.Download(ctx, *rec)
					if err == nil && downloaded != nil {
						rec.LocalPath = downloaded.LocalPath
						rec.MimeType = downloaded.MimeType
						rec.FileSizeBytes = downloaded.FileSize
						rec.AssetHash = downloaded.AssetHash
						rec.DownloadedAt = downloaded.DownloadedAt
					}
				}
			}
			_ = s.imagesDB.Touch(*rec)
			trace.withOutcome("imagesdb-cache", "cached image record reused", true)
			return rec, true, trace, nil
		}
	}

	finder := s.imageFinder
	if finder == nil {
		return nil, false, trace, fmt.Errorf("image finder not configured")
	}

	candidates := []string{query, entity}
	if strings.TrimSpace(topic) != "" && normalizeKeyword(topic) != normalizeKeyword(entity) {
		candidates = append(candidates, entity+" "+topic, topic+" "+entity)
	}

	for _, candidateQuery := range candidates {
		imageURL := strings.TrimSpace(finder.Find(candidateQuery))
		if imageURL == "" {
			continue
		}
		rec := &imagesdb.ImageRecord{
			Entity:         entity,
			Query:          candidateQuery,
			Source:         "entityimages",
			Title:          entity,
			ImageURL:       imageURL,
			VideoID:        topic,
			ChapterIndex:   chapterIndex,
			RelevanceScore: scoreImageCandidate(entity, topic, chapter, 0),
		}
		if s.imageDownloader != nil {
			if downloaded, err := s.imageDownloader.Download(ctx, *rec); err == nil && downloaded != nil {
				rec.LocalPath = downloaded.LocalPath
				rec.MimeType = downloaded.MimeType
				rec.FileSizeBytes = downloaded.FileSize
				rec.AssetHash = downloaded.AssetHash
				rec.DownloadedAt = downloaded.DownloadedAt
			}
		}
		if s.imagesDB != nil {
			if err := s.imagesDB.Upsert(*rec); err != nil {
				return nil, false, trace, err
			}
		}
		trace.withOutcome("entityimages", "finder resolved a new image", false)
		return rec, false, trace, nil
	}

	trace.addNote("no image match found for entity")
	return nil, false, trace, nil
}

func scoreImageCandidate(entity, topic string, chapter ScriptChapter, base float64) float64 {
	ne := normalizeKeyword(entity)
	nt := normalizeKeyword(topic)
	score := base
	if isTitleLikeEntity(entity) {
		score += 0.12
	}
	if entity == strings.ToLower(entity) {
		score -= 0.04
	}
	if ne != "" && ne == nt {
		score += 0.55
	}
	if nt != "" && strings.Contains(strings.ToLower(entity), nt) {
		score += 0.25
	}
	if nt != "" {
		for _, tok := range significantTokens(topic) {
			if ne != "" && ne == tok && ne != nt {
				score -= 0.10
			}
		}
	}
	if strings.Contains(strings.ToLower(chapter.Title), strings.ToLower(entity)) {
		score += 0.15
	}
	if strings.Contains(strings.ToLower(chapter.SourceText), strings.ToLower(entity)) {
		score += 0.2
	}
	if nt != "" && strings.Contains(strings.ToLower(chapter.SourceText), nt) && strings.Contains(strings.ToLower(chapter.SourceText), strings.ToLower(entity)) {
		score += 0.12
	}
	if chapter.Confidence > 0 {
		score += chapter.Confidence / 10
	}
	if wc := len(strings.Fields(entity)); wc > 1 {
		score += 0.05 * float64(wc)
	}
	return score
}

func anchorImageQuery(topic, entity string) string {
	topic = strings.TrimSpace(topic)
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return topic
	}
	if topic == "" {
		return entity
	}
	if strings.EqualFold(topic, entity) || strings.Contains(strings.ToLower(entity), strings.ToLower(topic)) {
		return entity
	}
	return strings.TrimSpace(topic + " " + entity)
}

func isTitleLikeEntity(entity string) bool {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return false
	}
	r := []rune(entity)
	first := r[0]
	if first < 'A' || first > 'Z' {
		return false
	}
	return true
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (s *ScriptDocService) buildImagePlan(topic string, duration int, mode string, langResults []LanguageResult) *ImagePlan {
	plan := &ImagePlan{
		Topic:           topic,
		Duration:        duration,
		AssociationMode: normalizeAssociationMode(mode),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	for _, lr := range langResults {
		lplan := ImagePlanLang{
			Language:          lr.Language,
			Associations:      append([]ImageAssociation(nil), lr.ImageAssociations...),
			TotalAssociations: len(lr.ImageAssociations),
		}
		for _, ch := range lr.Chapters {
			lplan.Chapters = append(lplan.Chapters, ImagePlanChapter{
				Index:            ch.Index,
				Title:            ch.Title,
				StartTime:        ch.StartTime,
				EndTime:          ch.EndTime,
				Confidence:       ch.Confidence,
				SourceText:       compactSnippet(ch.SourceText, 260),
				DominantEntities: append([]string(nil), ch.DominantEntities...),
			})
		}
		for _, assoc := range lr.ImageAssociations {
			if assoc.Cached {
				lplan.CachedAssociations++
				plan.TotalCached++
			}
			if strings.TrimSpace(assoc.LocalPath) != "" {
				lplan.Downloaded++
				plan.TotalDownloaded++
			}
		}
		plan.TotalAssociations += len(lr.ImageAssociations)
		plan.Languages = append(plan.Languages, lplan)
	}
	return plan
}

func saveImagePlanJSON(topic string, plan *ImagePlan) (string, error) {
	if plan == nil {
		return "", nil
	}
	dir := filepath.Join(os.TempDir(), "velox-image-plans")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	name := safeFileName(topic)
	path := filepath.Join(dir, fmt.Sprintf("%s_%d.json", name, time.Now().Unix()))
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func safeFileName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "image_plan"
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "image_plan"
	}
	return out
}
