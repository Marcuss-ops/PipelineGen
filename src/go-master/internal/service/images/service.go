package images

import (
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

	"velox/go-master/internal/repository/images"
	"velox/go-master/pkg/models"

	"go.uber.org/zap"
)

type Service struct {
	repo    *images.Repository
	baseDir string
	log     *zap.Logger
	client  *http.Client
	
	// Gestione concorrenza per evitare doppi download
	mu           sync.Mutex
	activeSearch map[string]*sync.WaitGroup
}

func NewService(repo *images.Repository, baseDir string, log *zap.Logger) *Service {
	return &Service{
		repo:         repo,
		baseDir:      baseDir,
		log:          log,
		client:       &http.Client{Timeout: 15 * time.Second},
		activeSearch: make(map[string]*sync.WaitGroup),
	}
}

// Slugify trasforma una stringa in uno slug pulito
func Slugify(s string) string {
	s = strings.ToLower(s)
	reg := regexp.MustCompile("[^a-z0-9]+")
	s = reg.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// SearchAndDownload prova a cercare un'immagine su Wikidata/Wikipedia (main) o DDG (fallback) e la scarica
func (s *Service) SearchAndDownload(subjectSlug, displayName, query string) (*models.ImageAsset, error) {
	// Normalizziamo lo slug
	slug := Slugify(subjectSlug)
	if slug == "" {
		slug = Slugify(query)
	}

	// 0. Gestione concorrenza
	s.mu.Lock()
	if wg, exists := s.activeSearch[slug]; exists {
		s.mu.Unlock()
		wg.Wait()
		// Recupero post-attesa semplificato per brevità
	} else {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		s.activeSearch[slug] = wg
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.activeSearch, slug)
			s.mu.Unlock()
			wg.Done()
		}()
	}

	// 1. Disambiguazione con Wikidata
	s.log.Info("Disambiguating with Wikidata", zap.String("query", query))
	wikiTitle, qid, wikiDesc := s.searchWikidata(query)
	
	finalQuery := query
	if wikiTitle != "" {
		finalQuery = wikiTitle
	}

	// 2. Cerca URL Immagine
	s.log.Info("Searching for image", zap.String("query", finalQuery), zap.String("slug", slug))
	imgURL := s.searchWikipedia(finalQuery)
	source := "wikipedia"

	if imgURL == "" {
		imgURL = s.searchDDG(query)
		source = "duckduckgo"
	}

	if imgURL == "" {
		return nil, fmt.Errorf("no image found for query: %s", query)
	}

	// 3. Scarica e ingesta
	description := wikiDesc
	if description == "" {
		description = fmt.Sprintf("Auto-downloaded from %s for query: %s", source, query)
	}

	asset, err := s.downloadAndIngest(slug, imgURL, source, finalQuery, description)
	if err == nil && asset != nil && qid != "" {
		// Se abbiamo un QID, aggiorniamo il soggetto nel DB
		subject, _ := s.repo.GetSubjectBySlugOrAlias(slug)
		if subject != nil && subject.WikidataID == "" {
			subject.WikidataID = qid
			// Nota: andrebbe aggiunto un metodo UpdateSubject nel repository
		}
	}
	return asset, err
}

func (s *Service) searchWikidata(query string) (string, string, string) {
	apiURL := fmt.Sprintf("https://www.wikidata.org/w/api.php?action=wbsearchentities&search=%s&language=en&format=json&limit=1", url.QueryEscape(query))
	resp, err := s.client.Get(apiURL)
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

	qid := payload.Search[0].ID
	label := payload.Search[0].Label
	desc := payload.Search[0].Description

	return label, qid, desc
}

func (s *Service) downloadAndIngest(slug, imgURL, source, query, description string) (*models.ImageAsset, error) {
	s.log.Info("Downloading image", zap.String("url", imgURL), zap.String("source", source))
	req, _ := http.NewRequest("GET", imgURL, nil)
	req.Header.Set("User-Agent", "VeloxEditingBot/1.0 (contact: admin@veloxediting.com)")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	return s.IngestImage(slug, resp.Body, filepath.Base(imgURL), imgURL, description)
}

func (s *Service) searchWikipedia(query string) string {
	apiURL := fmt.Sprintf("https://en.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&pithumbsize=1000&format=json&redirects=1", url.QueryEscape(query))
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "VeloxEditingBot/1.0 (contact: admin@veloxediting.com)")

	resp, err := s.client.Do(req)
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

// SyncAssets scansiona la cartella assets e sincronizza il database
func (s *Service) SyncAssets() error {
	s.log.Info("Starting asset synchronization", zap.String("baseDir", s.baseDir))

	// 1. Scansione cartelle soggetti
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return fmt.Errorf("failed to read assets dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		s.log.Debug("Syncing subject", zap.String("slug", slug))

		// Assicuriamoci che il soggetto esista nel DB
		subject, err := s.repo.GetSubjectBySlugOrAlias(slug)
		if err != nil {
			s.log.Info("Registering missing subject found on disk", zap.String("slug", slug))
			id, err := s.repo.CreateSubject(&models.Subject{Slug: slug, DisplayName: slug})
			if err != nil {
				s.log.Error("Failed to register subject during sync", zap.String("slug", slug), zap.Error(err))
				continue
			}
			subject = &models.Subject{ID: id, Slug: slug}
		}

		// 2. Scansione immagini nel soggetto
		rawDir := filepath.Join(s.baseDir, slug, "raw")
		imgEntries, err := os.ReadDir(rawDir)
		if err != nil {
			continue
		}

		for _, imgEntry := range imgEntries {
			if imgEntry.IsDir() || filepath.Ext(imgEntry.Name()) == ".json" {
				continue
			}

			// L'hash è il nome del file senza estensione
			hash := strings.TrimSuffix(imgEntry.Name(), filepath.Ext(imgEntry.Name()))
			
			// Verifica se l'immagine è nel DB
			exists, _ := s.repo.GetImageByHash(hash)
			if exists != nil {
				continue
			}

			// Se non c'è, proviamo a caricarla dal sidecar o ricrearla
			s.log.Info("Indexing missing image found on disk", zap.String("slug", slug), zap.String("hash", hash))
			s.indexExistingFile(subject, rawDir, imgEntry.Name(), hash)
		}
	}

	return nil
}

func (s *Service) indexExistingFile(subject *models.Subject, dir, filename, hash string) {
	fullPath := filepath.Join(dir, filename)
	sidecarPath := fullPath + ".json"
	
	asset := &models.ImageAsset{
		Hash:      hash,
		SubjectID: subject.ID,
		PathRel:   filepath.Join(subject.Slug, "raw", filename),
	}

	// Prova a leggere il sidecar
	if data, err := os.ReadFile(sidecarPath); err == nil {
		json.Unmarshal(data, asset)
	}

	if asset.Description == "" {
		asset.Description = "Recovered during sync task"
	}

	_, err := s.repo.AddImage(asset)
	if err != nil {
		s.log.Error("Failed to index file during sync", zap.String("path", fullPath), zap.Error(err))
	}
}

func (s *Service) searchDDG(query string) string {
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_redirect=1", url.QueryEscape(query))
	resp, err := s.client.Get(apiURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var payload struct {
		Image string `json:"Image"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Image)
}

// IngestImage gestisce l'ingestione di un'immagine da un lettore (es: file scaricato)
func (s *Service) IngestImage(subjectSlug string, reader io.Reader, filename string, sourceURL string, description string) (*models.ImageAsset, error) {
	// 1. Assicura che il soggetto esista (cerchiamo anche per alias)
	slug := Slugify(subjectSlug)
	subject, err := s.repo.GetSubjectBySlugOrAlias(slug)
	if err != nil {
		// Se non esiste, lo creiamo
		newSub := &models.Subject{Slug: slug, DisplayName: subjectSlug}
		id, err := s.repo.CreateSubject(newSub)
		if err != nil {
			return nil, fmt.Errorf("failed to create subject %s: %w", slug, err)
		}
		newSub.ID = id
		subject = newSub
	}

	// 2. Leggi tutto in memoria per calcolare l'hash
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %w", err)
	}

	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// 3. Verifica se l'immagine esiste già nel DB
	existing, err := s.repo.GetImageByHash(hashStr)
	if err == nil && existing != nil {
		s.log.Info("Image already exists", zap.String("hash", hashStr))
		return existing, nil
	}

	// 4. Prepara le cartelle
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg" // Default
	}
	
	relPath := filepath.Join(subjectSlug, "raw", hashStr+ext)
	fullPath := filepath.Join(s.baseDir, relPath)
	
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	// 5. Salva il file fisico
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}

	// 6. Crea il sidecar JSON
	asset := &models.ImageAsset{
		Hash:        hashStr,
		SubjectID:   subject.ID,
		PathRel:     relPath,
		SourceURL:   sourceURL,
		Description: description,
		SizeBytes:   int64(len(data)),
	}

	sidecarPath := fullPath + ".json"
	sidecarData, _ := json.MarshalIndent(asset, "", "  ")
	os.WriteFile(sidecarPath, sidecarData, 0644)

	// 7. Salva nel DB
	id, err := s.repo.AddImage(asset)
	if err != nil {
		return nil, fmt.Errorf("failed to save image to db: %w", err)
	}
	asset.ID = id

	return asset, nil
}

// GetOrRegisterSubject recupera un soggetto o lo crea se non esiste
func (s *Service) GetOrRegisterSubject(slug, displayName string) (*models.Subject, error) {
	subject, err := s.repo.GetSubjectBySlugOrAlias(slug)
	if err == nil {
		return subject, nil
	}

	newSubject := &models.Subject{
		Slug:        slug,
		DisplayName: displayName,
		Category:    "general",
	}

	id, err := s.repo.CreateSubject(newSubject)
	if err != nil {
		return nil, err
	}
	newSubject.ID = id
	return newSubject, nil
}
