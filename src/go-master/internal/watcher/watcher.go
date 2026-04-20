// Package watcher provides unified Drive watching with event emission
package watcher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"velox/go-master/internal/runtime"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type EventType string

const (
	EventFileCreated   EventType = "file.created"
	EventFileDeleted   EventType = "file.deleted"
	EventFolderCreated EventType = "folder.created"
	EventFolderDeleted EventType = "folder.deleted"
)

type DriveEvent struct {
	Type      EventType `json:"type"`
	FolderID  string    `json:"folder_id"`
	FileID    string    `json:"file_id,omitempty"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Timestamp time.Time `json:"timestamp"`
}

type EventHandler func(event DriveEvent) error

type CycleCallback func(ctx context.Context) error

// Compile-time check that Watcher satisfies BackgroundService.
var _ runtime.BackgroundService = (*Watcher)(nil)

type Watcher struct {
	driveClient      *drive.Client
	rootFolderID     string
	handlers         map[EventType][]EventHandler
	cycleCallbacks   []CycleCallback // called after each check cycle completes
	mu               sync.RWMutex
	running          bool
	stopOnce         sync.Once     // prevents double-close panic on stopCh
	stopCh           chan struct{}
	interval         time.Duration
	lastSync         time.Time
}

func NewWatcher(driveClient *drive.Client, rootFolderID string, interval time.Duration) *Watcher {
	if interval == 0 {
		interval = 5 * time.Minute
	}
	return &Watcher{
		driveClient:    driveClient,
		rootFolderID:   rootFolderID,
		handlers:       make(map[EventType][]EventHandler),
		cycleCallbacks: nil,
		stopCh:         make(chan struct{}),
		interval:       interval,
	}
}

func (w *Watcher) RegisterHandler(eventType EventType, handler EventHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[eventType] = append(w.handlers[eventType], handler)
}

// OnCycleComplete registers a callback that fires after each periodic check
// completes. This is the mechanism by which sync services (DriveSync,
// ArtlistSync) are triggered, making the Watcher the sole polling authority.
func (w *Watcher) OnCycleComplete(cb CycleCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cycleCallbacks = append(w.cycleCallbacks, cb)
}

func (w *Watcher) Start(ctx context.Context) error {
	if w.running {
		return fmt.Errorf("watcher already running")
	}

	w.running = true
	go w.run(ctx)

	logger.Info("Drive watcher started",
		zap.String("root_folder", w.rootFolderID),
		zap.Duration("interval", w.interval),
	)

	return nil
}

func (w *Watcher) Stop() error {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.running = false
		logger.Info("Drive watcher stopped")
	})
	return nil
}

// Name returns the service name for lifecycle logging.
func (w *Watcher) Name() string { return "Watcher" }

func (w *Watcher) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.initialSync(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.checkForChanges(ctx)
		}
	}
}

func (w *Watcher) initialSync(ctx context.Context) {
	logger.Info("Running initial Drive sync")

	folders, err := w.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: w.rootFolderID,
		MaxDepth: 3,
		MaxItems: 500,
	})
	if err != nil {
		logger.Error("Initial sync failed", zap.Error(err))
		return
	}

	for _, folder := range folders {
		event := DriveEvent{
			Type:      EventFolderCreated,
			FolderID:  folder.ID,
			Name:      folder.Name,
			Path:      folder.Name,
			Timestamp: time.Now(),
		}
		w.emit(event)

		content, err := w.driveClient.GetFolderContent(ctx, folder.ID)
		if err != nil {
			continue
		}

		for _, file := range content.Files {
			if !isVideoFile(file.Name) {
				continue
			}
			fileEvent := DriveEvent{
				Type:      EventFileCreated,
				FolderID:  folder.ID,
				FileID:    file.ID,
				Name:      file.Name,
				Path:      folder.Name + "/" + file.Name,
				Timestamp: time.Now(),
			}
			w.emit(fileEvent)
		}
	}

	w.lastSync = time.Now()
	logger.Info("Initial sync completed", zap.Int("folders", len(folders)))

	// Fire cycle callbacks after initial sync so dependent sync services run once on boot
	w.fireCycleCallbacks(ctx)
}

func (w *Watcher) checkForChanges(ctx context.Context) {
	logger.Info("Checking for Drive changes")

	folders, err := w.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: w.rootFolderID,
		MaxDepth: 3,
		MaxItems: 500,
	})
	if err != nil {
		logger.Error("Change check failed", zap.Error(err))
		return
	}

	for _, folder := range folders {
		content, err := w.driveClient.GetFolderContent(ctx, folder.ID)
		if err != nil {
			continue
		}

		for _, file := range content.Files {
			if !isVideoFile(file.Name) {
				continue
			}

			event := DriveEvent{
				Type:      EventFileCreated,
				FolderID:  folder.ID,
				FileID:    file.ID,
				Name:      file.Name,
				Path:      folder.Name + "/" + file.Name,
				Timestamp: time.Now(),
			}
			w.emit(event)
		}
	}

	w.lastSync = time.Now()
	logger.Info("Change check completed")

	// Notify registered sync services to update their DBs
	w.fireCycleCallbacks(ctx)
}

// fireCycleCallbacks invokes all registered OnCycleComplete callbacks.
// This triggers DB sync services (DriveSync, ArtlistSync) after the Watcher
// has finished its Drive polling cycle, making the Watcher the sole
// authority for Drive API access.
func (w *Watcher) fireCycleCallbacks(ctx context.Context) {
	w.mu.RLock()
	callbacks := make([]CycleCallback, len(w.cycleCallbacks))
	copy(callbacks, w.cycleCallbacks)
	w.mu.RUnlock()

	for _, cb := range callbacks {
		if err := cb(ctx); err != nil {
			logger.Warn("Cycle callback failed", zap.Error(err))
		}
	}
}

func (w *Watcher) emit(event DriveEvent) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	handlers, ok := w.handlers[event.Type]
	if !ok {
		return
	}

	for _, handler := range handlers {
		if err := handler(event); err != nil {
			logger.Warn("Event handler failed",
				zap.Error(err),
				zap.String("type", string(event.Type)),
			)
		}
	}
}

func isVideoFile(name string) bool {
	lower := name
	return hasSuffix(lower, ".mp4") ||
		hasSuffix(lower, ".mov") ||
		hasSuffix(lower, ".avi") ||
		hasSuffix(lower, ".mkv") ||
		hasSuffix(lower, ".webm")
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func (w *Watcher) GetLastSyncTime() time.Time {
	return w.lastSync
}

func (w *Watcher) IsRunning() bool {
	return w.running
}
