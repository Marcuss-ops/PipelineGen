// Package mocks provides mock implementations for testing
package mocks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/youtube"
)

// MockDriveClient is a mock implementation of the Drive client
type MockDriveClient struct {
	mu       sync.RWMutex
	folders  map[string]*MockFolder
	files    map[string]*MockFile
	rootID   string
	callLog  []MockCall
}

// MockFolder represents a mock Drive folder
type MockFolder struct {
	ID       string
	Name     string
	ParentID string
	Link     string
	Subfolders []MockFolder
	Files      []MockFile
}

// MockFile represents a mock Drive file
type MockFile struct {
	ID           string
	Name         string
	MimeType     string
	Size         int64
	Link         string
	ModifiedTime time.Time
	FolderID     string
}

// MockCall tracks method calls for verification
type MockCall struct {
	Method string
	Args   map[string]interface{}
	Time   time.Time
}

// NewMockDriveClient creates a new mock Drive client
func NewMockDriveClient() *MockDriveClient {
	rootID := "mock-root-folder-id"
	return &MockDriveClient{
		folders: map[string]*MockFolder{
			rootID: {
				ID:   rootID,
				Name: "Root",
				Link: "https://drive.google.com/drive/folders/" + rootID,
			},
		},
		files:  make(map[string]*MockFile),
		rootID: rootID,
		callLog: make([]MockCall, 0),
	}
}

// ListFolders mocks the ListFolders method
func (m *MockDriveClient) ListFolders(ctx context.Context, opts drive.ListFoldersOptions) ([]drive.Folder, error) {
	m.logCall("ListFolders", map[string]interface{}{"opts": opts})
	
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []drive.Folder
	parentID := opts.ParentID
	if parentID == "" {
		parentID = m.rootID
	}

	for _, folder := range m.folders {
		if folder.ParentID == parentID {
			results = append(results, drive.Folder{
				ID:   folder.ID,
				Name: folder.Name,
				Link: folder.Link,
			})
		}
	}

	return results, nil
}

// GetFolderContent mocks getting folder content
func (m *MockDriveClient) GetFolderContent(ctx context.Context, folderID string) (*drive.FolderContent, error) {
	m.logCall("GetFolderContent", map[string]interface{}{"folderID": folderID})
	
	m.mu.RLock()
	defer m.mu.RUnlock()

	folder, exists := m.folders[folderID]
	if !exists {
		return nil, fmt.Errorf("folder not found: %s", folderID)
	}

	content := &drive.FolderContent{
		FolderID:   folder.ID,
		FolderName: folder.Name,
		Files:      make([]drive.File, 0),
		Subfolders: make([]drive.Folder, 0),
	}

	// Add files
	for _, file := range folder.Files {
		content.Files = append(content.Files, drive.File{
			ID:           file.ID,
			Name:         file.Name,
			MimeType:     file.MimeType,
			Size:         file.Size,
			Link:         file.Link,
			ModifiedTime: file.ModifiedTime,
		})
	}

	// Add subfolders
	for _, sub := range folder.Subfolders {
		content.Subfolders = append(content.Subfolders, drive.Folder{
			ID:   sub.ID,
			Name: sub.Name,
			Link: sub.Link,
		})
	}

	return content, nil
}

// GetFolderByName mocks getting a folder by name
func (m *MockDriveClient) GetFolderByName(ctx context.Context, name, parentID string) (*drive.Folder, error) {
	m.logCall("GetFolderByName", map[string]interface{}{"name": name, "parentID": parentID})
	
	m.mu.RLock()
	defer m.mu.RUnlock()

	if parentID == "" {
		parentID = m.rootID
	}

	for _, folder := range m.folders {
		if folder.Name == name && folder.ParentID == parentID {
			return &drive.Folder{
				ID:   folder.ID,
				Name: folder.Name,
				Link: folder.Link,
			}, nil
		}
	}

	return nil, fmt.Errorf("folder not found: %s", name)
}

// CreateFolder mocks creating a folder
func (m *MockDriveClient) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	m.logCall("CreateFolder", map[string]interface{}{"name": name, "parentID": parentID})
	
	m.mu.Lock()
	defer m.mu.Unlock()

	if parentID == "" {
		parentID = m.rootID
	}

	folderID := fmt.Sprintf("mock-folder-%d", len(m.folders))
	folder := &MockFolder{
		ID:       folderID,
		Name:     name,
		ParentID: parentID,
		Link:     fmt.Sprintf("https://drive.google.com/drive/folders/%s", folderID),
	}

	m.folders[folderID] = folder

	return folderID, nil
}

// GetOrCreateFolder mocks getting or creating a folder
func (m *MockDriveClient) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	m.logCall("GetOrCreateFolder", map[string]interface{}{"name": name, "parentID": parentID})
	
	// Try to get first
	folder, err := m.GetFolderByName(ctx, name, parentID)
	if err == nil {
		return folder.ID, nil
	}

	// Create if not found
	return m.CreateFolder(ctx, name, parentID)
}

// UploadVideo mocks uploading a video
func (m *MockDriveClient) UploadVideo(ctx context.Context, filePath, folderID, filename string) (string, error) {
	m.logCall("UploadVideo", map[string]interface{}{"filePath": filePath, "folderID": folderID, "filename": filename})
	
	m.mu.Lock()
	defer m.mu.Unlock()

	fileID := fmt.Sprintf("mock-file-%d", len(m.files))
	file := MockFile{
		ID:           fileID,
		Name:         filename,
		MimeType:     "video/mp4",
		Size:         1024 * 1024 * 10, // 10MB
		Link:         fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
		ModifiedTime: time.Now(),
		FolderID:     folderID,
	}

	m.files[fileID] = &file

	// Add to folder
	if folder, exists := m.folders[folderID]; exists {
		folder.Files = append(folder.Files, file)
	}

	return fileID, nil
}

// Helper methods for test setup

// AddMockFolder adds a mock folder for testing
func (m *MockDriveClient) AddMockFolder(id, name, parentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if parentID == "" {
		parentID = m.rootID
	}

	m.folders[id] = &MockFolder{
		ID:       id,
		Name:     name,
		ParentID: parentID,
		Link:     fmt.Sprintf("https://drive.google.com/drive/folders/%s", id),
	}
}

// AddMockFile adds a mock file for testing
func (m *MockDriveClient) AddMockFile(folderID string, file MockFile) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.files[file.ID] = &file
	
	if folder, exists := m.folders[folderID]; exists {
		folder.Files = append(folder.Files, file)
	}
}

// GetCallLog returns the recorded method calls
func (m *MockDriveClient) GetCallLog() []MockCall {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	return append([]MockCall{}, m.callLog...)
}

// Reset clears all mock data
func (m *MockDriveClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.folders = map[string]*MockFolder{
		m.rootID: {
			ID:   m.rootID,
			Name: "Root",
			Link: "https://drive.google.com/drive/folders/" + m.rootID,
		},
	}
	m.files = make(map[string]*MockFile)
	m.callLog = make([]MockCall, 0)
}

// logCall records a method call
func (m *MockDriveClient) logCall(method string, args map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	m.callLog = append(m.callLog, MockCall{
		Method: method,
		Args:   args,
		Time:   time.Now(),
	})
}

// Ensure MockDriveClient implements the interface
type driveClientInterface interface {
	ListFolders(ctx context.Context, opts drive.ListFoldersOptions) ([]drive.Folder, error)
	GetFolderContent(ctx context.Context, folderID string) (*drive.FolderContent, error)
	GetFolderByName(ctx context.Context, name, parentID string) (*drive.Folder, error)
	CreateFolder(ctx context.Context, name, parentID string) (string, error)
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	UploadVideo(ctx context.Context, filePath, folderID, filename string) (string, error)
}

// MockYouTubeClient is a mock implementation of the YouTube client
// used when the real API is not available
type MockYouTubeClient struct{}

var _ youtube.Client = (*MockYouTubeClient)(nil)

// Search mocks YouTube Client Search method
func (m *MockYouTubeClient) Search(ctx context.Context, query string, opts *youtube.SearchOptions) ([]youtube.SearchResult, error) {
	return nil, nil
}

// GetVideo mocks YouTube Client GetVideo method
func (m *MockYouTubeClient) GetVideo(ctx context.Context, videoID string) (*youtube.VideoInfo, error) {
	return nil, nil
}

// Download mocks YouTube Client Download method
func (m *MockYouTubeClient) Download(ctx context.Context, req *youtube.DownloadRequest) (*youtube.DownloadResult, error) {
	return nil, nil
}

// DownloadAudio mocks YouTube Client DownloadAudio method
func (m *MockYouTubeClient) DownloadAudio(ctx context.Context, req *youtube.AudioDownloadRequest) (*youtube.AudioDownloadResult, error) {
	return nil, nil
}

// GetChannelVideos mocks YouTube Client GetChannelVideos method
func (m *MockYouTubeClient) GetChannelVideos(ctx context.Context, channelURL string, opts *youtube.ChannelOptions) ([]youtube.SearchResult, error) {
	return nil, nil
}

// GetTrending mocks YouTube Client GetTrending method
func (m *MockYouTubeClient) GetTrending(ctx context.Context, region string, limit int) ([]youtube.SearchResult, error) {
	return nil, nil
}

// GetSubtitles mocks YouTube Client GetSubtitles method
func (m *MockYouTubeClient) GetSubtitles(ctx context.Context, videoID string, lang string) (*youtube.SubtitleInfo, error) {
	return nil, nil
}

// GetTranscript mocks YouTube Client GetTranscript method
func (m *MockYouTubeClient) GetTranscript(ctx context.Context, url string, lang string) (string, error) {
	return "", nil
}

// CheckAvailable mocks YouTube Client CheckAvailable method
func (m *MockYouTubeClient) CheckAvailable(ctx context.Context) error {
	return nil
}
