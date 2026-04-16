// Package appenddb provides append-only database for reliable storage
package appenddb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type Record struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`   // "clip", "folder", "event"
	Action    string          `json:"action"` // "create", "update", "delete"
	Data      json.RawMessage `json:"data"`
	Timestamp time.Time       `json:"timestamp"`
	Hash      string          `json:"hash,omitempty"`
}

type View struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type Database struct {
	path          string
	mu            sync.RWMutex
	records       []Record
	views         map[string]*View
	pendingWrites []Record
	writeTicker   *time.Ticker
	stopCh        chan struct{}
}

func Open(path string) (*Database, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	db := &Database{
		path:    path,
		records: []Record{},
		views:   make(map[string]*View),
		stopCh:  make(chan struct{}),
	}

	if err := db.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load db: %w", err)
		}
		logger.Info("Creating new append-only database", zap.String("path", path))
		db.records = []Record{}
		db.views = make(map[string]*View)
		if err := db.save(); err != nil {
			return nil, fmt.Errorf("failed to create db: %w", err)
		}
	}

	db.startAutoSave()

	logger.Info("Append-only database loaded",
		zap.String("path", path),
		zap.Int("records", len(db.records)),
		zap.Int("views", len(db.views)),
	)

	return db, nil
}

func (db *Database) Close() {
	close(db.stopCh)
	if db.writeTicker != nil {
		db.writeTicker.Stop()
	}
	db.save()
}

func (db *Database) load() error {
	data, err := os.ReadFile(db.path)
	if err != nil {
		return err
	}

	var dataStruct struct {
		Records []Record        `json:"records"`
		Views   map[string]View `json:"views"`
	}

	if err := json.Unmarshal(data, &dataStruct); err != nil {
		return fmt.Errorf("failed to parse db: %w", err)
	}

	db.records = dataStruct.Records

	db.views = make(map[string]*View)
	for k, v := range dataStruct.Views {
		vCopy := v
		db.views[k] = &vCopy
	}

	db.rebuildViews()

	return nil
}

func (db *Database) save() error {
	dataStruct := struct {
		Records []Record        `json:"records"`
		Views   map[string]View `json:"views"`
	}{
		Records: db.records,
		Views:   make(map[string]View),
	}

	for k, v := range db.views {
		dataStruct.Views[k] = *v
	}

	data, err := json.MarshalIndent(dataStruct, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal db: %w", err)
	}

	tmpPath := db.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write db: %w", err)
	}

	if err := os.Rename(tmpPath, db.path); err != nil {
		return fmt.Errorf("failed to rename db: %w", err)
	}

	return nil
}

func (db *Database) startAutoSave() {
	db.writeTicker = time.NewTicker(5 * time.Minute)

	go func() {
		for {
			select {
			case <-db.stopCh:
				return
			case <-db.writeTicker.C:
				db.mu.Lock()
				if len(db.pendingWrites) > 0 {
					db.save()
					db.pendingWrites = []Record{}
				}
				db.mu.Unlock()
			}
		}
	}()
}

func (db *Database) rebuildViews() {
	for _, record := range db.records {
		db.applyRecordToView(&record)
	}
}

func (db *Database) applyRecordToView(record *Record) {
	var data map[string]interface{}
	if err := json.Unmarshal(record.Data, &data); err != nil {
		return
	}

	id, ok := data["id"].(string)
	if !ok {
		id, ok = data["ID"].(string)
	}
	if !ok {
		id = record.ID
	}

	viewKey := fmt.Sprintf("%s:%s", record.Type, id)

	switch record.Action {
	case "create", "update":
		db.views[viewKey] = &View{
			ID:        id,
			Type:      record.Type,
			Data:      record.Data,
			UpdatedAt: record.Timestamp,
		}
	case "delete":
		delete(db.views, viewKey)
	}
}

func (db *Database) Append(recordType, recordID string, data interface{}) error {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	record := Record{
		ID:        recordID,
		Type:      recordType,
		Action:    "create",
		Data:      dataJSON,
		Timestamp: time.Now(),
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	db.records = append(db.records, record)
	db.applyRecordToView(&record)

	db.pendingWrites = append(db.pendingWrites, record)

	return nil
}

func (db *Database) Update(recordType, recordID string, data interface{}) error {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	record := Record{
		ID:        recordID,
		Type:      recordType,
		Action:    "update",
		Data:      dataJSON,
		Timestamp: time.Now(),
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	db.records = append(db.records, record)
	db.applyRecordToView(&record)

	db.pendingWrites = append(db.pendingWrites, record)

	return nil
}

func (db *Database) Delete(recordType, recordID string) error {
	data := map[string]string{"id": recordID}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	record := Record{
		ID:        recordID,
		Type:      recordType,
		Action:    "delete",
		Data:      dataJSON,
		Timestamp: time.Now(),
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	db.records = append(db.records, record)
	db.applyRecordToView(&record)

	db.pendingWrites = append(db.pendingWrites, record)

	return nil
}

func (db *Database) Get(recordType, recordID string) (interface{}, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	viewKey := fmt.Sprintf("%s:%s", recordType, recordID)
	view, exists := db.views[viewKey]
	if !exists {
		return nil, fmt.Errorf("record not found: %s/%s", recordType, recordID)
	}

	var result interface{}
	if err := json.Unmarshal(view.Data, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func (db *Database) GetView(recordType, recordID string) (*View, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	viewKey := fmt.Sprintf("%s:%s", recordType, recordID)
	view, exists := db.views[viewKey]
	if !exists {
		return nil, fmt.Errorf("record not found: %s/%s", recordType, recordID)
	}

	return view, nil
}

func (db *Database) List(recordType string) []View {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var results []View
	for _, view := range db.views {
		if view.Type == recordType {
			results = append(results, *view)
		}
	}

	return results
}

func (db *Database) GetRecordCount() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.records)
}

func (db *Database) GetViewCount() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.views)
}

func (db *Database) GetLastRecord() *Record {
	db.mu.RLock()
	defer db.mu.RUnlock()

	if len(db.records) == 0 {
		return nil
	}

	return &db.records[len(db.records)-1]
}

func (db *Database) GetRecordsSince(timestamp time.Time) []Record {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var results []Record
	for _, record := range db.records {
		if record.Timestamp.After(timestamp) {
			results = append(results, record)
		}
	}

	return results
}

func (db *Database) ForceSave() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.pendingWrites = []Record{}
	return db.save()
}
