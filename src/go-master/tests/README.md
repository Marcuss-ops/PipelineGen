# Velox Go-Master Testing

This directory contains the test suite for the Velox Go-Master API.

## Structure

```text
tests/
├── README.md
├── integration/
│   ├── main_test.go
│   ├── stock_test.go
│   └── clip_test.go
└── mocks/
    ├── drive.go
    └── rust.go
```

## Running Tests

```bash
make test
make test-unit
make test-integration
make coverage
make coverage-check
```

## Test Coverage

### Stock Processing
- `POST /api/stock/create`
- `POST /api/stock/batch-create`
- `POST /api/stock/create-studio`
- `POST /api/stock/search`
- `POST /api/stock/search-youtube`
- `POST /api/stock/process-simple`
- `POST /api/stock/find-and-create`

### Clip Management
- `POST /api/clip/search-folders`
- `POST /api/clip/read-folder-clips`
- `POST /api/clip/suggest`
- `POST /api/clip/create-subfolder`
- `POST /api/clip/subfolders`
- `POST /api/clip/download`
- `POST /api/clip/upload`

## Mock Services

### MockDriveClient
Simulates Google Drive API operations.

### MockRustBinary
Simulates the `video-stock-creator` Rust binary.

## CI/CD

Tests now run through GitHub Actions using:

```text
.github/workflows/go-master-ci.yml
```

Workflow triggers:
- push to `main`, `master`, `develop`
- pull requests targeting `main`, `master`, `develop`
- only when `src/go-master/**` or the workflow file changes

Workflow jobs/checks:
1. format check
2. `go vet`
3. unit tests
4. integration tests
5. coverage threshold check
6. build verification

## Coverage Requirements

Minimum coverage: **60%**

## Notes

- Tests use `httpexpect` for HTTP assertions.
- Mock implementations are thread-safe.
- If documentation and routes diverge, treat `src/go-master/internal/api/routes.go` as the source of truth.
