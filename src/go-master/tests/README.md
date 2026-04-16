# Velox Go-Master Testing

This directory contains the test suite for the Velox Go-Master API.

## Structure

```
tests/
├── README.md                 # This file
├── integration/              # Integration tests
│   ├── main_test.go         # Main test suite setup
│   ├── stock_test.go        # Stock endpoint tests (Agente 1)
│   └── clip_test.go         # Clip endpoint tests (Agente 2)
└── mocks/                    # Mock implementations
    ├── drive.go             # Mock Drive client
    └── rust.go              # Mock Rust binary
```

## Running Tests

### Run all tests
```bash
make test
```

### Run unit tests only
```bash
make test-unit
```

### Run integration tests only
```bash
make test-integration
```

### Run with coverage
```bash
make coverage
```

### Check coverage threshold (60%)
```bash
make coverage-check
```

## Test Coverage

### Agente 1 - Stock Processing (5 endpoints)
- ✅ `POST /api/stock/create` - Single clip creation
- ✅ `POST /api/stock/batch-create` - Batch clip creation
- ✅ `POST /api/stock/create-studio` - Multi-video studio creation
- ✅ `POST /api/stock/search` - Stock video search
- ✅ `POST /api/stock/search-youtube` - YouTube search
- ✅ `POST /api/stock/process-simple` - Simple processing
- ✅ `POST /api/stock/find-and-create` - Search & create pipeline

### Agente 2 - Clip Management (7 endpoints)
- ✅ `POST /api/clip/search-folders` - Drive folder search with caching
- ✅ `POST /api/clip/read-folder-clips` - Read folder clips
- ✅ `POST /api/clip/suggest` - Clip suggestions with matching
- ✅ `POST /api/clip/create-subfolder` - Create Drive folders
- ✅ `POST /api/clip/subfolders` - List subfolders
- ✅ `POST /api/clip/download` - Download from YouTube
- ✅ `POST /api/clip/upload` - Upload to Drive

## Mock Services

### MockDriveClient
Simulates Google Drive API operations:
- ListFolders
- GetFolderContent
- GetFolderByName
- CreateFolder
- GetOrCreateFolder
- UploadVideo

### MockRustBinary
Simulates video-stock-creator Rust binary:
- Execute with config
- Simulate processing delay
- Mock output generation
- Error simulation

## CI/CD

Tests run automatically on:
- Push to main/master/develop
- Pull requests

### GitHub Actions Jobs:
1. **test** - Unit tests with coverage (Go 1.18-1.21)
2. **integration-test** - Integration tests
3. **build** - Build verification
4. **lint** - Code linting
5. **swagger** - API documentation check

## Coverage Requirements

Minimum coverage: **60%**

Current status: Check CI badges

## Writing New Tests

### Integration Test Pattern
```go
func (s *TestSuite) TestEndpoint() {
    payload := map[string]interface{}{
        "field": "value",
    }
    
    s.Expect().POST("/api/endpoint").
        WithJSON(payload).
        Expect().
        Status(http.StatusOK).
        JSON().Object().
        Value("ok").Boolean().Equal(true)
}
```

### Mock Usage
```go
// Setup mock
s.MockDrive().AddMockFolder("id", "name", "parent")
s.MockRust().SetDelay(100 * time.Millisecond)

// Verify calls
calls := s.MockDrive().GetCallLog()
```

## Environment Variables

- `SKIP_INTEGRATION_TESTS` - Skip integration tests
- `VELOX_EFFECTS_DIR` - Effects directory path
- `VELOX_RUST_STOCK_BINARY` - Rust binary path
- `GOOGLE_CREDENTIALS_FILE` - Drive credentials
- `GOOGLE_TOKEN_FILE` - Drive token

## Notes

- Tests use httpexpect for HTTP assertions
 testify/suite for test organization
- Mock implementations are thread-safe
- Caching tests verify 5-minute TTL
- All tests are isolated and parallelizable