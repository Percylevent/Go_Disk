0# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GoDisk is a cloud storage backend system built with Go + Gin framework. It provides file upload/download, chunked resumable uploads, file deduplication via SHA256 hashing, semantic search via Qwen Embeddings API, and shareable links with access controls.

**Important**: This project uses `github.com/glebarez/sqlite` (pure Go SQLite driver) instead of `gorm.io/driver/sqlite` to avoid CGO dependency. When adding database-related code, always use the pure Go driver.

## Build and Run Commands

```bash
# Build the project
go build -o GoDisk.exe ./cmd/server/main.go

# Run directly
go run ./cmd/server/main.go

# Run the built executable
.\GoDisk.exe    # Windows

# Format code
go fmt ./...

# Tidy dependencies
go mod tidy

# Vet code
go vet ./...
```

## Configuration

Configuration is loaded from `config.yaml` via Viper. Key configuration sections:
- `server`: Port and mode (debug/release)
- `database`: SQLite file path
- `storage`: Upload paths, file size limits, user storage quotas
- `jwt`: Secret and expiration time
- `qwen`: API key for semantic search (with fallback to text search)
- `upload`: Chunk size and concurrency limits

The config package ensures required directories exist on startup via `ensureDirectories()`.

## Architecture

The codebase follows a clean layered architecture:

```
Handler Layer (internal/handler/)
    ├── HTTP request/response handling
    ├── Input validation
    └── Calls Service layer
         ↓
Service Layer (internal/service/)
    ├── Business logic
    ├── Transaction handling
    ├── Calls Storage/Model layers
    └── Dependencies injected via constructors
         ↓
Model Layer (internal/model/)
    ├── Database entities (GORM)
    ├── Database initialization
    └── Data access methods
```

### Key Architectural Patterns

**Dependency Injection**: Services are instantiated in `main.go` with dependencies passed through constructors:
```go
storageSvc := service.NewStorageService(model.DB, cfg)
fileSvc := service.NewFileService(model.DB, cfg, storageSvc)
fileHandler := handler.NewFileHandler(fileSvc, chunkSvc, embSvc)
```

**Authentication Flow**:
1. `AuthMiddleware` extracts Bearer token from Authorization header
2. Validates token using JWT package with secret from config
3. Stores `user_id` and `username` in Gin context
4. Handlers use `middleware.MustGetUserID(c)` to retrieve authenticated user

**File Upload with Deduplication**:
1. `FileService.UploadFile` calculates SHA256 hash of incoming file
2. `StorageService.SaveFile` checks if hash already exists in database
3. If exists: reuses existing file path, creates new File record pointing to same physical file
4. If new: saves to `./uploads/<hash>`, creates File record
5. Updates user's storage quota

**Chunked Upload Flow**:
1. Client calls `InitUpload` to get upload_id and total chunk count
2. Upload chunks individually with `UploadChunk` - tracks progress in `file_chunks` table
3. Client calls `CompleteUpload` to merge chunks
4. `StorageService.MergeChunks` combines chunk files into final file
5. Chunks are cleaned up after merge

**Embedding Search**:
1. Text files have content extracted and sent to Qwen API for vector embedding
2. Binary files use filename as embedding input
3. Search uses cosine similarity between query embedding and file embeddings
4. Falls back to text search if Qwen API unavailable (configurable via `qwen.fallback_to_text_search`)

### Database Models

Five core tables:
- `users`: User accounts with storage quota tracking
- `files`: File metadata with `file_hash` for deduplication, `parent_id` for folder hierarchy
- `file_chunks`: Tracks chunked upload progress via JSON array in `uploaded_chunks`
- `shares`: Share links with optional password (bcrypt), expiration, and download limits
- `embeddings`: Vector embeddings stored as JSON for semantic search

All models use soft deletes via GORM's `deleted_at` field. File hierarchy uses `parent_id` (0 = root directory).

### Global State

- `model.DB`: Global GORM database instance, initialized once in `main.go`
- `config.cfg`: Global config singleton, accessible via `config.Get()`

### Response Format

All API responses use the unified `response.Response` struct:
```go
type Response struct {
    Code    int         `json:"code"`    // 0 = success
    Message string      `json:"message"`
    Data    interface{} `json:"data,omitempty"`
}
```

Use helper functions like `response.Success()`, `response.Error()`, `response.Page()` for consistent responses.

### Share Link Security

- Share codes are 12-char hex strings generated via `crypto/rand`
- Passwords are hashed with bcrypt before storage
- Access checks via `Share.CanDownload()` verify: expiration date, download limits
- Public endpoints (`/s/:code`, `/s/:code/download`) don't require auth but enforce share restrictions

## API Route Organization

Routes are grouped in `main.go`:
- Public (no auth): `/api/auth/register`, `/api/auth/login`, `/health`
- Protected (requires auth): All other `/api/*` routes
- Public shares: `/s/:code`, `/s/:code/verify`, `/s/:code/download`

Protected routes automatically apply `AuthMiddleware` via route group nesting.
