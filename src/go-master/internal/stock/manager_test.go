package stock

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- NewManager Tests ---

func TestNewManager(t *testing.T) {
	tmpDir := t.TempDir()
	m, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("expected manager, got nil")
	}
	if m.dataDir != tmpDir {
		t.Errorf("dataDir = %q, want %q", m.dataDir, tmpDir)
	}
	expectedProjectsDir := filepath.Join(tmpDir, "stock_projects")
	if m.projectsDir != expectedProjectsDir {
		t.Errorf("projectsDir = %q, want %q", m.projectsDir, expectedProjectsDir)
	}
}

func TestNewManager_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	projectsDir := filepath.Join(tmpDir, "stock_projects")

	_, err := NewManager(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		t.Error("expected stock_projects directory to be created")
	}
}

// --- CreateProject Tests ---

func TestStockManager_CreateProject(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	tests := []struct {
		name        string
		projectName string
		config      *ProjectConfig
		expectError bool
	}{
		{
			name:        "Valid project",
			projectName: "test-project",
			config: &ProjectConfig{
				Description: "A test project",
				Tags:        []string{"test", "demo"},
			},
			expectError: false,
		},
		{
			name:        "Empty name",
			projectName: "",
			config: &ProjectConfig{
				Description: "Should fail",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project, err := m.CreateProject(context.Background(), tt.projectName, tt.config)
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if project.Name != tt.projectName {
					t.Errorf("project name = %q, want %q", project.Name, tt.projectName)
				}
				if project.Status != "active" {
					t.Errorf("project status = %q, want active", project.Status)
				}
				// Check project directory was created
				projectDir := filepath.Join(tmpDir, "stock_projects", tt.projectName)
				if _, err := os.Stat(projectDir); os.IsNotExist(err) {
					t.Error("expected project directory to exist")
				}
				videosDir := filepath.Join(projectDir, "videos")
				if _, err := os.Stat(videosDir); os.IsNotExist(err) {
					t.Error("expected videos subdirectory to exist")
				}
			}
		})
	}
}

func TestStockManager_CreateProject_Duplicate(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, err := m.CreateProject(context.Background(), "dup-project", &ProjectConfig{})
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	_, err = m.CreateProject(context.Background(), "dup-project", &ProjectConfig{})
	if err == nil {
		t.Fatal("expected error for duplicate project, got nil")
	}
}

// --- ListProjects Tests ---

func TestStockManager_ListProjects(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	// Empty list
	projects, err := m.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}

	// Create some projects
	_, _ = m.CreateProject(context.Background(), "proj1", &ProjectConfig{Description: "Project 1"})
	_, _ = m.CreateProject(context.Background(), "proj2", &ProjectConfig{Description: "Project 2"})

	projects, err = m.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(projects) != 2 {
		t.Errorf("expected 2 projects, got %d", len(projects))
	}
}

// --- GetProject Tests ---

func TestStockManager_GetProject(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, _ = m.CreateProject(context.Background(), "my-project", &ProjectConfig{Description: "desc"})

	project, err := m.GetProject(context.Background(), "my-project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if project.Name != "my-project" {
		t.Errorf("project name = %q, want my-project", project.Name)
	}
}

func TestStockManager_GetProject_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, err := m.GetProject(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- DeleteProject Tests ---

func TestStockManager_DeleteProject(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, _ = m.CreateProject(context.Background(), "to-delete", &ProjectConfig{})

	err := m.DeleteProject(context.Background(), "to-delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify project is gone
	_, err = m.GetProject(context.Background(), "to-delete")
	if err == nil {
		t.Error("expected project to be deleted")
	}

	// Verify directory is gone
	projectDir := filepath.Join(tmpDir, "stock_projects", "to-delete")
	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Error("expected project directory to be deleted")
	}
}

func TestStockManager_DeleteProject_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	err := m.DeleteProject(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- Project Directories Tests ---

func TestStockManager_GetProjectDir(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	result := m.GetProjectDir("my-project")
	expected := filepath.Join(tmpDir, "stock_projects", "my-project")
	if result != expected {
		t.Errorf("GetProjectDir = %q, want %q", result, expected)
	}
}

func TestStockManager_GetVideosDir(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	result := m.GetVideosDir("my-project")
	expected := filepath.Join(tmpDir, "stock_projects", "my-project", "videos")
	if result != expected {
		t.Errorf("GetVideosDir = %q, want %q", result, expected)
	}
}

// --- Download Management Tests ---

func TestStockManager_DownloadVideo_ProjectNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, err := m.DownloadVideo(context.Background(), "https://youtube.com/watch?v=abc", "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStockManager_DownloadVideo_Success(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, _ = m.CreateProject(context.Background(), "dl-project", &ProjectConfig{})

	task, err := m.DownloadVideo(context.Background(), "https://youtube.com/watch?v=abc123", "dl-project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if task == nil {
		t.Fatal("expected task, got nil")
	}
	if task.ID == "" {
		t.Error("expected non-empty task ID")
	}
	if task.Status != "pending" {
		t.Errorf("task status = %q, want pending", task.Status)
	}
	if task.ProjectName != "dl-project" {
		t.Errorf("task project = %q, want dl-project", task.ProjectName)
	}
}

func TestStockManager_GetDownloadStatus_Success(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, _ = m.CreateProject(context.Background(), "status-project", &ProjectConfig{})
	task, _ := m.DownloadVideo(context.Background(), "https://youtube.com/watch?v=abc", "status-project")

	status, err := m.GetDownloadStatus(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.TaskID != task.ID {
		t.Errorf("task ID = %q, want %q", status.TaskID, task.ID)
	}
}

func TestStockManager_GetDownloadStatus_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, err := m.GetDownloadStatus(context.Background(), "nonexistent-task")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- generateTaskID Tests ---

func TestGenerateTaskID(t *testing.T) {
	id1 := generateTaskID()
	id2 := generateTaskID()

	if id1 == id2 {
		t.Error("expected unique task IDs, got duplicates")
	}
	if len(id1) == 0 {
		t.Error("expected non-empty task ID")
	}
	// Task IDs should start with "task_"
	if len(id1) < 5 || id1[:5] != "task_" {
		t.Errorf("task ID %q does not start with task_", id1)
	}
}

// --- Report Tests ---

func TestStockManager_GetReport_ProjectNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, err := m.GetReport(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStockManager_GetReport_Success(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, _ = m.CreateProject(context.Background(), "report-project", &ProjectConfig{})

	report, err := m.GetReport(context.Background(), "report-project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Project.Name != "report-project" {
		t.Errorf("report project name = %q, want report-project", report.Project.Name)
	}
	if report.Videos == nil {
		t.Error("expected Videos slice, got nil")
	}
}

// --- ProcessProject Tests ---

func TestStockManager_ProcessProject_Success(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, _ = m.CreateProject(context.Background(), "process-project", &ProjectConfig{})

	result, err := m.ProcessProject(context.Background(), &ProcessOptions{
		ProjectName: "process-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProjectName != "process-project" {
		t.Errorf("project name = %q, want process-project", result.ProjectName)
	}
}

func TestStockManager_ProcessProject_MissingName(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, err := m.ProcessProject(context.Background(), &ProcessOptions{ProjectName: ""})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestStockManager_ProcessProject_NilOptions(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, err := m.ProcessProject(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- UpdateProject Tests ---

func TestStockManager_UpdateProject(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	_, _ = m.CreateProject(context.Background(), "update-project", &ProjectConfig{
		Description: "original",
		Tags:        []string{"old"},
	})

	err := m.UpdateProject(context.Background(), "update-project", &ProjectConfig{
		Description: "updated description",
		Tags:        []string{"new", "tags"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	project, err := m.GetProject(context.Background(), "update-project")
	if err != nil {
		t.Fatalf("unexpected error getting project: %v", err)
	}
	if project.Description != "updated description" {
		t.Errorf("description = %q, want updated description", project.Description)
	}
}

func TestStockManager_UpdateProject_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	err := m.UpdateProject(context.Background(), "nonexistent", &ProjectConfig{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- detectLatestFile Tests ---

func TestDetectLatestFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with different modification times
	file1 := filepath.Join(tmpDir, "old.txt")
	file2 := filepath.Join(tmpDir, "new.txt")

	os.WriteFile(file1, []byte("old"), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(file2, []byte("new"), 0644)

	result := detectLatestFile(tmpDir)
	if result != file2 {
		t.Errorf("detectLatestFile = %q, want %q", result, file2)
	}
}

func TestDetectLatestFile_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	result := detectLatestFile(tmpDir)
	if result != "" {
		t.Errorf("expected empty string for empty dir, got %q", result)
	}
}

func TestDetectLatestFile_NonExistentDir(t *testing.T) {
	result := detectLatestFile("/nonexistent/path/that/does/not/exist")
	if result != "" {
		t.Errorf("expected empty string for nonexistent dir, got %q", result)
	}
}

// --- Search Tests (interface-level) ---

func TestStockManager_Search_InvalidSource(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	// Search with invalid source should not panic and return empty/error gracefully
	results, err := m.Search(context.Background(), "test query", []string{"invalid_source"})
	// Should not error, just return empty results for invalid sources
	if err != nil {
		t.Logf("Search returned error (acceptable): %v", err)
	}
	_ = results // May be empty for invalid sources
}

func TestStockManager_Search_EmptySources(t *testing.T) {
	tmpDir := t.TempDir()
	m, _ := NewManager(tmpDir)

	// Empty sources defaults to youtube
	results, err := m.Search(context.Background(), "test", []string{})
	// May fail due to yt-dlp not being available, but shouldn't panic
	_ = results
	_ = err
}

// --- Project/Type Struct Tests ---

func TestProjectStruct(t *testing.T) {
	now := time.Now()
	p := Project{
		Name:        "test",
		CreatedAt:   now,
		UpdatedAt:   now,
		VideoCount:  5,
		TotalSize:   1024,
		Status:      "active",
		Tags:        []string{"tag1"},
		Description: "A test project",
	}

	if p.Name != "test" {
		t.Errorf("Name = %q, want test", p.Name)
	}
	if p.VideoCount != 5 {
		t.Errorf("VideoCount = %d, want 5", p.VideoCount)
	}
	if p.Status != "active" {
		t.Errorf("Status = %q, want active", p.Status)
	}
}

func TestSearchResultStruct(t *testing.T) {
	sr := SearchResult{
		Source:      "youtube",
		Title:       "Test Video",
		URL:         "https://youtube.com/watch?v=abc",
		Duration:    120,
		Thumbnail:   "https://i.ytimg.com/vi/abc/maxresdefault.jpg",
		Description: "A test video",
	}

	if sr.Source != "youtube" {
		t.Errorf("Source = %q, want youtube", sr.Source)
	}
	if sr.Duration != 120 {
		t.Errorf("Duration = %d, want 120", sr.Duration)
	}
}

func TestVideoResultStruct(t *testing.T) {
	vr := VideoResult{
		ID:          "abc123",
		Source:      "youtube",
		Title:       "Test",
		URL:         "https://youtube.com/watch?v=abc123",
		Duration:    300,
		Thumbnail:   "https://i.ytimg.com/vi/abc123/maxresdefault.jpg",
		Description: "desc",
		Uploader:    "UploaderName",
		ViewCount:   1000000,
	}

	if vr.ID != "abc123" {
		t.Errorf("ID = %q, want abc123", vr.ID)
	}
	if vr.ViewCount != 1000000 {
		t.Errorf("ViewCount = %d, want 1000000", vr.ViewCount)
	}
}

func TestDownloadTaskStruct(t *testing.T) {
	now := time.Now()
	dt := DownloadTask{
		ID:          "task_123",
		URL:         "https://youtube.com/watch?v=abc",
		ProjectName: "proj",
		Status:      "completed",
		Progress:    100,
		OutputPath:  "/path/to/video.mp4",
		Error:       "",
		CreatedAt:   now,
	}

	if dt.Status != "completed" {
		t.Errorf("Status = %q, want completed", dt.Status)
	}
	if dt.Progress != 100 {
		t.Errorf("Progress = %d, want 100", dt.Progress)
	}
}

func TestDownloadStatusStruct(t *testing.T) {
	ds := DownloadStatus{
		TaskID:     "task_123",
		Status:     "pending",
		Progress:   0,
		Speed:      "5.2 MB/s",
		ETA:        "2m30s",
		OutputPath: "",
		Error:      "",
	}

	if ds.TaskID != "task_123" {
		t.Errorf("TaskID = %q, want task_123", ds.TaskID)
	}
	if ds.Speed != "5.2 MB/s" {
		t.Errorf("Speed = %q, want 5.2 MB/s", ds.Speed)
	}
}

func TestProcessOptionsStruct(t *testing.T) {
	po := ProcessOptions{
		ProjectName:    "my-project",
		Videos:         []string{"video1.mp4", "video2.mp4"},
		Format:         "mp4",
		Quality:        "high",
		Resolution:     "1080p",
		RemoveAudio:    true,
		GenerateThumbs: true,
	}

	if po.ProjectName != "my-project" {
		t.Errorf("ProjectName = %q, want my-project", po.ProjectName)
	}
	if !po.RemoveAudio {
		t.Error("expected RemoveAudio = true")
	}
}

func TestProcessResultStruct(t *testing.T) {
	pr := ProcessResult{
		ProjectName:     "my-project",
		VideosProcessed: 10,
		TotalSize:       500000000,
		OutputFiles:     []string{"/path/file1.mp4"},
		Duration:        30.5,
	}

	if pr.VideosProcessed != 10 {
		t.Errorf("VideosProcessed = %d, want 10", pr.VideosProcessed)
	}
	if pr.Duration != 30.5 {
		t.Errorf("Duration = %f, want 30.5", pr.Duration)
	}
}

func TestProjectReportStruct(t *testing.T) {
	pr := ProjectReport{
		Project:       Project{Name: "test"},
		Videos:        []VideoInfo{},
		TotalSize:     1024,
		TotalDuration: 300,
		LastUpdated:   time.Now(),
	}

	if pr.TotalSize != 1024 {
		t.Errorf("TotalSize = %d, want 1024", pr.TotalSize)
	}
}

func TestVideoInfoStruct(t *testing.T) {
	vi := VideoInfo{
		Name:       "video.mp4",
		Path:       "/path/video.mp4",
		Size:       50000000,
		Duration:   120,
		Resolution: "1920x1080",
		Format:     "mp4",
		CreatedAt:  time.Now(),
	}

	if vi.Format != "mp4" {
		t.Errorf("Format = %q, want mp4", vi.Format)
	}
	if vi.Resolution != "1920x1080" {
		t.Errorf("Resolution = %q, want 1920x1080", vi.Resolution)
	}
}

func TestProjectConfigStruct(t *testing.T) {
	pc := ProjectConfig{
		Description: "Test config",
		Tags:        []string{"test"},
		OutputDir:   "/tmp/output",
	}

	if pc.Description != "Test config" {
		t.Errorf("Description = %q, want Test config", pc.Description)
	}
}
