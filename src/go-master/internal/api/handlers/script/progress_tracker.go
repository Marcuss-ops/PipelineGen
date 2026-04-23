package script

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"velox/go-master/pkg/logger"
)

// ProgressEvent represents a progress update event
type ProgressEvent struct {
	Type      string      `json:"type"`
	Message   string      `json:"message"`
	Progress  float64     `json:"progress"` // 0.0 to 1.0
	Data      interface{} `json:"data,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// ProgressTracker manages progress tracking for long-running operations
type ProgressTracker struct {
	mu        sync.RWMutex
	events    map[string][]ProgressEvent
	channels  map[string][]chan ProgressEvent
	maxEvents int
}

var (
	globalTracker     *ProgressTracker
	globalTrackerOnce sync.Once
)

// GetProgressTracker returns the global progress tracker instance
func GetProgressTracker() *ProgressTracker {
	globalTrackerOnce.Do(func() {
		globalTracker = &ProgressTracker{
			events:    make(map[string][]ProgressEvent),
			channels:  make(map[string][]chan ProgressEvent),
			maxEvents: 100,
		}
	})
	return globalTracker
}

// StartTracking starts tracking progress for a given operation ID
func (pt *ProgressTracker) StartTracking(operationID string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.events[operationID] = []ProgressEvent{}
	pt.channels[operationID] = []chan ProgressEvent{}
	logger.Info("Started progress tracking", zap.String("operation_id", operationID))
}

// SendProgress sends a progress update
func (pt *ProgressTracker) SendProgress(operationID, eventType, message string, progress float64, data interface{}) {
	event := ProgressEvent{
		Type:      eventType,
		Message:   message,
		Progress:  progress,
		Data:      data,
		Timestamp: time.Now(),
	}

	pt.mu.Lock()
	defer pt.mu.Unlock()

	// Store event
	if _, ok := pt.events[operationID]; ok {
		pt.events[operationID] = append(pt.events[operationID], event)
		// Trim if too many events
		if len(pt.events[operationID]) > pt.maxEvents {
			pt.events[operationID] = pt.events[operationID][1:]
		}
	}

	// Notify channels
	if channels, ok := pt.channels[operationID]; ok {
		for _, ch := range channels {
			select {
			case ch <- event:
			default:
				// Channel full, skip
			}
		}
	}

	logger.Debug("Progress update",
		zap.String("operation_id", operationID),
		zap.String("type", eventType),
		zap.Float64("progress", progress),
		zap.String("message", message))
}

// Subscribe returns a channel that receives progress events for an operation
func (pt *ProgressTracker) Subscribe(operationID string) chan ProgressEvent {
	ch := make(chan ProgressEvent, 10)

	pt.mu.Lock()
	defer pt.mu.Unlock()

	if _, ok := pt.channels[operationID]; !ok {
		pt.channels[operationID] = []chan ProgressEvent{}
	}
	pt.channels[operationID] = append(pt.channels[operationID], ch)

	return ch
}

// Unsubscribe removes a channel subscription
func (pt *ProgressTracker) Unsubscribe(operationID string, ch chan ProgressEvent) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if channels, ok := pt.channels[operationID]; ok {
		for i, c := range channels {
			if c == ch {
				pt.channels[operationID] = append(channels[:i], channels[i+1:]...)
				close(ch)
				break
			}
		}
	}
}

// GetEvents returns all events for an operation
func (pt *ProgressTracker) GetEvents(operationID string) []ProgressEvent {
	pt.mu.RLock()
	defer pt.mu.RUnlock()
	return pt.events[operationID]
}

// Complete marks an operation as complete and cleans up after a delay
func (pt *ProgressTracker) Complete(operationID string) {
	pt.SendProgress(operationID, "complete", "Operation completed", 1.0, nil)

	// Clean up after 5 minutes
	go func() {
		time.Sleep(5 * time.Minute)
		pt.mu.Lock()
		defer pt.mu.Unlock()
		delete(pt.events, operationID)
		delete(pt.channels, operationID)
		logger.Info("Cleaned up progress tracking", zap.String("operation_id", operationID))
	}()
}

// GenerateOperationID generates a unique operation ID
func GenerateOperationID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}