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
	Enabled      bool   `yaml:"enabled"`
	ServerURL    string `yaml:"server_url"`
	ScriptPath   string `yaml:"script_path"`
	PythonBin    string `yaml:"python_bin"`
	DBPath       string `yaml:"db_path"`
	AutoIndexAfterArtlist bool `yaml:"auto_index_after_artlist"`
}

// DefaultConfig returns default clipindexer config
func DefaultConfig() *Config {
	return &Config{
		Enabled:                true,
		ServerURL:              "http://127.0.0.1:8001",
		ScriptPath:             "scripts/index_clips.py",
		PythonBin:              "python3",
		AutoIndexAfterArtlist:  true,
	}
}

// Service provides clip indexing functionality
type Service struct {
	db            *sql.DB
	cfg           *Config
	log           *zap.Logger
	scriptPath    string
	dbPath        string
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
			return nil
		}
		s.log.Warn("failed to index via API, falling back to script", zap.Error(err))
	}

	return s.indexViaScript(ctx, clipID)
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
	err := s.db.QueryRowContext(ctx, "SELECT name, local_path FROM clips WHERE id = ?", clipID).Scan(&name, &localPath)
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

// IndexRunItems indexes multiple clips from a run
func (s *Service) IndexRunItems(ctx context.Context, items []map[string]interface{}) error {
	if !s.cfg.Enabled {
		return nil
	}

	for _, item := range items {
		clipID, _ := item["clip_id"].(string)
		if clipID == "" {
			continue
		}
		if err := s.IndexClip(ctx, clipID); err != nil {
			s.log.Warn("failed to index clip", zap.String("clip_id", clipID), zap.Error(err))
		}
	}
	return nil
}

// IsEnabled returns whether the service is enabled
func (s *Service) IsEnabled() bool {
	return s.cfg.Enabled
}
