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
	subject, err := s.repo.GetSubjectBySlugOrAlias(ctx, slug)
	if err == nil && subject != nil {
		if images, err := s.repo.ListImagesBySubject(ctx, subject.Slug); err == nil && len(images) > 0 {
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
		_, err := s.repo.CreateSubject(ctx, subject)
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
		s.log.Info("Wikipedia failed, falling back to DuckDuckGo (wide)", zap.String("query", query))
		imgURL = s.searchDDGWide(query)
		source = "duckduckgo"
	}

	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", query)
	}

	// 4. Scarica e Ingest (già chiama AddImage internamente)
	s.log.Info("Downloading image", zap.String("url", imgURL), zap.String("source", source))
	description := fmt.Sprintf("Image for %s found via %s", displayName, source)
	asset, err := s.downloadAndIngest(ctx, slug, imgURL, "", source, finalQuery, description, tags)
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
		// Aggiorna i metadati senza duplicare l'inserimento (già fatto da downloadAndIngest)
		_ = s.repo.UpdateImageMetadata(ctx, asset.Hash, string(metaJSON))
		asset.MetadataJSON = string(metaJSON)
	}

	return asset, err
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
	re := regexp.MustCompile(`vqd=['\"]([^'\"]+)['\"]`)
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
