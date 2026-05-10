package images

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"math/rand"

	"velox/go-master/pkg/models"
	imagesRepo "velox/go-master/internal/repository/images"
	"go.uber.org/zap"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type Service struct {
	repo          *imagesRepo.Repository
	client        *http.Client
	log           *zap.Logger
	dataDir       string
	imagesDir     string
	driveFolderID string
	mu            sync.Mutex
}

func NewService(repo *imagesRepo.Repository, driveSvc interface{}, log *zap.Logger, dataDir string, driveFolderID string) *Service {
	imagesDir := filepath.Join(dataDir, "images")
	os.MkdirAll(imagesDir, 0755)

	return &Service{
		repo:          repo,
		client:        &http.Client{Timeout: 30 * time.Second},
		log:           log,
		dataDir:       dataDir,
		imagesDir:     imagesDir,
		driveFolderID: driveFolderID,
	}
}

// Slugify crea uno slug URL-friendly
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	reg, _ := regexp.Compile("[^a-z0-9]+")
	s = reg.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// SearchAndDownload prova a cercare un'immagine nel DB locale, e se non trovata procede con Wikidata/Wikipedia (main) o DDG (fallback) e la scarica
func (s *Service) SearchAndDownload(subjectSlug, displayName, query, lang string) (*models.ImageAsset, error) {
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
	imgURL := s.searchWikipedia(finalQuery, lang)
	source := "wikipedia"

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
	asset, err := s.downloadAndIngest(slug, imgURL, source, finalQuery, description)
	
	return asset, err
}

func (s *Service) searchWikidata(query, lang string) (string, string, string) {
	apiURL := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbsearchentities&search=%s&language=%s&format=json&limit=1", url.QueryEscape(query), lang)
	
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

	return payload.Search[0].Label, payload.Search[0].ID, payload.Search[0].Description
}

func (s *Service) searchWikipedia(query, lang string) string {
	// Aggiungiamo un pizzico di contesto per evitare ambiguità
	searchQuery := query
	if !strings.Contains(strings.ToLower(query), "pizza") && !strings.Contains(strings.ToLower(query), "italia") {
		searchQuery = query + " " + lang
	}

	// Step 1: Search for the most relevant page
	searchURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&list=search&srsearch=%s&format=json&srlimit=1", lang, url.QueryEscape(searchQuery))
	
	req, _ := http.NewRequest("GET", searchURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Error("Wikipedia search request failed", zap.Error(err))
		return ""
	}
	defer resp.Body.Close()

	var searchPayload struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchPayload); err != nil {
		s.log.Error("Failed to decode Wikipedia search response", zap.Error(err))
		return ""
	}

	if len(searchPayload.Query.Search) == 0 {
		s.log.Warn("Wikipedia search returned no results", zap.String("query", searchQuery))
		return ""
	}

	bestTitle := searchPayload.Query.Search[0].Title
	s.log.Info("Wikipedia best match found", zap.String("title", bestTitle))

	// Step 2: Get thumbnail for the best match
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(bestTitle))
	req, _ = http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err = s.client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var payload struct {
		Query struct {
			Pages map[string]struct {
				Thumbnail struct {
					Source string `json:"source"`
				} `json:"thumbnail"`
			} `json:"pages"`
		} `json:"query"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}

	for _, page := range payload.Query.Pages {
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source
		}
	}
	return ""
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

func (s *Service) downloadAndIngest(slug, imgURL, source, query, description string) (*models.ImageAsset, error) {
	req, _ := http.NewRequest("GET", imgURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	return s.IngestImage(slug, resp.Body, filepath.Base(imgURL), imgURL, description)
}

func (s *Service) IngestImage(slug string, data io.Reader, filename, sourceURL, description string) (*models.ImageAsset, error) {
	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	// 1. Calcola Hash
	hasher := sha256.New()
	hasher.Write(content)
	hash := hex.EncodeToString(hasher.Sum(nil))

	// 2. Verifica se esiste già per Hash
	if existing, err := s.repo.GetImageByHash(hash); err == nil && existing != nil {
		s.log.Info("Image with this hash already exists", zap.String("hash", hash))
		return existing, nil
	}

	// 3. Trova Soggetto (o crealo)
	subject, err := s.repo.GetSubjectBySlugOrAlias(slug)
	if err != nil || subject == nil {
		subject = &models.Subject{
			Slug:        slug,
			DisplayName: slug,
		}
		_, err := s.repo.CreateSubject(subject)
		if err != nil {
			s.log.Warn("Ingest: subject might exist", zap.String("slug", slug))
		}
	}

	// 4. Prepara percorsi
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}
	relPath := filepath.Join(slug, hash+ext)
	fullPath := filepath.Join(s.imagesDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	// 5. Salva il file fisico
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}

	// 6. Crea record DB
	asset := &models.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relPath,
		SourceURL:    sourceURL,
		Description:  description,
		Status:       "ready",
		MetadataJSON: "{}",
	}

	if _, err := s.repo.AddImage(asset); err != nil {
		// Final safety check for UNIQUE constraint (IGNORE handled by INSERT OR IGNORE, but double check)
		if existing, exErr := s.repo.GetImageByHash(hash); exErr == nil && existing != nil {
			return existing, nil
		}
		return nil, fmt.Errorf("failed to add image to repository: %w", err)
	}

	return asset, nil
}

func (s *Service) SyncAssets() error {
	return nil
}

func (s *Service) SyncFromDrive(ctx context.Context) error {
	return nil
}
