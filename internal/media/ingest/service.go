package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/pkg/hashutil"
	"velox/go-master/internal/upload/drive"
)

type Pipeline struct {
	Kind          Kind
	DefaultSource string
	RootFolderID  string
	RootFolder    func(*Request) string
	Lifecycle     *lifecycle.Service
}

type Service struct {
	cfg       *config.Config
	log       *zap.Logger
	client    *http.Client
	driveUp   *drive.Uploader
	pipelines map[Kind]*Pipeline
	imagesDir string
	tempDir   string
}

func NewService(cfg *config.Config, log *zap.Logger, driveSvc *gdrive.Service, pipelines map[Kind]*Pipeline) *Service {
	return &Service{
		cfg:       cfg,
		log:       log,
		client:    &http.Client{Timeout: 90 * time.Second},
		driveUp:   &drive.Uploader{Service: driveSvc, Log: log},
		pipelines: pipelines,
		imagesDir: cfg.Storage.ImagesPath(),
		tempDir:   cfg.Storage.TempPath(),
	}
}

func (s *Service) Ingest(ctx context.Context, req *Request) (*Result, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	kind := normalizeKind(req.Kind)
	if kind == "" {
		return nil, fmt.Errorf("kind is required")
	}

	pipeline := s.pipelines[kind]
	if pipeline == nil || pipeline.Lifecycle == nil {
		return nil, fmt.Errorf("ingest pipeline not configured for kind: %s", kind)
	}

	localPath, filename, cleanup, err := s.acquireLocalPath(ctx, kind, req)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	if kind == KindImage {
		localPath, filename, cleanup, err = s.materializeImage(localPath, filename, req)
		if err != nil {
			return nil, err
		}
		if cleanup != nil {
			defer cleanup()
		}
	}

	fileHash, err := hashutil.MD5File(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to hash media file: %w", err)
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = pipeline.DefaultSource
	}
	if source == "" {
		source = string(kind)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.Filename)
	}
	if name == "" {
		name = strings.TrimSpace(filename)
	}
	if name == "" {
		name = strings.TrimSpace(req.SourceID)
	}
	if name == "" {
		name = strings.TrimSpace(fileHash)
	}

	if filename == "" {
		filename = strings.TrimSpace(req.Filename)
	}
	if filename == "" {
		filename = filepath.Base(localPath)
	}
	if filename == "" {
		filename = name
	}

	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(req.URL)
	}
	if sourceID == "" {
		sourceID = strings.TrimSpace(req.LocalPath)
	}
	if sourceID == "" {
		sourceID = fileHash
	}

	rootFolderID := pipeline.RootFolderID
	if pipeline.RootFolder != nil {
		rootFolderID = pipeline.RootFolder(req)
	}
	resolvedFolderID, resolvedFolderPath, err := s.resolveDriveFolder(ctx, kind, rootFolderID, req)
	if err != nil {
		return nil, err
	}

	metadata := mergeMetadata(req.Metadata, map[string]any{
		"kind":          string(kind),
		"source":        source,
		"source_id":     sourceID,
		"filename":      filename,
		"local_path":    localPath,
		"folder_id":     resolvedFolderID,
		"folder_path":   resolvedFolderPath,
		"file_hash":     fileHash,
		"content_hash":  fileHash,
		"source_url":    req.URL,
		"drive_link":    req.DriveLink,
		"drive_file_id": req.DriveFileID,
		"download_link": req.DownloadLink,
		"tags":          req.Tags,
	})
	metaJSON, _ := json.Marshal(metadata)

	id := buildAssetID(kind, fileHash)
	input := &lifecycle.FinalizeInput{
		ID:           id,
		Name:         name,
		Filename:     filename,
		Kind:         toAssetKind(kind),
		Source:       source,
		SourceID:     sourceID,
		Group:        strings.TrimSpace(req.Group),
		Subfolder:    strings.TrimSpace(req.Subfolder),
		LocalPath:    localPath,
		FolderID:     resolvedFolderID,
		FolderPath:   resolvedFolderPath,
		DriveLink:    strings.TrimSpace(req.DriveLink),
		DriveFileID:  strings.TrimSpace(req.DriveFileID),
		DownloadLink: strings.TrimSpace(req.DownloadLink),
		FileHash:     fileHash,
		Metadata:     string(metaJSON),
		Duration:     req.Duration,
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: resolvedFolderID != "",
		VerifyDB:     true,
	}

	result, err := pipeline.Lifecycle.ProcessAsset(ctx, input, fileHash)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("empty ingest result")
	}
	if !result.OK {
		return &Result{
			OK:           false,
			Status:       result.Status,
			Kind:         string(kind),
			ID:           id,
			Source:       source,
			SourceID:     sourceID,
			Name:         name,
			Filename:     filename,
			FolderID:     resolvedFolderID,
			FolderPath:   resolvedFolderPath,
			LocalPath:    localPath,
			DriveLink:    result.DriveLink,
			DriveFileID:  result.DriveFileID,
			DownloadLink: result.DownloadLink,
			FileHash:     fileHash,
			ContentHash:  fileHash,
			Metadata:     metadata,
		}, nil
	}

	status := result.Status
	if status == "" {
		status = "processed"
	}

	return &Result{
		OK:               true,
		Status:           status,
		Kind:             string(kind),
		ID:               id,
		Source:           source,
		SourceID:         sourceID,
		Name:             name,
		Filename:         filename,
		FolderID:         resolvedFolderID,
		FolderPath:       resolvedFolderPath,
		LocalPath:        localPath,
		DriveLink:        result.DriveLink,
		DriveFileID:      result.DriveFileID,
		DownloadLink:     result.DownloadLink,
		FileHash:         fileHash,
		ContentHash:      fileHash,
		SkippedDuplicate: status == "skipped_duplicate" || status == "would_skip_duplicate",
		Metadata:         metadata,
	}, nil
}

func (s *Service) acquireLocalPath(ctx context.Context, kind Kind, req *Request) (string, string, func(), error) {
	localPath := strings.TrimSpace(req.LocalPath)
	filename := strings.TrimSpace(req.Filename)
	if localPath != "" {
		if filename == "" {
			filename = filepath.Base(localPath)
		}
		return localPath, filename, nil, nil
	}

	remoteURL := strings.TrimSpace(req.URL)
	if remoteURL == "" {
		return "", "", nil, fmt.Errorf("local_path or url is required")
	}

	tmpDir, err := os.MkdirTemp(s.tempDir, "media-ingest-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	dstName := filename
	if dstName == "" {
		if parsed, err := url.Parse(remoteURL); err == nil {
			dstName = filepath.Base(parsed.Path)
		}
	}
	if dstName == "" {
		dstName = fmt.Sprintf("%s.bin", string(kind))
	}

	dstPath := filepath.Join(tmpDir, dstName)
	if err := s.downloadToFile(ctx, remoteURL, dstPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", "", nil, err
	}

	if filename == "" {
		filename = dstName
	}

	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}
	return dstPath, filename, cleanup, nil
}

func (s *Service) materializeImage(sourcePath, filename string, req *Request) (string, string, func(), error) {
	if strings.TrimSpace(sourcePath) == "" {
		return "", "", nil, fmt.Errorf("image source path is required")
	}

	sourcePath = strings.TrimSpace(sourcePath)
	if strings.TrimSpace(filename) == "" {
		filename = filepath.Base(sourcePath)
	}

	slug := slugify(firstNonEmpty(req.Group, req.Name, req.SourceID, "image"))
	if slug == "" {
		slug = "image"
	}

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = filepath.Ext(sourcePath)
	}
	if ext == "" {
		ext = ".jpg"
	}

	fullDir := filepath.Join(s.imagesDir, slug)
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		return "", "", nil, fmt.Errorf("failed to create image dir: %w", err)
	}

	hash, err := hashutil.MD5File(sourcePath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to hash image source: %w", err)
	}

	dstPath := filepath.Join(fullDir, hash+ext)
	if sameFile(sourcePath, dstPath) {
		return dstPath, filepath.Base(dstPath), nil, nil
	}

	in, err := os.Open(sourcePath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to open image source: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dstPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create image destination: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dstPath)
		return "", "", nil, fmt.Errorf("failed to copy image into storage: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", "", nil, fmt.Errorf("failed to close image destination: %w", err)
	}

	return dstPath, filepath.Base(dstPath), nil, nil
}

func (s *Service) downloadToFile(ctx context.Context, remoteURL, dstPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remoteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return fmt.Errorf("failed to create destination dir: %w", err)
	}

	out, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to write downloaded file: %w", err)
	}

	return nil
}

func (s *Service) resolveDriveFolder(ctx context.Context, kind Kind, rootFolderID string, req *Request) (string, string, error) {
	if strings.TrimSpace(req.FolderID) != "" {
		return strings.TrimSpace(req.FolderID), strings.TrimSpace(req.FolderPath), nil
	}

	if s.driveUp == nil {
		return "", "", nil
	}

	if strings.TrimSpace(rootFolderID) == "" {
		zap.L().Warn("Drive root folder not configured, skipping Drive upload", zap.String("kind", string(kind)))
		return "", "", nil
	}

	folderID := strings.TrimSpace(rootFolderID)
	var parts []string
	if path := strings.TrimSpace(req.FolderPath); path != "" {
		parts = splitFolderPath(path)
	} else {
		if group := strings.TrimSpace(req.Group); group != "" {
			parts = append(parts, group)
		} else if fallback := defaultGroupForKind(kind, req); fallback != "" {
			parts = append(parts, fallback)
		}
		if sub := strings.TrimSpace(req.Subfolder); sub != "" {
			parts = append(parts, sub)
		}
	}

	for _, part := range parts {
		nextID, err := s.driveUp.GetOrCreateFolder(ctx, part, folderID)
		if err != nil {
			return "", "", fmt.Errorf("failed to get or create folder %q: %w", part, err)
		}
		folderID = nextID
	}

	return folderID, strings.Join(parts, "/"), nil
}

func buildAssetID(kind Kind, hash string) string {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return string(kind)
	}
	return string(kind) + ":" + hash
}

func toAssetKind(kind Kind) lifecycle.AssetKind {
	switch kind {
	case KindImage:
		return lifecycle.AssetKindImage
	case KindVoiceover:
		return lifecycle.AssetKindAudio
	case KindClip, KindStock:
		return lifecycle.AssetKindVideo
	default:
		return lifecycle.AssetKindDocument
	}
}

func normalizeKind(kind string) Kind {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case string(KindImage):
		return KindImage
	case string(KindVoiceover):
		return KindVoiceover
	case string(KindClip):
		return KindClip
	case string(KindStock):
		return KindStock
	default:
		return ""
	}
}

func defaultGroupForKind(kind Kind, req *Request) string {
	switch kind {
	case KindImage:
		return slugOrFallback(firstNonEmpty(req.Group, req.Name, req.SourceID, "images"))
	case KindVoiceover:
		return slugOrFallback(firstNonEmpty(req.Group, req.Name, "voiceover"))
	case KindClip:
		return slugOrFallback(firstNonEmpty(req.Group, req.Source, req.Name, "clips"))
	case KindStock:
		return slugOrFallback(firstNonEmpty(req.Group, req.Source, req.Name, "stock"))
	default:
		return ""
	}
}

func slugOrFallback(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	slug := slugify(value)
	if slug == "" {
		return value
	}
	return slug
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile("[^a-z0-9]+")
	s = re.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func splitFolderPath(p string) []string {
	raw := strings.Split(p, "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func sameFile(a, b string) bool {
	aInfo, errA := os.Stat(a)
	bInfo, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(aInfo, bInfo)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
