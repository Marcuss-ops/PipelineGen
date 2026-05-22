package images

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/ingest"
	"velox/go-master/internal/media/models"
	clipsRepo "velox/go-master/internal/repository/clips"
	imagesRepo "velox/go-master/internal/repository/images"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type Service struct {
	repo                *imagesRepo.Repository
	stockRepo           *clipsRepo.Repository
	client              *http.Client
	log                 *zap.Logger
	dataDir             string
	imagesDir           string
	animationsDir       string
	driveFolderID       string
	driveSvc            *driveapi.Service
	googleAccountingURL string
	googleAccountingDir string
	nvidiaAPIKey        string
	nvidiaModel         string
	scriptsDir          string
	tempDir             string
	ingestSvc           *ingest.Service
	mu                  sync.Mutex
}

// DiagnosticsReport summarizes the local wiring for image generation and animation.
type DiagnosticsReport struct {
	OK                bool   `json:"ok"`
	LocalNIMEndpoint  string `json:"local_nim_endpoint"`
	NvidiaModel       string `json:"nvidia_model"`
	NvidiaConfigured  bool   `json:"nvidia_configured"`
	AnimateScriptPath string `json:"animate_script_path"`
	AnimateScriptOK   bool   `json:"animate_script_ok"`
	ImagesDir         string `json:"images_dir"`
	AnimationsDir     string `json:"animations_dir"`
}

func NewService(cfg *config.Config, repo *imagesRepo.Repository, stockRepo *clipsRepo.Repository, driveSvc *driveapi.Service, log *zap.Logger) *Service {
	dataDir := cfg.Storage.DataDir
	imagesDir := filepath.Join(dataDir, cfg.Storage.ImagesDir)
	animationsDir := filepath.Join(dataDir, cfg.Storage.AnimationsDir)

	return &Service{
		repo:                repo,
		stockRepo:           stockRepo,
		client:              &http.Client{Timeout: 30 * time.Second},
		log:                 log,
		dataDir:             dataDir,
		imagesDir:           imagesDir,
		animationsDir:       animationsDir,
		driveFolderID:       cfg.Drive.ImagesRootFolder,
		driveSvc:            driveSvc,
		googleAccountingURL: strings.TrimSpace(cfg.GoogleAccounting.ServerURL),
		googleAccountingDir: strings.TrimSpace(cfg.GoogleAccounting.DownloadDir),
		nvidiaAPIKey:        cfg.External.NvidiaAPIKey,
		nvidiaModel:         cfg.External.NvidiaModel,
		scriptsDir:          cfg.Paths.PythonScriptsDir,
		tempDir:             cfg.Storage.TempPath(),
	}
}

func (s *Service) SetNvidiaConfig(apiKey, model string) {
	s.nvidiaAPIKey = apiKey
	if model != "" {
		s.nvidiaModel = model
	}
}

func (s *Service) SetScriptsDir(dir string) {
	s.scriptsDir = dir
}

// Diagnostics reports whether the local AI/video wiring is present.
func (s *Service) Diagnostics() DiagnosticsReport {
	scriptPath := filepath.Join(s.scriptsDir, "animate_image.py")
	_, scriptErr := os.Stat(scriptPath)

	return DiagnosticsReport{
		OK:                true,
		LocalNIMEndpoint:  "http://localhost:8000/v1/infer",
		NvidiaModel:       s.nvidiaModel,
		NvidiaConfigured:  s.nvidiaAPIKey != "" && s.nvidiaAPIKey != "PASTE_YOUR_NVIDIA_API_KEY_HERE",
		AnimateScriptPath: scriptPath,
		AnimateScriptOK:   scriptErr == nil,
		ImagesDir:         s.imagesDir,
		AnimationsDir:     s.animationsDir,
	}
}

func (s *Service) SetIngestService(svc *ingest.Service) {
	s.ingestSvc = svc
}

func (s *Service) SetGoogleAccountingConfig(serverURL, downloadDir string) {
	s.googleAccountingURL = strings.TrimSpace(serverURL)
	s.googleAccountingDir = strings.TrimSpace(downloadDir)
}

// Slugify crea uno slug URL-friendly
func Slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	reg, _ := regexp.Compile("[^a-z0-9]+")
	s = reg.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// SearchAndDownload prova a cercare un'immagine nel DB locale, e se non trovata procede con Wikidata/Wikipedia (main) o DDG (fallback) e la scarica
func (s *Service) SearchAndDownload(subjectSlug, displayName, query, lang string, tags []string) (*models.ImageAsset, error) {
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
	asset, err := s.downloadAndIngest(slug, imgURL, source, finalQuery, description, tags)
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

func (s *Service) searchWikipedia(query, lang string) (string, string) {
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
		return "", ""
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
		return "", ""
	}

	if len(searchPayload.Query.Search) == 0 {
		s.log.Warn("Wikipedia search returned no results", zap.String("query", searchQuery))
		return "", ""
	}

	bestTitle := searchPayload.Query.Search[0].Title
	s.log.Info("Wikipedia best match found", zap.String("title", bestTitle))

	// Step 2: Get thumbnail for the best match
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/w/api.php?action=query&prop=pageimages&titles=%s&pithumbsize=1000&format=json&redirects=1", lang, url.QueryEscape(bestTitle))
	req, _ = http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)

	resp, err = s.client.Do(req)
	if err != nil {
		return "", ""
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
		return "", ""
	}

	for _, page := range payload.Query.Pages {
		if page.Thumbnail.Source != "" {
			return page.Thumbnail.Source, bestTitle
		}
	}
	return "", ""
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

func (s *Service) downloadAndIngest(slug, imgURL, source, query, description string, tags []string) (*models.ImageAsset, error) {
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

	return s.IngestImage(slug, resp.Body, filepath.Base(imgURL), imgURL, description, tags)
}

func (s *Service) IngestImage(slug string, data io.Reader, filename, sourceURL, description string, tags []string) (*models.ImageAsset, error) {
	content, err := io.ReadAll(data)
	if err != nil {
		return nil, err
	}

	// Legacy dedup: SHA256 check per immagini già salvate col vecchio path
	hasher := sha256.New()
	hasher.Write(content)
	legacyHash := hex.EncodeToString(hasher.Sum(nil))

	if existing, err := s.repo.GetImageByHash(legacyHash); err == nil && existing != nil {
		s.log.Info("Image with this hash already exists (legacy SHA256)", zap.String("hash", legacyHash))
		return existing, nil
	}

	// Se l'ingest pipeline è disponibile, usala
	if s.ingestSvc != nil {
		return s.ingestViaPipeline(slug, content, filename, sourceURL, description, tags)
	}

	// Fallback: path diretto legacy
	return s.ingestDirect(slug, content, filename, sourceURL, description, tags, legacyHash)
}

func (s *Service) ingestViaPipeline(slug string, content []byte, filename, sourceURL, description string, tags []string) (*models.ImageAsset, error) {
	tmpDir, err := os.MkdirTemp(s.tempDir, "image-ingest-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, filename)
	if err := os.WriteFile(tmpPath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	result, err := s.ingestSvc.Ingest(context.Background(), &ingest.Request{
		Kind:      string(ingest.KindImage),
		LocalPath: tmpPath,
		Name:      description,
		Group:     slug,
		Source:    "image",
		SourceID:  sourceURL,
		Tags:      tags,
	})
	if err != nil {
		return nil, err
	}

	asset, err := s.repo.GetImageByHash(result.FileHash)
	if err != nil {
		asset, err = s.repo.GetImageByHash(result.ContentHash)
		if err != nil {
			return nil, fmt.Errorf("image not found after ingest: %w", err)
		}
	}
	return asset, nil
}

func (s *Service) ingestDirect(slug string, content []byte, filename, sourceURL, description string, tags []string, hash string) (*models.ImageAsset, error) {
	// 1. Trova Soggetto (o crealo)
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

	// 2. Prepara percorsi
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".jpg"
	}
	relPath := filepath.Join(slug, hash+ext)
	fullPath := filepath.Join(s.imagesDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	// 3. Salva il file fisico
	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write image file: %w", err)
	}

	// 4. Upload to Drive if configured
	var driveFileID string
	if s.driveSvc != nil && s.driveFolderID != "" {
		s.log.Info("Uploading image to Google Drive", zap.String("filename", filename), zap.String("folder_id", s.driveFolderID))

		driveFile := &driveapi.File{
			Name:    filename,
			Parents: []string{s.driveFolderID},
		}

		res, err := s.driveSvc.Files.Create(driveFile).
			Media(strings.NewReader(string(content))).
			Fields("id").
			Do()

		if err != nil {
			s.log.Error("Drive upload failed", zap.Error(err))
		} else {
			driveFileID = res.Id
			s.log.Info("Drive upload successful", zap.String("file_id", driveFileID))
		}
	}

	// 5. Crea record DB
	asset := &models.ImageAsset{
		SubjectID:    slug,
		Hash:         hash,
		PathRel:      relPath,
		SourceURL:    sourceURL,
		Description:  description,
		DriveFileID:  driveFileID,
		Status:       "ready",
		MetadataJSON: "{}",
		Tags:         tags,
	}

	if _, err := s.repo.AddImage(asset); err != nil {
		// Final safety check for UNIQUE constraint
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
	if s.driveSvc == nil || s.driveFolderID == "" {
		return fmt.Errorf("drive service or folder ID not configured")
	}

	s.log.Info("Starting images sync from Drive", zap.String("folder_id", s.driveFolderID))
	return s.syncFolderRecursive(ctx, s.driveFolderID, "")
}

func (s *Service) syncFolderRecursive(ctx context.Context, folderID, folderPath string) error {
	query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	fl, err := s.driveSvc.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, webContentLink)").
		PageSize(1000).
		Context(ctx).
		Do()
	if err != nil {
		return err
	}

	for _, file := range fl.Files {
		if file.MimeType == "application/vnd.google-apps.folder" {
			newPath := filepath.Join(folderPath, file.Name)
			if err := s.syncFolderRecursive(ctx, file.Id, newPath); err != nil {
				s.log.Warn("failed to sync subfolder", zap.String("id", file.Id), zap.Error(err))
			}
			continue
		}

		// Skip non-image files (basic check)
		lowerName := strings.ToLower(file.Name)
		if !strings.HasSuffix(lowerName, ".jpg") && !strings.HasSuffix(lowerName, ".jpeg") &&
			!strings.HasSuffix(lowerName, ".png") && !strings.HasSuffix(lowerName, ".webp") {
			continue
		}

		// Check if already exists by Drive ID
		existing, err := s.repo.GetByDriveFileID(ctx, file.Id)
		if err == nil && existing != nil {
			continue
		}

		// Create metadata-only record
		// Note: We don't have the hash yet, so we use a placeholder or the Drive ID
		// IngestImage would be better but it downloads the file.
		// For population, we just want the record.

		asset := &models.ImageAsset{
			SubjectID:    Slugify(file.Name),
			Hash:         "drive_" + file.Id, // Placeholder hash
			PathRel:      "",                 // No local path yet
			SourceURL:    file.WebViewLink,
			Description:  "Synced from Drive: " + file.Name,
			DriveFileID:  file.Id,
			Status:       "ready",
			MetadataJSON: "{}",
		}

		if _, err := s.repo.AddImage(asset); err != nil {
			s.log.Warn("failed to add synced image", zap.String("name", file.Name), zap.Error(err))
		}
	}

	return nil
}

func (s *Service) GenerateAImage(prompt, model string, width, height int, tags []string) (*models.ImageAsset, error) {
	var invokeURL string
	var payload map[string]interface{}
	var useCloudAuth bool
	var sourceLabel string
	resolvedModel := strings.TrimSpace(model)

	// Default resolution if not provided
	if width <= 0 {
		width = 1024
	}
	if height <= 0 {
		height = 1024
	}

	if resolvedModel == "" {
		if s.nvidiaAPIKey != "" && s.nvidiaAPIKey != "PASTE_YOUR_NVIDIA_API_KEY_HERE" {
			resolvedModel = "flux-1-dev"
		} else {
			resolvedModel = "local-nim"
		}
	}

	switch resolvedModel {
	case "flux-1-dev":
		invokeURL = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.1-dev"
		payload = map[string]interface{}{
			"prompt":    prompt,
			"mode":      "base",
			"cfg_scale": 3.5,
			"width":     width,
			"height":    height,
			"seed":      0,
			"steps":     50,
		}
		useCloudAuth = true
		sourceLabel = "flux-1-dev"

	case "flux-2-klein":
		invokeURL = "https://ai.api.nvidia.com/v1/genai/black-forest-labs/flux.2-klein-4b"
		payload = map[string]interface{}{
			"prompt": prompt,
			"width":  width,
			"height": height,
			"seed":   0,
			"steps":  4,
		}
		useCloudAuth = true
		sourceLabel = "flux-2-klein"

	case "local-nim", "":
		invokeURL = "http://localhost:8000/v1/infer"
		payload = map[string]interface{}{
			"prompt": prompt,
			"mode":   "base",
			"seed":   0,
			"steps":  30,
		}
		useCloudAuth = false
		sourceLabel = "nvidia-local"
		resolvedModel = "local-nim"

	default:
		return nil, fmt.Errorf("unsupported model: %s", model)
	}

	jsonPayload, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", invokeURL, strings.NewReader(string(jsonPayload)))
	if err != nil {
		return nil, err
	}

	if useCloudAuth {
		if s.nvidiaAPIKey == "" || s.nvidiaAPIKey == "PASTE_YOUR_NVIDIA_API_KEY_HERE" {
			return nil, fmt.Errorf("NVIDIA API key not configured (required for cloud models)")
		}
		req.Header.Set("Authorization", "Bearer "+s.nvidiaAPIKey)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s error (status %d): %s", resolvedModel, resp.StatusCode, string(body))
	}

	var responseBody struct {
		Image     string `json:"image"`
		Artifacts []struct {
			Base64 string `json:"base64"`
		} `json:"artifacts"`
	}

	if err := json.Unmarshal(body, &responseBody); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var base64Data string
	if responseBody.Image != "" {
		base64Data = responseBody.Image
	} else if len(responseBody.Artifacts) > 0 {
		base64Data = responseBody.Artifacts[0].Base64
	}

	if base64Data == "" {
		return nil, fmt.Errorf("no image data found in response")
	}

	// Decode base64
	imageData, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 image: %w", err)
	}

	// Ingest image
	slug := Slugify(prompt)
	if len(slug) > 50 {
		slug = slug[:50]
	}
	filename := fmt.Sprintf("%s_%d.png", sourceLabel, time.Now().Unix())
	description := fmt.Sprintf("AI generated image via %s for prompt: %s", resolvedModel, prompt)

	return s.IngestImage(slug, strings.NewReader(string(imageData)), filename, sourceLabel, description, tags)
}

func (s *Service) AnimateImage(ctx context.Context, imageHash string, duration int) (string, error) {
	// 1. Get image from repo
	asset, err := s.repo.GetImageByHash(imageHash)
	if err != nil {
		return "", fmt.Errorf("image not found: %w", err)
	}

	fullPath := filepath.Join(s.imagesDir, asset.PathRel)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return "", fmt.Errorf("local file not found: %s", fullPath)
	}

	// 2. Prepare output path
	outputName := fmt.Sprintf("animate_%s.mp4", imageHash)
	outputPath := filepath.Join(s.animationsDir, outputName)

	// 3. Run script
	scriptPath := filepath.Join(s.scriptsDir, "animate_image.py")
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		// Fallback for development if scripts is in current dir
		scriptPath = "scripts/animate_image.py"
	}

	durStr := fmt.Sprintf("%d", duration)
	if duration <= 0 {
		durStr = "7"
	}

	cmd := exec.CommandContext(ctx, "python3", scriptPath, fullPath, "--output", outputPath, "--duration", durStr)
	output, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("Animation script failed", zap.Error(err), zap.String("output", string(output)))
		return "", fmt.Errorf("animation failed: %w", err)
	}

	s.log.Info("Animation created", zap.String("path", outputPath))

	// 4. Se l'ingest pipeline è disponibile, usala per stock clip
	if s.ingestSvc != nil {
		_, err := s.ingestSvc.Ingest(ctx, &ingest.Request{
			Kind:      string(ingest.KindStock),
			LocalPath: outputPath,
			Name:      "AI Animation: " + asset.SubjectID,
			Filename:  outputName,
			Group:     "ai-generated",
			Source:    "nvidia-animation",
			SourceID:  imageHash,
			Duration:  duration,
		})
		if err != nil {
			s.log.Warn("Ingest pipeline failed for animated clip", zap.Error(err))
		}
		return outputPath, nil
	}

	// 5. Fallback: upload manuale a Drive
	var driveVideoID string
	if s.driveSvc != nil && s.driveFolderID != "" {
		s.log.Info("Uploading animated video to Google Drive", zap.String("filename", outputName))

		videoFile, err := os.Open(outputPath)
		if err == nil {
			driveFile := &driveapi.File{
				Name:    outputName,
				Parents: []string{s.driveFolderID},
			}

			res, err := s.driveSvc.Files.Create(driveFile).
				Media(videoFile).
				Fields("id").
				Do()

			videoFile.Close()

			if err != nil {
				s.log.Error("Drive video upload failed", zap.Error(err))
			} else {
				driveVideoID = res.Id
				s.log.Info("Drive video upload successful", zap.String("file_id", driveVideoID))
			}
		}
	}

	// 6. Salva nel DB stock (fallback)
	if s.stockRepo != nil {
		clip := &models.MediaAsset{
			ID:          "ai_" + imageHash,
			Name:        "AI Animation: " + asset.SubjectID,
			Filename:    outputName,
			Group:       "ai-generated",
			MediaType:   "video",
			DriveFileID: driveVideoID,
			LocalPath:   outputPath,
			Duration:    duration,
			Source:      "nvidia-animation",
			Status:      "ready",
			CreatedAt:   time.Now(),
		}

		if err := s.stockRepo.UpsertClip(ctx, clip); err != nil {
			s.log.Warn("Failed to ingest animated clip into stock DB", zap.Error(err))
		} else {
			s.log.Info("Animated clip ingested into stock DB", zap.String("clip_id", clip.ID))
		}
	}

	return outputPath, nil
}
