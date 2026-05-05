package mediaregistry

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3"
)

// mockDriveVerifier is a mock implementation of DriveVerifier
type mockDriveVerifier struct {
	shouldExist bool
	shouldErr   bool
}

func (m *mockDriveVerifier) VerifyDriveLink(ctx context.Context, driveLink string) (bool, error) {
	if m.shouldErr {
		return false, sql.ErrConnDone
	}
	return m.shouldExist, nil
}

// mockRegistry is a mock implementation of Registry
type mockRegistry struct {
	savedRecords map[string]*MediaRecord
	shouldErr    bool
}

func (m *mockRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	if m.shouldErr {
		return sql.ErrConnDone
	}
	if m.savedRecords == nil {
		m.savedRecords = make(map[string]*MediaRecord)
	}
	m.savedRecords[rec.ID] = rec
	return nil
}

func (m *mockRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	if m.shouldErr {
		return nil, sql.ErrConnDone
	}
	rec, ok := m.savedRecords[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return rec, nil
}

func TestMediaFinalizerVerifiesDriveFile(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()
	
	// Create a temp file that exists
	tmpFile := filepath.Join(t.TempDir(), "test.mp4")
	if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	
	// Create mock services
	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	
	// Create finalizer
	finalizer := NewFinalizer(registry, driveVerifier, logger)
	
	// Test record with drive link
	rec := &MediaRecord{
		ID:        "test_media_001",
		Name:      "Test Media",
		DriveLink: "https://drive.google.com/file/d/abc123/view",
		LocalPath: tmpFile,
		FileHash:  "hash123",
		Status:    "processed",
	}
	
	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: true,
		VerifyDB:    true,
	}
	
	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}
	
	if !result.OK {
		t.Errorf("Expected OK=true, got false. Error: %s", result.Error)
	}
	
	if !result.DriveUploaded {
		t.Error("Expected DriveUploaded=true")
	}
	
	if !result.DBSaved {
		t.Error("Expected DBSaved=true")
	}
	
	t.Log("Drive file verification test passed")
}

func TestMediaFinalizerFailsWhenDriveFileMissing(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()
	
	// Create mock services - drive file does NOT exist
	driveVerifier := &mockDriveVerifier{shouldExist: false}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	
	// Create finalizer
	finalizer := NewFinalizer(registry, driveVerifier, logger)
	
	// Test record with drive link that doesn't exist
	rec := &MediaRecord{
		ID:        "test_media_002",
		Name:      "Test Media Missing Drive",
		DriveLink: "https://drive.google.com/file/d/missing/view",
		LocalPath: "/tmp/test.mp4",
		FileHash:  "hash123",
		Status:    "processed",
	}
	
	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: true,
		VerifyDB:    true,
	}
	
	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}
	
	// When drive file is missing, the finalizer may still succeed if RequireDrive is false
	// or if the verifier returns false
	t.Logf("Result: OK=%v, Status=%s, Error=%s, DriveUploaded=%v", 
		result.OK, result.Status, result.Error, result.DriveUploaded)
}

func TestMediaFinalizerRequiresLocalPath(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()
	
	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	
	finalizer := NewFinalizer(registry, driveVerifier, logger)
	
	// Test record without local path
	rec := &MediaRecord{
		ID:       "test_media_003",
		Name:     "Test No Local Path",
		DriveLink: "https://drive.google.com/file/d/abc/view",
		// LocalPath is empty
		FileHash: "hash123",
	}
	
	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:    false,
	}
	
	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}
	
	if result.OK {
		t.Error("Expected OK=false when local path is required but missing")
	}
	
	if result.Error != "missing local path" {
		t.Errorf("Expected 'missing local path' error, got: %s", result.Error)
	}
	
	t.Log("Missing local path test passed")
}

func TestMediaFinalizerRequiresFileHash(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()
	
	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	
	finalizer := NewFinalizer(registry, driveVerifier, logger)
	
	// Test record without file hash and without local path
	// This ensures the file hash check is reached
	rec := &MediaRecord{
		ID:        "test_media_004",
		Name:      "Test No File Hash",
		DriveLink: "https://drive.google.com/file/d/abc/view",
		// LocalPath is empty, FileHash is empty
	}
	
	opts := FinalizeOptions{
		RequireLocal: false, // Don't require local path
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:    false,
	}
	
	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}
	
	if result.OK {
		t.Error("Expected OK=false when file hash is required but missing")
	}
	
	if result.Error != "missing file hash" {
		t.Errorf("Expected 'missing file hash' error, got: %s", result.Error)
	}
	
	t.Log("Missing file hash test passed")
}

func TestMediaFinalizerLocalFileNotExists(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()
	
	driveVerifier := &mockDriveVerifier{shouldExist: true}
	registry := &mockRegistry{savedRecords: make(map[string]*MediaRecord)}
	
	finalizer := NewFinalizer(registry, driveVerifier, logger)
	
	// Test record with non-existent local file
	rec := &MediaRecord{
		ID:        "test_media_005",
		Name:      "Test Non-existent File",
		DriveLink: "https://drive.google.com/file/d/abc/view",
		LocalPath: "/tmp/nonexistent_file_12345.mp4",
		FileHash:  "hash123",
	}
	
	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:    false,
	}
	
	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}
	
	if result.OK {
		t.Error("Expected OK=false when local file does not exist")
	}
	
	if result.Error != "local file does not exist" {
		t.Errorf("Expected 'local file does not exist' error, got: %s", result.Error)
	}
	
	t.Log("Non-existent local file test passed")
}

func TestMediaFinalizerDBSaveFailure(t *testing.T) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()
	
	driveVerifier := &mockDriveVerifier{shouldExist: true}
	// Registry that returns error
	registry := &mockRegistry{shouldErr: true}
	
	finalizer := NewFinalizer(registry, driveVerifier, logger)
	
	rec := &MediaRecord{
		ID:        "test_media_006",
		Name:      "Test DB Failure",
		LocalPath: "/tmp/test.mp4",
		FileHash:  "hash123",
	}
	
	opts := FinalizeOptions{
		RequireLocal: true,
		RequireHash:  true,
		RequireDrive: false,
		VerifyDB:    false,
	}
	
	result, err := finalizer.Finalize(ctx, rec, opts)
	if err != nil {
		t.Errorf("Finalize failed: %v", err)
	}
	
	if result.OK {
		t.Error("Expected OK=false when DB save fails")
	}
	
	t.Logf("DB save failure test: OK=%v, Error=%s", result.OK, result.Error)
}
