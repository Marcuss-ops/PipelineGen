// Package stock provides project management functionality.
package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	youtube "velox/go-master/internal/youtube"
	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// StockManager gestisce progetti stock video
type StockManager struct {
	dataDir     string
	projectsDir string
	mu          sync.RWMutex
	projects    map[string]*Project
	downloads   map[string]*DownloadTask
	ytClient    youtube.Client // YouTube v2 client for search
}

// NewManager crea un nuovo manager
func NewManager(dataDir string, ytClient youtube.Client) (*StockManager, error) {
	projectsDir := filepath.Join(dataDir, "stock_projects")
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		return nil, err
	}

	m := &StockManager{
		dataDir:     dataDir,
		projectsDir: projectsDir,
		projects:    make(map[string]*Project),
		downloads:   make(map[string]*DownloadTask),
		ytClient:    ytClient,
	}

	if err := m.loadProjects(); err != nil {
		return nil, err
	}

	return m, nil
}

// CreateProject crea un nuovo progetto
func (m *StockManager) CreateProject(ctx context.Context, name string, config *ProjectConfig) (*Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if name == "" {
		return nil, fmt.Errorf("project name cannot be empty")
	}

	if _, exists := m.projects[name]; exists {
		return nil, fmt.Errorf("project %s already exists", name)
	}

	project := &Project{
		Name:        name,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		Status:      "active",
		Tags:        config.Tags,
		Description: config.Description,
	}

	// Crea directory progetto
	projectDir := filepath.Join(m.projectsDir, name)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, err
	}

	// Crea sottodirectory videos
	videosDir := filepath.Join(projectDir, "videos")
	if err := os.MkdirAll(videosDir, 0755); err != nil {
		return nil, err
	}

	m.projects[name] = project

	if err := m.saveProjects(); err != nil {
		delete(m.projects, name)
		return nil, err
	}

	logger.Info("Project created", zap.String("name", name))
	return project, nil
}

// ListProjects elenca tutti i progetti
func (m *StockManager) ListProjects(ctx context.Context) ([]Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	projects := make([]Project, 0, len(m.projects))
	for _, p := range m.projects {
		projects = append(projects, *p)
	}

	return projects, nil
}

// GetProject ottiene un progetto per nome
func (m *StockManager) GetProject(ctx context.Context, name string) (*Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	project, exists := m.projects[name]
	if !exists {
		return nil, fmt.Errorf("project %s not found", name)
	}

	return project, nil
}

// DeleteProject elimina un progetto
func (m *StockManager) DeleteProject(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.projects[name]; !exists {
		return fmt.Errorf("project %s not found", name)
	}

	// Elimina directory
	projectDir := filepath.Join(m.projectsDir, name)
	if err := os.RemoveAll(projectDir); err != nil {
		return err
	}

	delete(m.projects, name)

	if err := m.saveProjects(); err != nil {
		return err
	}

	logger.Info("Project deleted", zap.String("name", name))
	return nil
}

// UpdateProject aggiorna un progetto
func (m *StockManager) UpdateProject(ctx context.Context, name string, config *ProjectConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	project, exists := m.projects[name]
	if !exists {
		return fmt.Errorf("project %s not found", name)
	}

	if config.Description != "" {
		project.Description = config.Description
	}
	if config.Tags != nil {
		project.Tags = config.Tags
	}
	project.UpdatedAt = time.Now()

	return m.saveProjects()
}

// GetProjectDir restituisce la directory del progetto
func (m *StockManager) GetProjectDir(name string) string {
	return filepath.Join(m.projectsDir, name)
}

// GetVideosDir restituisce la directory videos del progetto
func (m *StockManager) GetVideosDir(name string) string {
	return filepath.Join(m.projectsDir, name, "videos")
}

// saveProjects salva l'indice progetti
func (m *StockManager) saveProjects() error {
	data, err := json.MarshalIndent(m.projects, "", "  ")
	if err != nil {
		return err
	}

	indexFile := filepath.Join(m.projectsDir, "index.json")
	return os.WriteFile(indexFile, data, 0644)
}

// loadProjects carica l'indice progetti
func (m *StockManager) loadProjects() error {
	indexFile := filepath.Join(m.projectsDir, "index.json")

	data, err := os.ReadFile(indexFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return json.Unmarshal(data, &m.projects)
}

// updateProjectStats aggiorna le statistiche del progetto
func (m *StockManager) updateProjectStats(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	project, exists := m.projects[name]
	if !exists {
		return nil
	}

	videosDir := filepath.Join(m.projectsDir, name, "videos")
	entries, err := os.ReadDir(videosDir)
	if err != nil {
		return err
	}

	var totalSize int64
	for _, entry := range entries {
		if !entry.IsDir() {
			info, err := entry.Info()
			if err == nil {
				totalSize += info.Size()
			}
		}
	}

	project.VideoCount = len(entries)
	project.TotalSize = totalSize
	project.UpdatedAt = time.Now()

	return m.saveProjects()
}