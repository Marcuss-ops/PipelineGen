package channelmonitor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/youtube"
)

type fakeYouTubeClient struct {
	video      *youtube.VideoInfo
	transcript string
}

func (f *fakeYouTubeClient) GetVideo(ctx context.Context, videoID string) (*youtube.VideoInfo, error) {
	if f.video == nil {
		return nil, fmt.Errorf("no video configured")
	}
	cp := *f.video
	cp.ID = videoID
	return &cp, nil
}

func (f *fakeYouTubeClient) Download(ctx context.Context, req *youtube.DownloadRequest) (*youtube.DownloadResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeYouTubeClient) DownloadAudio(ctx context.Context, req *youtube.AudioDownloadRequest) (*youtube.AudioDownloadResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeYouTubeClient) Search(ctx context.Context, query string, opts *youtube.SearchOptions) ([]youtube.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeYouTubeClient) GetChannelVideos(ctx context.Context, channelURL string, opts *youtube.ChannelOptions) ([]youtube.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeYouTubeClient) GetTrending(ctx context.Context, region string, limit int) ([]youtube.SearchResult, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeYouTubeClient) GetSubtitles(ctx context.Context, videoID string, lang string) (*youtube.SubtitleInfo, error) {
	return nil, fmt.Errorf("not implemented")
}

func (f *fakeYouTubeClient) GetTranscript(ctx context.Context, url string, lang string) (string, error) {
	if strings.TrimSpace(f.transcript) == "" {
		return "", fmt.Errorf("no transcript configured")
	}
	return f.transcript, nil
}

func (f *fakeYouTubeClient) CheckAvailable(ctx context.Context) error {
	return nil
}

type mockDriveClient struct {
	mu      sync.Mutex
	nextID  int
	folders map[string]*drive.Folder
}

func newMockDriveClient() *mockDriveClient {
	m := &mockDriveClient{
		nextID:  1,
		folders: make(map[string]*drive.Folder),
	}
	m.folders["root"] = &drive.Folder{ID: "root", Name: "root"}
	return m
}

func (m *mockDriveClient) next(prefix string) string {
	id := fmt.Sprintf("%s_%d", prefix, m.nextID)
	m.nextID++
	return id
}

func (m *mockDriveClient) UploadFile(ctx context.Context, filePath, folderID, filename string) (string, error) {
	if _, err := os.Stat(filePath); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.folders[folderID]; !ok {
		return "", fmt.Errorf("unknown folder %s", folderID)
	}
	return m.next("file"), nil
}

func (m *mockDriveClient) CreateFolder(ctx context.Context, name, parentID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.next("folder")
	m.folders[id] = &drive.Folder{ID: id, Name: name, Parents: []string{parentID}}
	return id, nil
}

func (m *mockDriveClient) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, folder := range m.folders {
		if folder.Name == name && len(folder.Parents) > 0 && folder.Parents[0] == parentID {
			return folder.ID, nil
		}
	}
	id := m.next("folder")
	m.folders[id] = &drive.Folder{ID: id, Name: name, Parents: []string{parentID}}
	return id, nil
}

func (m *mockDriveClient) ListFolders(ctx context.Context, opts drive.ListFoldersOptions) ([]drive.Folder, error) {
	return m.listChildren(opts.ParentID), nil
}

func (m *mockDriveClient) ListFoldersNoRecursion(ctx context.Context, opts drive.ListFoldersOptions) ([]drive.Folder, error) {
	return m.listChildren(opts.ParentID), nil
}

func (m *mockDriveClient) listChildren(parentID string) []drive.Folder {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]drive.Folder, 0)
	for _, folder := range m.folders {
		if parentID == "" {
			if len(folder.Parents) == 0 || folder.Parents[0] == "root" {
				out = append(out, *folder)
			}
			continue
		}
		if len(folder.Parents) > 0 && folder.Parents[0] == parentID {
			out = append(out, *folder)
		}
	}
	return out
}

func TestProcessVideo(t *testing.T) {
	yt := &fakeYouTubeClient{
		video: &youtube.VideoInfo{
			Title:   "Nicholas Irving on Trump Ultimatum to Iran, The Strait of Hormuz & Stock Market (Full Interview)",
			Views:   103379,
			Channel: "DJ Vlad",
		},
		transcript: strings.Repeat("he said because the truth was important and never changed. ", 8),
	}

	driveMock := newMockDriveClient()
	monitor := NewMonitor(MonitorConfig{
		MaxClipDuration: 45,
	}, yt, driveMock, "")

	tempProcessed := t.TempDir() + "/processed.json"
	monitor.processedFile = tempProcessed
	monitor.downloadClipFn = func(ctx context.Context, videoID string, startSec, duration int, outputFile string) error {
		payload := strings.Repeat("x", 2048)
		return os.WriteFile(outputFile, []byte(payload), 0644)
	}

	res, err := monitor.ProcessVideo(context.Background(), "BpEtpjwXxNw", "HipHop")
	if err != nil {
		t.Fatalf("ProcessVideo failed: %v", err)
	}

	if res.VideoID != "BpEtpjwXxNw" {
		t.Fatalf("unexpected video id %q", res.VideoID)
	}
	if len(res.Highlights) == 0 {
		t.Fatalf("expected highlights")
	}
	if len(res.Clips) == 0 {
		t.Fatalf("expected clips")
	}
	if res.FolderPath == "" {
		t.Fatalf("expected folder path")
	}
	_ = tempProcessed
}
