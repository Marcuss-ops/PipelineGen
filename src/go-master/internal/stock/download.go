// Package stock provides download and reporting functionality for stock videos.
package stock

import (
"context"
"crypto/rand"
"encoding/hex"
"fmt"
"os"
"os/exec"
"path/filepath"
"time"

"velox/go-master/pkg/logger"
"go.uber.org/zap"
)

// DownloadVideo starts a background yt-dlp download for the project.
func (m *StockManager) DownloadVideo(ctx context.Context, url string, projectName string) (*DownloadTask, error) {
m.mu.Lock()
defer m.mu.Unlock()

if _, exists := m.projects[projectName]; !exists {
return nil, fmt.Errorf("project %s not found", projectName)
}

task := &DownloadTask{
ID:          generateTaskID(),
URL:         url,
ProjectName: projectName,
Status:      "pending",
Progress:    0,
CreatedAt:   time.Now(),
}
m.downloads[task.ID] = task
go m.performDownload(task)
return task, nil
}

func (m *StockManager) performDownload(task *DownloadTask) {
projectDir := filepath.Join(m.projectsDir, task.ProjectName, "videos")
cmd := exec.Command("yt-dlp",
"--newline",
"--no-warnings",
"-f", "best[height<=1080]",
"-o", filepath.Join(projectDir, "%(title)s.%(ext)s"),
task.URL,
)
output, err := cmd.CombinedOutput()

m.mu.Lock()
defer m.mu.Unlock()

if err != nil {
task.Status = "failed"
task.Error = fmt.Sprintf("%v: %s", err, string(output))
logger.Error("Download failed", zap.String("task_id", task.ID), zap.Error(err))
return
}

task.Status = "completed"
task.Progress = 100
task.OutputPath = detectLatestFile(projectDir)
_ = m.updateProjectStats(task.ProjectName)
logger.Info("Download completed", zap.String("task_id", task.ID), zap.String("output", task.OutputPath))
}

// GetDownloadStatus returns the current state for a download task.
func (m *StockManager) GetDownloadStatus(ctx context.Context, taskID string) (*DownloadStatus, error) {
m.mu.RLock()
defer m.mu.RUnlock()

task, exists := m.downloads[taskID]
if !exists {
return nil, fmt.Errorf("task not found: %s", taskID)
}

return &DownloadStatus{
TaskID:     task.ID,
Status:     task.Status,
Progress:   task.Progress,
OutputPath: task.OutputPath,
Error:      task.Error,
}, nil
}

// GetReport summarizes project files currently on disk.
func (m *StockManager) GetReport(ctx context.Context, projectName string) (*ProjectReport, error) {
project, err := m.GetProject(ctx, projectName)
if err != nil {
return nil, err
}

projectDir := filepath.Join(m.projectsDir, projectName, "videos")
entries, err := os.ReadDir(projectDir)
if err != nil {
return nil, err
}

report := &ProjectReport{
Project:     *project,
Videos:      []VideoInfo{},
TotalSize:   0,
LastUpdated: time.Now(),
}

for _, entry := range entries {
if entry.IsDir() {
continue
}
info, err := entry.Info()
if err != nil {
continue
}
report.Videos = append(report.Videos, VideoInfo{
Name:      info.Name(),
Path:      filepath.Join(projectDir, info.Name()),
Size:      info.Size(),
Format:    filepath.Ext(info.Name()),
CreatedAt: info.ModTime(),
})
report.TotalSize += info.Size()
}
return report, nil
}

// ProcessProject currently reports the files already available for the project.
func (m *StockManager) ProcessProject(ctx context.Context, options *ProcessOptions) (*ProcessResult, error) {
if options == nil || options.ProjectName == "" {
return nil, fmt.Errorf("project_name is required")
}

report, err := m.GetReport(ctx, options.ProjectName)
if err != nil {
return nil, err
}

outputFiles := make([]string, 0, len(report.Videos))
for _, video := range report.Videos {
outputFiles = append(outputFiles, video.Path)
}

return &ProcessResult{
ProjectName:     options.ProjectName,
VideosProcessed: len(report.Videos),
TotalSize:       report.TotalSize,
OutputFiles:     outputFiles,
}, nil
}

func generateTaskID() string {
bytes := make([]byte, 8)
_, _ = rand.Read(bytes)
return "task_" + hex.EncodeToString(bytes)
}

func detectLatestFile(dir string) string {
entries, err := os.ReadDir(dir)
if err != nil {
return ""
}

var latestPath string
var latestTime time.Time
for _, entry := range entries {
if entry.IsDir() {
continue
}
info, err := entry.Info()
if err != nil {
continue
}
if info.ModTime().After(latestTime) {
latestTime = info.ModTime()
latestPath = filepath.Join(dir, entry.Name())
}
}
return latestPath
}
