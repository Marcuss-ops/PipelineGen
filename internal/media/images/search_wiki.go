package images

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"go.uber.org/zap"
)

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
