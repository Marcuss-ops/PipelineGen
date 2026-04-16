# Pipeline Verification Test Documentation

## Overview
This document provides comprehensive test procedures to verify the VeloxEditing pipeline is working correctly across all components.

## Test Environment Setup

### Prerequisites
```bash
# Ensure Go 1.18+ and Rust toolchain are installed
go version
rustc --version

# Install dependencies
cd src/go-master
make deps

# Start the server
make run
# Or: go run cmd/server/main.go
```

### Health Check
```bash
curl http://localhost:8080/health
# Expected: {"ok":true,"status":"healthy"}
```

## Test Categories

### 1. Unit Tests (Fast Validation)
```bash
make test-unit
```
**Purpose:** Validates core logic, data structures, and individual components
**Expected:** All tests pass with race detection enabled

### 2. Integration Tests (End-to-End Validation)
```bash
make test-integration
```
**Purpose:** Validates API endpoints with mocked external services
**Expected:** All integration tests pass

### 3. Full Test Suite
```bash
make test
```
**Purpose:** Runs both unit and integration tests
**Expected:** Complete test suite passes

### 4. Coverage Validation
```bash
make coverage-check
```
**Purpose:** Ensures minimum 60% code coverage
**Expected:** Coverage meets or exceeds 60% threshold

## API Endpoint Test Matrix

### Stock Processing (Agente 1) - 7 Endpoints

| Endpoint | Test | Payload | Expected Status |
|----------|------|---------|----------------|
| `POST /api/stock/create` | Single clip creation | `{"video_url":"...","title":"...","duration":60,"drive_folder":"..."}` | 200 OK |
| `POST /api/stock/batch-create` | Batch clips (3 items) | `{"drive_folder":"...","clips":[{...},{...},{...}]}` | 200 OK |
| `POST /api/stock/create-studio` | Multi-video studio | `{"links":[...],"total_duration":120,"drive_folder":"..."}` | 200 OK |
| `POST /api/stock/search` | Stock search | `{"title":"nature","max_clips":10,"drive_folder":"..."}` | 200 OK |
| `POST /api/stock/search-youtube` | YouTube search | `{"urls":[...],"clip_length":5,"drive_folder":"..."}` | 200 OK |
| `POST /api/stock/process-simple` | Simple processing | `{"videos":[...],"output_dir":"...","duration":120}` | 200 OK |
| `POST /api/stock/find-and-create` | Search & create | `{"query":"topic","max_videos":3,"drive_folder":"..."}` | 200 OK |

### Clip Management (Agente 2) - 7 Endpoints

| Endpoint | Test | Payload | Expected Status |
|----------|------|---------|----------------|
| `POST /api/clip/search-folders` | Folder search | `{"query":"clips","max_depth":3}` | 200 OK |
| `POST /api/clip/read-folder-clips` | Read clips | `{"folder_id":"..."}` | 200 OK |
| `POST /api/clip/suggest` | Clip suggestions | `{"text":"description","max_suggestions":5}` | 200 OK |
| `POST /api/clip/create-subfolder` | Create folder | `{"parent_id":"...","name":"new_folder"}` | 201 Created |
| `POST /api/clip/subfolders` | List subfolders | `{"folder_id":"..."}` | 200 OK |
| `POST /api/clip/download` | Download from YouTube | `{"url":"https://youtube.com/...","quality":"1080p"}` | 200 OK |
| `POST /api/clip/upload` | Upload to Drive | `{"file":"video.mp4","folder_id":"..."}` | 200 OK |

### Shared Infrastructure Tests

#### Health & Status
- `GET /api/health` → 200 OK with `{"ok":true,"status":"healthy"}`
- `GET /api/metrics` → 200 OK with Prometheus metrics
- `GET /api/stats` → 200 OK with server statistics

#### Job Management
- `POST /api/jobs/create` → 201 Created
- `GET /api/jobs/:id` → 200 OK with job details
- `POST /api/jobs/:id/complete` → 200 OK
- `GET /api/workers` → 200 OK with worker list

#### YouTube Operations
- `POST /api/youtube/v2/search` → 200 OK with video results
- `POST /api/youtube/v2/metadata` → 200 OK with video metadata
- `POST /api/youtube/v2/transcript` → 200 OK with transcript

#### Drive Operations
- `GET /api/drive/folders` → 200 OK with folder list
- `POST /api/drive/upload` → 200 OK with upload confirmation
- `GET /api/drive/token/refresh` → 200 OK with new token

#### Video Processing
- `POST /api/video/create-master` → 200 OK with video ID
- `POST /api/video/generate` → 200 OK with generation status
- `POST /api/video/process` → 200 OK with processing status
- `GET /api/video/status/:id` → 200 OK with status info

#### AI & GPU Features
- `POST /api/nvidia/verify` → 200 OK with verification result
- `POST /api/gpu/textgen` → 200 OK with generated text
- `GET /api/gpu/status` → 200 OK with GPU information

#### Voiceover & NLP
- `POST /api/voiceover/generate` → 200 OK with voiceover URL
- `POST /api/nlp/extract-entities` → 200 OK with entity list

#### Timestamp Mapping
- `POST /api/timestamp/map-clips` → 200 OK with mapping result

## Mock Service Configuration

### MockDriveClient
- Simulates Google Drive API operations
- Thread-safe implementation
- Configurable delays and error simulation
- Methods: ListFolders, GetFolderContent, CreateFolder, UploadVideo

### MockRustBinary
- Simulates video-stock-creator Rust binary
- Configurable processing delays
- Mock output generation
- Error simulation capabilities

## Test Execution Commands

### Quick Validation
```bash
# Run all tests
make test

# Run with coverage
make coverage

# Check coverage threshold
make coverage-check
```

### Individual Test Suites
```bash
# Unit tests only
make test-unit

# Integration tests only
make test-integration

# Run specific package
cd src/go-master
go test -v ./tests/integration/...
```

### Validation Checklist
- [ ] All unit tests pass
- [ ] All integration tests pass
- [ ] Coverage threshold met (≥60%)
- [ ] Health endpoint responds correctly
- [ ] All API endpoints return expected status codes
- [ ] Mock services respond appropriately
- [ ] Error handling works correctly
- [ ] Edge cases handled properly

## Expected Results

### Success Criteria
1. **Test Pass Rate:** 100% of tests should pass
2. **Response Times:** API responses within acceptable limits
3. **Coverage:** Minimum 60% code coverage
4. **Mock Behavior:** Mocks should simulate real service behavior accurately
5. **Error Handling:** Proper error responses for invalid inputs

### Common Failure Patterns
- Missing required fields in request payload
- Invalid status codes returned
- Mock services not properly configured
- Network connectivity issues
- Resource allocation failures

## Troubleshooting

### Test Failures
```bash
# Check server logs
tail -f server.log

# Run with verbose output
make test-unit

# Check specific test
go test -v -run TestEndpointName ./tests/integration/...
```

### Coverage Issues
```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Mock Configuration
Ensure environment variables are set:
- `VELOX_EFFECTS_DIR`
- `VELOX_RUST_STOCK_BINARY`
- `GOOGLE_CREDENTIALS_FILE`
- `GOOGLE_TOKEN_FILE`

## Integration Test Pattern

```go
// Example test structure
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

## CI/CD Integration
Tests run automatically on:
- Push to main/master/develop branches
- Pull requests
- Scheduled intervals (daily/weekly)

### GitHub Actions Jobs
1. **test** - Unit tests with coverage
2. **integration-test** - Integration tests
3. **build** - Build verification
4. **lint** - Code linting
5. **swagger** - API documentation validation

## Maintenance
- Update test data regularly
- Review and update mock responses
- Monitor test execution times
- Adjust coverage thresholds as needed
- Document new endpoint tests immediately