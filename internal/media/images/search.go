package images

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
)
func (s *Service) SearchAndDownload(ctx context.Context, subjectSlug, displayName, query, lang string, tags []string) (*models.ImageAsset, error) {
	// Normalizziamo lo slug
	slug := Slugify(subjectSlug)
	if slug == "" {
		slug = Slugify(query)
	}

	// Default to 'it'
	if lang == "" {
		lang = "it"
	}

	// Filtro per evitare termini inutili o segnaposto dell'LLM
	qLower := strings.ToLower(query)
	if qLower == "name" || qLower == "titolo" || len(query) < 2 {
		return nil, fmt.Errorf("invalid query term: %s", query)
	}

	// 1. Cerca nel DB locale
	subject, err := s.repo.GetSubjectBySlugOrAlias(slug)
	if err == nil && subject != nil {
		if images, err := s.repo.ListImagesBySubject(subject.Slug); err == nil && len(images) > 0 {
			s.log.Info("Images found in local database", zap.String("subject", subject.Slug), zap.Int("count", len(images)))

			// SCELTA CASUALE: Se abbiamo più immagini, ne prendiamo una a caso
			if len(images) > 1 {
				source := rand.New(rand.NewSource(time.Now().UnixNano()))
				randomIndex := source.Intn(len(images))
				s.log.Info("Picking random image from database", zap.Int("index", randomIndex), zap.Int("total", len(images)))
				return &images[randomIndex], nil
			}

			return &images[0], nil
		}
	}

	// Se il soggetto non esiste, creiamolo
	if subject == nil {
		subject = &models.Subject{
			Slug:        slug,
			DisplayName: displayName,
		}
		_, err := s.repo.CreateSubject(subject)
		if err != nil {
			s.log.Warn("Ingest: subject might already exist", zap.String("slug", slug))
		}
	}

	// Lock per evitare download duplicati dello stesso soggetto
	s.mu.Lock()
	defer s.mu.Unlock()

	// 2. Disambiguazione con Wikidata
	s.log.Info("Disambiguating with Wikidata", zap.String("query", query), zap.String("lang", lang))
	wikiTitle, qid, _ := s.searchWikidata(query, lang)

	finalQuery := query
	if wikiTitle != "" {
		finalQuery = wikiTitle
		s.log.Info("Wikidata disambiguation successful", zap.String("original", query), zap.String("resolved", finalQuery), zap.String("qid", qid))
	} else {
		s.log.Warn("Wikidata disambiguation found nothing", zap.String("query", query))
	}

	// 3. Cerca URL Immagine
	s.log.Info("Searching for image on Wikipedia", zap.String("query", finalQuery), zap.String("lang", lang))
	imgURL, wikiTitle := s.searchWikipedia(finalQuery, lang)
	source := "wikipedia"
	wikiURL := ""
	if wikiTitle != "" {
		wikiURL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, strings.ReplaceAll(wikiTitle, " ", "_"))
	}

	if imgURL == "" {
		s.log.Info("Wikipedia failed, falling back to DuckDuckGo", zap.String("query", query))
		imgURL = s.searchDDG(query)
		source = "duckduckgo"
	}

	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", query)
	}

	// 4. Scarica e Ingest
	s.log.Info("Downloading image", zap.String("url", imgURL), zap.String("source", source))
	description := fmt.Sprintf("Image for %s found via %s", displayName, source)
	asset, err := s.downloadAndIngest(ctx, slug, imgURL, source, finalQuery, description, tags)
	if err == nil && asset != nil {
		meta := make(map[string]any)
		if asset.MetadataJSON != "" && asset.MetadataJSON != "{}" {
			_ = json.Unmarshal([]byte(asset.MetadataJSON), &meta)
		}
		meta["source_image_url"] = imgURL
		if wikiURL != "" {
			meta["source_page_url"] = wikiURL
		}
		meta["source_name"] = source
		meta["source_query"] = finalQuery
		metaJSON, _ := json.Marshal(meta)
		asset.MetadataJSON = string(metaJSON)
		_, _ = s.repo.AddImage(asset)
	}

	return asset, err
}

func (s *Service) searchWikidata(query, lang string) (string, string, string) {
	apiURL := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbsearchentities&search=%s&language=%s&format=json&limit=10", url.QueryEscape(query), lang)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", "", ""
	}
	defer resp.Body.Close()

	var payload struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"search"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || len(payload.Search) == 0 {
		return "", "", ""
	}

	bestLabel, bestID, bestDescription := selectBestWikidataHit(query, payload.Search)
	if bestID == "" {
		return "", "", ""
	}
	return bestLabel, bestID, bestDescription
}

func (s *Service) searchWikipedia(query, lang string) (string, string) {
	if imgURL, wikiTitle := s.wikipediaThumbnailByExactTitle(query, lang); imgURL != "" {
		return imgURL, wikiTitle
	}

	searchQueries := []string{strings.TrimSpace(query)}
	if !looksLikeProperName(query) && !strings.Contains(strings.ToLower(query), "pizza") && !strings.Contains(strings.ToLower(query), "italia") {
		searchQueries = append(searchQueries, strings.TrimSpace(query+" "+lang))
	}

	bestTitle := ""
	for _, searchQuery := range searchQueries {
		if searchQuery == "" {
			continue
		}

		searchURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=5", lang, url.QueryEscape(searchQuery))
		req, _ := http.NewRequest("GET", searchURL, nil)
		req.Header.Set("User-Agent", userAgent)

		resp, err := s.client.Do(req)
		if err != nil {
			s.log.Error("Wikipedia search request failed", zap.Error(err))
			continue
		}

		var searchPayload struct {
			Query struct {
				Search []struct {
					Title string `json:"title"`
				} `json:"search"`
			} `json:"query"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&searchPayload); err != nil {
			resp.Body.Close()
			s.log.Error("Failed to decode Wikipedia search response", zap.Error(err))
			continue
		}
		resp.Body.Close()

		bestTitle = selectBestWikiTitle(query, searchPayload.Query.Search)
		if bestTitle != "" {
			s.log.Info("Wikipedia best match found", zap.String("title", bestTitle), zap.String("query", searchQuery))
			break
		}
	}

	if bestTitle == "" {
		s.log.Warn("Wikipedia search returned no results", zap.String("query", query))
		return "", ""
	}

	// Step 2: Get thumbnail for the best match
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(bestTitle))
	req2, _ := http.NewRequest("GET", apiURL, nil)
	req2.Header.Set("User-Agent", userAgent)

	resp2, err := s.client.Do(req2)
	if err != nil {
		return "", ""
	}
	defer resp2.Body.Close()

	var payload2 struct {
		Query struct {
			Pages map[string]struct {
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}

	if err := json.NewDecoder(resp2.Body).Decode(&payload2); err != nil {
		return "", ""
	}

	for _, page := range payload2.Query.Pages {
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source, bestTitle
		}
	}
	return "", ""
}

func (s *Service) wikipediaThumbnailByExactTitle(title, lang string) (string, string) {
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(title))

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	var payload struct {
		Query struct {
			Pages map[string]struct {
				Title     string `json:"title"`
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", ""
	}

	for _, page := range payload.Query.Pages {
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source, page.Title
		}
	}
	return "", ""
}

func normalizeLookupTerm(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("’", "'", "‘", "'", "´", "'", "`", "'", "´", "'").Replace(value)
	value = strings.ToLower(value)
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func looksLikeProperName(query string) bool {
	query = strings.TrimSpace(strings.NewReplacer("’", "'", "‘", "'").Replace(query))
	if query == "" {
		return false
	}

	parts := strings.Fields(query)
	if len(parts) == 0 || len(parts) > 5 {
		return false
	}

	capitalized := 0
	for _, part := range parts {
		part = strings.Trim(part, `"'.,;:!?()[]{}<>`)
		if part == "" {
			continue
		}
		r, _ := utf8.DecodeRuneInString(part)
		if unicode.IsUpper(r) {
			capitalized++
		}
	}

	if len(parts) == 1 {
		return capitalized == 1 && len(parts[0]) >= 4
	}

	return capitalized >= 1 || strings.ContainsAny(query, "'’")
}

func selectBestWikidataHit(query string, hits []struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}) (string, string, string) {
	bestScore := 0
	bestLabel := ""
	bestID := ""
	bestDescription := ""
	for _, hit := range hits {
		score := scoreWikiCandidate(query, hit.Label)
		if score > bestScore {
			bestScore = score
			bestLabel = hit.Label
			bestID = hit.ID
			bestDescription = hit.Description
		}
	}
	if bestScore < minWikiScore(query) {
		return "", "", ""
	}
	return bestLabel, bestID, bestDescription
}

func selectBestWikiTitle(query string, hits []struct {
	Title string `json:"title"`
}) string {
	bestScore := 0
	bestTitle := ""
	for _, hit := range hits {
		score := scoreWikiCandidate(query, hit.Title)
		if score > bestScore {
			bestScore = score
			bestTitle = hit.Title
		}
	}
	if bestScore < minWikiScore(query) {
		return ""
	}
	return bestTitle
}

func minWikiScore(query string) int {
	if looksLikeProperName(query) {
		return 80
	}
	return 50
}

func scoreWikiCandidate(query, candidate string) int {
	qTokens := meaningfulLookupTokens(query)
	cTokens := meaningfulLookupTokens(candidate)
	if len(qTokens) == 0 || len(cTokens) == 0 {
		return 0
	}

	qNorm := strings.Join(qTokens, " ")
	cNorm := strings.Join(cTokens, " ")
	if qNorm == cNorm {
		return 100
	}

	if strings.HasPrefix(cNorm, qNorm) || strings.HasPrefix(qNorm, cNorm) {
		return 95
	}

	cTokenSet := make(map[string]struct{}, len(cTokens))
	for _, token := range cTokens {
		cTokenSet[token] = struct{}{}
	}

	matched := 0
	for _, token := range qTokens {
		if _, ok := cTokenSet[token]; ok {
			matched++
		}
	}

	if matched == 0 {
		return 0
	}

	score := matched * 20
	if len(qTokens) == 1 {
		if matched == 1 {
			score += 25
		}
		return score
	}

	if matched == len(qTokens) {
		score += 40
	}
	if qTokens[0] == cTokens[0] {
		score += 10
	}
	return score
}

func meaningfulLookupTokens(value string) []string {
	value = normalizeLookupTerm(value)
	if value == "" {
		return nil
	}

	stopwords := map[string]struct{}{
		"d": {}, "de": {}, "di": {}, "da": {}, "del": {}, "della": {}, "dello": {}, "degli": {}, "delle": {},
		"of": {}, "the": {}, "and": {}, "la": {}, "le": {}, "el": {}, "los": {}, "las": {}, "von": {}, "van": {},
	}

	parts := strings.Fields(value)
	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		if _, ok := stopwords[part]; ok {
			continue
		}
		tokens = append(tokens, part)
	}
	return tokens
}

func (s *Service) searchDDG(query string) string {
	// Simple implementation for DuckDuckGo image search
	vqdURL := fmt.Sprintf("https://duckduckgo.com/?q=%s", url.QueryEscape(query))
	req, _ := http.NewRequest("GET", vqdURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return ""
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Robust regex for vqd
	re := regexp.MustCompile(`vqd=['"]([^'"]+)['"]`)
	matches := re.FindStringSubmatch(string(body))
	if len(matches) < 2 {
		return ""
	}
	vqd := matches[1]

	// Fetch images
	apiURL := fmt.Sprintf("https://duckduckgo.com/i.js?l=it-it&o=json&q=%s&vqd=%s&f=,,,&p=1", url.QueryEscape(query), vqd)
	req, _ = http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err = s.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var payload struct {
		Results []struct {
			Image string `json:"image"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || len(payload.Results) == 0 {
		return ""
	}

	return payload.Results[0].Image
}
