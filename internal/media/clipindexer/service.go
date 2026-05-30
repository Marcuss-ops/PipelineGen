package clipindexer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Config holds clipindexer configuration
type Config struct {
	Enabled               bool   `yaml:"enabled"`
	ServerURL             string `yaml:"server_url"`
	ScriptPath            string `yaml:"script_path"`
	PythonBin             string `yaml:"python_bin"`
	DBPath                string `yaml:"db_path"`
	AutoIndexAfterArtlist bool   `yaml:"auto_index_after_artlist"`
}

// DefaultConfig returns default clipindexer config
func DefaultConfig() *Config {
	return &Config{
		Enabled:               true,
		ServerURL:             "http://127.0.0.1:8001",
		ScriptPath:            "scripts/index_clips.py",
		PythonBin:             "python3",
		AutoIndexAfterArtlist: true,
	}
}

// Service provides clip indexing functionality
type Service struct {
	db           *sql.DB
	cfg          *Config
	log          *zap.Logger
	scriptPath   string
	dbPath       string
	vectorStore  VectorStoreIndexer
}

// NewService creates a new clipindexer service
func NewService(cfg *Config, db *sql.DB, dbPath string, log *zap.Logger) *Service {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	// Resolve script path to absolute
	scriptPath := cfg.ScriptPath
	if !filepath.IsAbs(scriptPath) {
		absPath, err := filepath.Abs(scriptPath)
		if err == nil {
			scriptPath = absPath
		}
	}

	return &Service{
		db:         db,
		cfg:        cfg,
		log:        log,
		scriptPath: scriptPath,
		dbPath:     dbPath,
	}
}

// IndexClip indexes a single clip by ID
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if !s.cfg.Enabled {
		s.log.Debug("clipindexer disabled, skipping", zap.String("clip_id", clipID))
		return nil
	}

	if s.cfg.ServerURL != "" {
		err := s.indexViaAPI(ctx, clipID)
		if err == nil {
			s.UpsertVectorStore(ctx, clipID)
			return nil
		}
		s.log.Warn("failed to index via API, falling back to script", zap.Error(err))
	}

	err := s.indexViaScript(ctx, clipID)
	if err != nil {
		return err
	}
	s.UpsertVectorStore(ctx, clipID)
	return nil
}

func (s *Service) indexViaAPI(ctx context.Context, clipID string) error {
	payload := map[string]string{
		"db_path": s.dbPath,
		"clip_id": clipID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/index", strings.TrimSuffix(s.cfg.ServerURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	s.log.Info("clip indexed via API", zap.String("clip_id", clipID))
	return nil
}

func (s *Service) indexViaScript(ctx context.Context, clipID string) error {
	// Get clip info from DB
	var name, localPath string
	err := s.db.QueryRowContext(ctx, "SELECT name, json_extract(metadata_json, '$.local_path') FROM media_assets WHERE id = ?", clipID).Scan(&name, &localPath)
	if err != nil {
		return fmt.Errorf("failed to get clip info: %w", err)
	}

	// Build command arguments using absolute script path
	scriptName := filepath.Base(s.scriptPath)
	args := []string{scriptName}

	// Add database path (required by script)
	if s.dbPath != "" {
		args = append(args, "--db", s.dbPath)
	}

	if name != "" {
		args = append(args, "--clip-name", name)
	}
	if localPath != "" {
		args = append(args, "--clip-path", localPath)
	}
	args = append(args, "--clip-id", clipID)

	// Execute Python script from the script's directory
	cmd := exec.CommandContext(ctx, s.cfg.PythonBin, args...)
	cmd.Dir = filepath.Dir(s.scriptPath)

	s.log.Info("indexing clip via script", zap.String("clip_id", clipID), zap.String("script", s.scriptPath))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to index clip %s: %w, output: %s", clipID, err, strings.TrimSpace(string(output)))
	}

	s.log.Info("clip indexed successfully via script", zap.String("clip_id", clipID))
	return nil
}

// IndexRunItems indexes multiple clips from a run using bulk API if possible
func (s *Service) IndexRunItems(ctx context.Context, items []map[string]interface{}) error {
	if !s.cfg.Enabled {
		return nil
	}

	clipIDs := make([]string, 0, len(items))
	for _, item := range items {
		clipID, _ := item["clip_id"].(string)
		if clipID != "" {
			clipIDs = append(clipIDs, clipID)
		}
	}

	if len(clipIDs) == 0 {
		return nil
	}

	if s.cfg.ServerURL != "" {
		err := s.indexBulkAPI(ctx, clipIDs)
		if err == nil {
			return nil
		}
		s.log.Warn("bulk indexing via API failed, falling back to individual indexing", zap.Error(err))
	}

	for _, clipID := range clipIDs {
		if err := s.IndexClip(ctx, clipID); err != nil {
			s.log.Warn("failed to index clip", zap.String("clip_id", clipID), zap.Error(err))
		}
	}
	return nil
}

func (s *Service) indexBulkAPI(ctx context.Context, clipIDs []string) error {
	payload := map[string]interface{}{
		"db_path":  s.dbPath,
		"clip_ids": clipIDs,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/index_bulk", strings.TrimSuffix(s.cfg.ServerURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	s.log.Info("bulk indexing completed via API", zap.Int("count", len(clipIDs)))
	return nil
}

// SetVectorStore sets the vector store indexer for Qdrant upsert after indexing.
func (s *Service) SetVectorStore(vs VectorStoreIndexer) {
	s.vectorStore = vs
}

// UpsertVectorStore pushes the newly indexed clip to Qdrant if a vector store is configured.
func (s *Service) UpsertVectorStore(ctx context.Context, clipID string) {
	if s.vectorStore == nil {
		return
	}
	if err := s.vectorStore.UpsertFromClip(ctx, clipID); err != nil {
		s.log.Warn("failed to upsert clip to vector store",
			zap.String("clip_id", clipID),
			zap.Error(err))
	} else {
		s.log.Debug("vector store upserted clip",
			zap.String("clip_id", clipID))
	}
}

// IsEnabled returns whether the service is enabled
func (s *Service) IsEnabled() bool {
	return s.cfg.Enabled
}

// StartServer starts the Python embedding server as a background process
func (s *Service) StartServer(ctx context.Context) error {
	if !s.cfg.Enabled || s.cfg.ServerURL == "" {
		return nil
	}

	// Check if server is already running
	if s.checkServer(ctx) {
		s.log.Info("embedding server already running")
		return nil
	}

	// Start server
	serverScript := filepath.Join(filepath.Dir(s.scriptPath), "embedding_server.py")
	s.log.Info("starting embedding server", zap.String("script", serverScript))

	cmd := exec.Command(s.cfg.PythonBin, serverScript)
	cmd.Dir = filepath.Dir(s.scriptPath)

	// Start the process in the background
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start embedding server: %w", err)
	}

	s.log.Info("embedding server process started", zap.Int("pid", cmd.Process.Pid))
	return nil
}

// StartWatchdog starts a background goroutine to monitor and restart the server if it fails
func (s *Service) StartWatchdog(ctx context.Context) {
	if !s.cfg.Enabled || s.cfg.ServerURL == "" {
		return
	}

	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.checkServer(ctx) {
					s.log.Warn("embedding server health check failed, restarting...")
					if err := s.StartServer(ctx); err != nil {
						s.log.Error("watchdog failed to restart server", zap.Error(err))
					}
				}
			}
		}
	}()
}

func (s *Service) checkServer(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/health", strings.TrimSuffix(s.cfg.ServerURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
