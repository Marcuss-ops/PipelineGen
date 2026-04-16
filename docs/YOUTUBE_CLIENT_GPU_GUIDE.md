# YouTube Client & GPU Acceleration Integration Guide

## Overview

This document describes the new unified YouTube client architecture and NVIDIA GPU acceleration for AI text generation in the VeloxEditing backend.

## Architecture Summary

### New Components Created

| Component | Location | Purpose |
|-----------|----------|---------|
| **Unified Client Interface** | `internal/youtube/client.go` | Clean interface for YouTube operations, swappable backends |
| **yt-dlp Backend** | `internal/youtube/backend_ytdlp.go` | Improved yt-dlp wrapper with retry logic, better error handling |
| **Client Factory** | `internal/youtube/factory.go` | Creates and manages YouTube client instances |
| **GPU Manager** | `internal/gpu/manager.go` | NVIDIA GPU detection, health monitoring, resource management |
| **Text Generator** | `internal/textgen/generator.go` | AI text generation with GPU acceleration (Ollama, OpenAI, Groq) |
| **API Handlers** | `internal/api/handlers/youtube_new.go`, `gpu_textgen.go` | HTTP endpoints for new functionality |

### Key Features

✅ **Unified YouTube Client Interface** - Swappable backends (yt-dlp now, native Go libraries later)
✅ **Better Error Handling** - Retry logic with exponential backoff, detailed error messages
✅ **Format Selection** - Advanced format filtering (quality, resolution, audio/video)
✅ **Authentication Support** - Browser cookies, proxy configuration
✅ **NVIDIA GPU Acceleration** - For AI text generation via Ollama
✅ **GPU Health Monitoring** - Temperature, memory, utilization checks
✅ **Backward Compatibility** - Old downloader files maintained, new system coexists

## Directory Structure

```
src/go-master/internal/
├── youtube/
│   ├── client.go              # Unified Client interface + data models
│   ├── backend_ytdlp.go       # yt-dlp backend implementation
│   ├── factory.go             # Client factory
│   ├── downloader.go          # Updated to use new client
│   ├── downloader_ytdlp.go    # Legacy (kept for compatibility)
│   ├── downloader_search.go   # Legacy (kept for compatibility)
│   └── downloader_helpers.go  # Legacy (kept for compatibility)
├── gpu/
│   └── manager.go             # NVIDIA GPU management
├── textgen/
│   └── generator.go           # AI text generation with GPU
└── api/handlers/
    ├── youtube_new.go         # New YouTube API handlers
    └── gpu_textgen.go         # GPU & Text Generation API handlers
```

## Usage Examples

### 1. Using the New YouTube Client

```go
import (
    "context"
    "velox/go-master/internal/youtube"
)

// Create client with default configuration
client, err := youtube.NewDefaultClient()
if err != nil {
    // Handle error
}

// Get video info
videoInfo, err := client.GetVideo(ctx, "VIDEO_ID")
if err != nil {
    // Handle error
}

// Download video
result, err := client.Download(ctx, &youtube.DownloadRequest{
    URL:       "https://youtube.com/watch?v=VIDEO_ID",
    OutputDir: "/path/to/downloads",
    MaxHeight: 1080,  // Max 1080p
    Format:    "best[ext=mp4]/best",
    Retries:   3,
})
if err != nil {
    // Handle error
}
fmt.Printf("Downloaded: %s\n", result.FilePath)

// Download audio only
audioResult, err := client.DownloadAudio(ctx, &youtube.AudioDownloadRequest{
    URL:         "https://youtube.com/watch?v=VIDEO_ID",
    OutputDir:   "/path/to/audio",
    AudioFormat: "mp3",
})

// Search videos
searchResults, err := client.Search(ctx, "search query", &youtube.SearchOptions{
    MaxResults: 20,
    SortBy:     "relevance",
})
```

### 2. Using GPU Acceleration for AI Text Generation

```go
import (
    "context"
    "velox/go-master/internal/gpu"
    "velox/go-master/internal/textgen"
)

// Initialize GPU manager
gpuConfig := &gpu.GPUConfig{
    Enabled:       true,
    DeviceIndex:   0,
    MinMemoryMB:   2048,  // 2GB minimum
    MaxTemp:       85,    // 85°C max
    OllamaGPU:     true,
    OllamaHost:    "localhost",
    OllamaPort:    11434,
}

gpuMgr := gpu.NewManager(gpuConfig)
err := gpuMgr.Initialize(context.Background())
if err != nil {
    // GPU not available or initialization failed
}

// Create text generator with GPU support
textGen := textgen.NewGenerator(gpuMgr, &textgen.GeneratorConfig{
    DefaultProvider:  textgen.ProviderOllama,
    DefaultModel:     "llama2",
    DefaultTemperature: 0.7,
    DefaultMaxTokens:   2048,
    GPUSupported:      true,
})

// Generate text with GPU acceleration
result, err := textGen.GenerateText(ctx, &textgen.GenerationRequest{
    Provider:  textgen.ProviderOllama,
    Model:     "llama2",
    Prompt:    "Write a script about AI and video editing",
    UseGPU:    true,  // Request GPU acceleration
})

// Generate a structured script
scriptResult, err := textGen.GenerateScript(ctx, &textgen.ScriptRequest{
    Type:         textgen.ScriptYouTube,
    Topic:        "The Future of AI in Video Production",
    TargetLength: 800,
    Language:     "en",
    Tone:         "professional",
    Keywords:     []string{"AI", "video", "automation"},
    UseGPU:       true,
})
```

### 3. API Endpoints

After registering the new handlers, these endpoints are available:

#### YouTube Operations

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/youtube/video/info?video_id=ID` | Get video metadata |
| POST | `/api/youtube/download` | Download video |
| POST | `/api/youtube/audio/download` | Download audio only |
| GET | `/api/youtube/search?query=QUERY` | Search videos |
| GET | `/api/youtube/subtitles?video_id=ID&language=en` | Get subtitles |
| GET | `/api/youtube/health` | Check YouTube client health |

#### GPU & Text Generation

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/gpu/status` | Get GPU hardware status |
| GET | `/api/gpu/list` | List all GPUs |
| POST | `/api/text/generate` | Generate text with AI |
| POST | `/api/script/generate-new` | Generate video script |
| GET | `/api/text/gpu-status` | Check GPU availability for text gen |

### 4. Format Selection Examples

```go
// Download best MP4 format under 1080p
req := &youtube.DownloadRequest{
    URL:       "https://youtube.com/watch?v=ID",
    MaxHeight: 1080,
    Format:    "best[height<=1080][ext=mp4]/best[ext=mp4]",
}

// Download only formats with audio
formats := videoInfo.Formats.WithAudio()
bestAudio := formats.Best()

// Filter by quality
hdFormats := videoInfo.Formats.Quality("720p")

// With browser cookies for authentication
req := &youtube.DownloadRequest{
    URL:         "https://youtube.com/watch?v=ID",
    CookiesFile: "/path/to/cookies.txt",
}

// With proxy
req := &youtube.DownloadRequest{
    URL:   "https://youtube.com/watch?v=ID",
    Proxy: "http://proxy:8080",
}
```

## GPU Requirements

### Hardware Requirements

- NVIDIA GPU with CUDA support (Compute Capability 3.5+)
- NVIDIA drivers installed
- CUDA toolkit (for Ollama GPU acceleration)

### Software Requirements

- `nvidia-smi` command available (comes with NVIDIA drivers)
- Ollama installed and running with GPU support
- Linux OS (CUDA support primarily on Linux)

### Checking GPU Availability

```bash
# Check if NVIDIA drivers are installed
nvidia-smi

# Check CUDA availability
nvcc --version

# Verify Ollama GPU support
ollama run llama2
```

## Migration Guide

### From Old Downloader to New Client

**Old Way:**
```go
downloader := youtube.NewDownloader("/path/to/output")
filePath, err := downloader.Download(ctx, url, "filename")
```

**New Way:**
```go
client, _ := youtube.NewDefaultClient()
result, err := client.Download(ctx, &youtube.DownloadRequest{
    URL:       url,
    OutputDir: "/path/to/output",
    OutputFile: "filename",
})
```

### Benefits of Migration

1. **Better Error Handling** - Retry logic, detailed error messages
2. **Format Selection** - Advanced filtering by quality, resolution, codecs
3. **Authentication** - Cookie-based authentication support
4. **Proxy Support** - Built-in proxy configuration
5. **Progress Tracking** - Optional progress callbacks
6. **Swappable Backends** - Easy to switch between yt-dlp and native Go libraries
7. **GPU Acceleration** - For AI text generation tasks

## Backward Compatibility

**IMPORTANT**: The old downloader files (`downloader_ytdlp.go`, `downloader_search.go`, etc.) are **kept for backward compatibility** and are NOT deleted. The new system coexists with the old system.

Existing code using the old downloaders will continue to work. New code should use the unified client interface.

## Testing

```bash
# Test the new YouTube client
cd src/go-master
go test ./internal/youtube/... -v

# Test GPU manager
go test ./internal/gpu/... -v

# Test text generation
go test ./internal/textgen/... -v

# Run all tests
make test
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `VELOX_YOUTUBE_BACKEND` | `ytdlp` | YouTube backend (ytdlp, native) |
| `VELOX_YTDLP_PATH` | `yt-dlp` | Path to yt-dlp binary |
| `VELOX_FFMPEG_PATH` | `ffmpeg` | Path to ffmpeg binary |
| `VELOX_GPU_ENABLED` | `false` | Enable GPU acceleration |
| `VELOX_GPU_DEVICE` | `0` | NVIDIA GPU device index |
| `VELOX_OLLAMA_HOST` | `localhost` | Ollama server host |
| `VELOX_OLLAMA_PORT` | `11434` | Ollama server port |
| `VELOX_DEFAULT_COOKIES` | `` | Path to browser cookies file |

### Example Configuration

```json
{
  "backend": "ytdlp",
  "ytdlp_path": "/usr/local/bin/yt-dlp",
  "ffmpeg_path": "/usr/bin/ffmpeg",
  "default_format": "best[ext=mp4]/best",
  "default_max_height": 1080,
  "default_retries": 3,
  "gpu_acceleration": true,
  "gpu_device": 0,
  "ollama_gpu": true,
  "ollama_host": "localhost",
  "ollama_port": 11434
}
```

## Future Enhancements

- [ ] Native Go YouTube backend (kkdai/youtube when Go version supports it)
- [ ] Multi-GPU support and load balancing
- [ ] GPU memory pooling for faster inference
- [ ] Automatic fallback to CPU when GPU unhealthy
- [ ] Integration with existing ML/Ollama module
- [ ] Batch download support
- [ ] Playlist downloading with concurrency
- [ ] Subtitle translation
- [ ] Video metadata enrichment

## Troubleshooting

### yt-dlp Not Found
```bash
# Install yt-dlp
pip install yt-dlp
# or
sudo apt install yt-dlp
```

### GPU Not Detected
```bash
# Check NVIDIA drivers
nvidia-smi

# If not found, install NVIDIA drivers
sudo apt install nvidia-driver-535
```

### Ollama Not Using GPU
```bash
# Check Ollama logs
journalctl -u ollama -f

# Verify CUDA availability
export CUDA_VISIBLE_DEVICES=0
ollama serve
```

### Download Fails with 403 Error
- This is usually YouTube blocking automated downloads
- Try using browser cookies: `CookiesFile: "/path/to/cookies.txt"`
- Update yt-dlp: `pip install --upgrade yt-dlp`
- Use proxy if IP is blocked

## Credits

- **yt-dlp**: https://github.com/yt-dlp/yt-dlp (The best YouTube downloader CLI)
- **kkdai/youtube**: https://github.com/kkdai/youtube (Native Go YouTube library)
- **Ollama**: https://ollama.ai (Local AI inference with GPU support)
